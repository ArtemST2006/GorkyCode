package notifications

import (
	"gorkycode/backend/internal/model"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// outbound is one queued server frame: a notification or an error answer or
// the ready confirmation.
type outbound struct {
	notification *model.Notification
	code         string
	message      string
	readyUserID  uuid.UUID
}

// connection is one authenticated client and its outbound mailbox. Every
// outbound frame goes through the queue of the single writer goroutine: the
// WebSocket API allows exactly one concurrent writer.
type connection struct {
	user uuid.UUID
	ws   *websocket.Conn
	mail chan outbound
	mu   sync.Mutex
	// seen keeps the IDs already handed to this alive socket, so the live
	// dispatch and the reconnect snapshot never deliver a duplicate within
	// one session.
	seen map[string]struct{}
	stop chan struct{}
	once sync.Once
}

func newConnection(user uuid.UUID, ws *websocket.Conn) *connection {
	return &connection{
		user: user,
		ws:   ws,
		mail: make(chan outbound, mailboxSize),
		seen: map[string]struct{}{},
		stop: make(chan struct{}),
	}
}

// enqueueReady offers the ready confirmation frame first.
func (c *connection) enqueueReady(userID uuid.UUID) {
	select {
	case c.mail <- outbound{readyUserID: userID}:
	default:
	}
}

// enqueueNotification offers the notification without blocking. false means
// the mailbox is full and the serving goroutine stops the connection: the
// reconnect resends everything unacknowledged, per the contract.
func (c *connection) enqueueNotification(n model.Notification) bool {
	c.mu.Lock()
	if _, sent := c.seen[n.ID.String()]; sent {
		c.mu.Unlock()
		return true
	}
	select {
	case c.mail <- outbound{notification: &n}:
		c.seen[n.ID.String()] = struct{}{}
		c.mu.Unlock()
		return true
	default:
		c.mu.Unlock()
		c.sendControl(closeSlowQueue, "slow connection")
		_ = c.ws.Close()
		return false
	}
}

// enqueueError queues an error answer for the client.
func (c *connection) enqueueError(code, message string) error {
	select {
	case c.mail <- outbound{code: code, message: message}:
		return nil
	default:
		return websocket.ErrCloseSent
	}
}

// sendControl writes one control frame; the method is safe concurrently
// with the writer goroutine and the serving goroutine.
func (c *connection) sendControl(code int, text string) {
	deadline := time.Now().Add(writeTimeout)
	_ = c.ws.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(code, text), deadline)
}

func closeNow(ws *websocket.Conn, code int, text string) {
	deadline := time.Now().Add(writeTimeout)
	_ = ws.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(code, text), deadline)
	_ = ws.Close()
}

// shutdown unblocks the writer goroutine once the reader is done.
func (c *connection) shutdown() {
	c.once.Do(func() { close(c.stop) })
}

// writer serializes every outbound frame of one connection: notifications,
// error answers and the protocol pings. It exits after any write failure or
// when the serving goroutine calls shutdown.
func (c *connection) writer(log *slog.Logger) {
	ping := time.NewTicker(pingInterval)
	defer ping.Stop()

	for {
		select {
		case <-c.stop:
			return
		case <-ping.C:
			if err := c.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeTimeout)); err != nil {
				log.Debug("websocket ping failed", slog.Any("error", err))
				return
			}
		case msg, ok := <-c.mail:
			if !ok {
				return
			}
			if err := c.writeOutbound(msg); err != nil {
				log.Debug("websocket frame write failed", slog.Any("error", err))
				return
			}
		}
	}
}

func (c *connection) writeOutbound(msg outbound) error {
	deadline := time.Now().Add(writeTimeout)
	if err := c.ws.SetWriteDeadline(deadline); err != nil {
		return err
	}
	var payload any
	switch {
	case msg.readyUserID != (uuid.UUID{}):
		payload = readyMessage{Type: msgReady, UserID: msg.readyUserID}
	case msg.notification != nil:
		payload = notificationMessage{
			Type:         msgNotification,
			Notification: toPayload(*msg.notification),
		}
	default:
		payload = errorMessage{
			Type:    msgError,
			Code:    msg.code,
			Message: msg.message,
		}
	}
	return c.ws.WriteJSON(payload)
}
