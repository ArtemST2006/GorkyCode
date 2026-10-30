package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"gorkycode/backend/internal/model"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// Protocol constants of contracts/backend-addition.openapi.yaml.
const (
	// authTimeout bounds the wait for the first AuthMessage.
	authTimeout = 5 * time.Second
	// pingInterval probes every idle connection.
	pingInterval = 30 * time.Second
	// readTimeout is longer than one ping: a dead peer fails a read.
	readTimeout = 65 * time.Second
	// writeTimeout bounds every single outbound control frame.
	writeTimeout = 5 * time.Second
	// maxMessageBytes is the inbound frame limit of the contract.
	maxMessageBytes = 16 * 1024
	// pendingResendLimit bounds the unacked messages resent after reconnect.
	pendingResendLimit = 100
	// maxConnectionsPerUser caps parallel WebSocket connections.
	maxConnectionsPerUser = 4
	// maxClientFrames closes the connection after repeated bad frames.
	maxClientFrames = 3

	// mailboxSize buffers pending delivery for one client. An overflow
	// closes the connection: the reconnect resends everything unacked.
	mailboxSize = 64
)

// SessionStore resolves an access token of a Redis session.
type SessionStore interface {
	Get(ctx context.Context, token string) (uuid.UUID, error)
}

// PendingStore is the acknowledged part of notifications storage.
type PendingStore interface {
	// DuePendingByUser lists unacked notifications whose moment passed.
	DuePendingByUser(ctx context.Context, userID uuid.UUID, now time.Time, limit int) ([]model.Notification, error)
	// MarkDelivered stores the acknowledgement of one notification.
	MarkDelivered(ctx context.Context, userID, notificationID uuid.UUID) (bool, error)
}

// Hub keeps the WebSocket connections and pushes fired notifications to
// their owners.
type Hub struct {
	log          *slog.Logger
	sessions     SessionStore
	store        PendingStore
	queryTimeout time.Duration
	mu           sync.Mutex
	conns        map[uuid.UUID]map[*connection]struct{}
	upgrader     *websocket.Upgrader
}

// NewHub constructs the hub.
func NewHub(log *slog.Logger, sessions SessionStore, store PendingStore, queryTimeout time.Duration) *Hub {
	return &Hub{
		log:          log,
		sessions:     sessions,
		store:        store,
		queryTimeout: queryTimeout,
		conns:        map[uuid.UUID]map[*connection]struct{}{},
		upgrader: &websocket.Upgrader{
			// Only nginx passes this route to the backend, and the caller
			// is authenticated by the first protocol message anyway.
			CheckOrigin: func(*http.Request) bool { return true },
		},
	}
}

// dispatch sends the notification to every client of the user it belongs to.
func (h *Hub) dispatch(n model.Notification) {
	h.mu.Lock()
	targets := make([]*connection, 0, len(h.conns[n.UserID]))
	for conn := range h.conns[n.UserID] {
		targets = append(targets, conn)
	}
	h.mu.Unlock()

	for _, conn := range targets {
		conn.enqueueNotification(n)
	}
}

// Run consumes the fired notification channel until it closes or the
// context is done.
func (h *Hub) Run(ctx context.Context, in <-chan model.Notification) {
	for {
		select {
		case <-ctx.Done():
			return
		case n, ok := <-in:
			if !ok {
				return
			}
			h.dispatch(n)
		}
	}
}

// Handle upgrades the request and serves one client until it disconnects.
func (h *Hub) Handle(w http.ResponseWriter, r *http.Request) {
	ws, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.log.ErrorContext(r.Context(), "websocket upgrade failed", slog.Any("error", err))
		return
	}
	h.serve(r, ws)
}

// serve implements the server-stream half of the protocol: auth, ready,
// the unacked resend and the ack consumption loop.
func (h *Hub) serve(r *http.Request, ws *websocket.Conn) {
	defer func() { _ = ws.Close() }()

	// The contract gives the client five seconds to authenticate. Redis
	// resolution uses a detached context: after the upgrade the socket does
	// not outlive the HTTP request lifecycle any more.
	authCtx, cancel := context.WithTimeout(context.Background(), h.queryTimeout)
	defer cancel()

	user, err := h.readAuth(authCtx, r, ws)
	if err != nil {
		return
	}

	conn := newConnection(user, ws)
	if !h.register(conn) {
		return
	}
	defer h.unregister(conn)
	go conn.writer(h.log)
	conn.enqueueReady(user)

	// Everything unacked and due is resent after a reconnect; future
	// moments arrive at their own scheduled time.
	if err = h.resendPending(r, conn); err != nil {
		h.log.ErrorContext(r.Context(), "pending notifications resend failed",
			slog.String("user_id", user.String()), slog.Any("error", err))
		conn.shutdown()
		return
	}

	// Gorilla's default pong handler does not update the read deadline, so an
	// idle client that never sends application frames would be closed at
	// readTimeout even though the server's pings are answered by Pongs.
	// Reset the deadline on every pong to keep healthy idle sockets alive.
	conn.ws.SetPongHandler(func(string) error {
		return conn.ws.SetReadDeadline(time.Now().Add(readTimeout))
	})
	// The contract limits client frames to 16 KiB; an oversize frame closes
	// the connection with 1009 through the WebSocket library itself.
	conn.ws.SetReadLimit(maxMessageBytes)
	h.readLoop(r, conn)
	conn.shutdown()
}

// readAuth waits for the first frame and resolves it to a user ID.
func (h *Hub) readAuth(authCtx context.Context, r *http.Request, ws *websocket.Conn) (uuid.UUID, error) {
	_ = ws.SetReadDeadline(time.Now().Add(authTimeout))
	mt, data, err := ws.ReadMessage()
	if err != nil {
		closeNow(ws, closeNoAuth, "auth timeout")
		return uuid.Nil, errors.New("auth frame read failed")
	}
	if mt != websocket.TextMessage {
		closeNow(ws, closeBadData, "binary frames are not supported")
		return uuid.Nil, errors.New("binary auth frame")
	}

	var auth authMessage
	if err = json.Unmarshal(data, &auth); err != nil || auth.Type != msgAuth || auth.Token == "" {
		closeNow(ws, closePolicy, "invalid auth message")
		return uuid.Nil, errors.New("invalid auth message")
	}

	userID, err := h.sessions.Get(authCtx, auth.Token)
	if err != nil {
		closeNow(ws, closeBadToken, "invalid token")
		return uuid.Nil, errors.New("unknown session token: " + err.Error())
	}

	h.log.InfoContext(r.Context(), "websocket client authenticated", slog.String("user_id", userID.String()))
	return userID, nil
}

// register starts tracking the connection; a user over the limit closes it.
func (h *Hub) register(conn *connection) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.conns[conn.user]) >= maxConnectionsPerUser {
		conn.sendControl(closePolicy, "too many connections")
		_ = conn.ws.Close()
		return false
	}
	set, ok := h.conns[conn.user]
	if !ok {
		set = map[*connection]struct{}{}
		h.conns[conn.user] = set
	}
	set[conn] = struct{}{}
	return true
}

// unregister forgets a closed connection.
func (h *Hub) unregister(conn *connection) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if set, ok := h.conns[conn.user]; ok {
		delete(set, conn)
		if len(set) == 0 {
			delete(h.conns, conn.user)
		}
	}
}

// resendPending pushes the due unacked records into the client mailbox.
func (h *Hub) resendPending(r *http.Request, conn *connection) error {
	queryCtx, cancel := context.WithTimeout(r.Context(), h.queryTimeout)
	defer cancel()

	pending, err := h.store.DuePendingByUser(queryCtx, conn.user, time.Now(), pendingResendLimit)
	if err != nil {
		return err
	}
	for _, n := range pending {
		if !conn.enqueueNotification(n) {
			return nil // The mailbox overflowed; the connection is closing.
		}
	}
	return nil
}

// readLoop consumes the ack stream of an authenticated client.
func (h *Hub) readLoop(r *http.Request, conn *connection) {
	badFrames := 0
	for {
		if err := conn.ws.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
			return
		}
		mt, data, err := conn.ws.ReadMessage()
		if err != nil {
			return
		}
		if mt != websocket.TextMessage {
			conn.sendControl(closeBadData, "binary frames are not supported")
			_ = conn.ws.Close()
			return
		}
		if code := h.handleFrame(r, conn, data); code != "" {
			badFrames++
			if badFrames >= maxClientFrames {
				conn.sendControl(closePolicy, "too many invalid messages")
				_ = conn.ws.Close()
				return
			}
		}
	}
}

// handleFrame processes one text frame; returns the error code or "".
func (h *Hub) handleFrame(r *http.Request, conn *connection, data []byte) string {
	msgType, err := parseClientType(data)
	if err != nil {
		_ = conn.enqueueError(errInvalidMessage, "Некорректное сообщение.")
		return errInvalidMessage
	}
	switch msgType {
	case msgAck:
		var ack ackMessage
		if err = json.Unmarshal(data, &ack); err != nil || ack.NotificationID == uuid.Nil {
			_ = conn.enqueueError(errInvalidMessage, "Некорректное подтверждение.")
			return errInvalidMessage
		}
		queryCtx, cancel := context.WithTimeout(r.Context(), h.queryTimeout)
		defer cancel()
		delivered, err := h.store.MarkDelivered(queryCtx, conn.user, ack.NotificationID)
		if err != nil {
			h.log.ErrorContext(r.Context(), "ack mark failed",
				slog.String("user_id", conn.user.String()),
				slog.String("notification_id", ack.NotificationID.String()),
				slog.Any("error", err))
		}
		if !delivered {
			_ = conn.enqueueError(errUnknownNotification, "Уведомление не найдено.")
			return errUnknownNotification
		}
		return ""
	default:
		_ = conn.enqueueError(errInvalidMessage, "Неизвестный тип сообщения.")
		return errInvalidMessage
	}
}
