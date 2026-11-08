package notifications

import (
	"context"
	"errors"
	"gorkycode/backend/internal/model"
	"gorkycode/backend/internal/reminder"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

var errTestNotFound = errors.New("session not found")

type fakeSessionStore struct {
	users map[string]uuid.UUID
}

func (f *fakeSessionStore) Get(_ context.Context, token string) (uuid.UUID, error) {
	if user, ok := f.users[token]; ok {
		return user, nil
	}
	return uuid.Nil, errTestNotFound
}

type fakePendingStore struct {
	mu        sync.Mutex
	pending   map[uuid.UUID][]model.Notification
	delivered map[uuid.UUID]map[uuid.UUID]bool
}

func (f *fakePendingStore) DuePendingByUser(
	_ context.Context, userID uuid.UUID, _ time.Time, _ int,
) ([]model.Notification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]model.Notification(nil), f.pending[userID]...), nil
}

func (f *fakePendingStore) MarkDelivered(_ context.Context, userID, id uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.delivered[userID] == nil {
		f.delivered[userID] = map[uuid.UUID]bool{}
	}
	f.delivered[userID][id] = true
	return true, nil
}

func TestHub_AuthenticatesAndSendsPending(t *testing.T) {
	userID := uuid.MustParse("7eef3f0d-4469-4f34-9f66-79d1112c2b7c")
	token := "opaque"
	notificationID := uuid.MustParse("a1e39b02-5346-4b53-a792-e4fcf5121c79")
	taskID := uuid.MustParse("b2e47c10-6283-4103-9d15-ed22addb0d59")

	pending := &fakePendingStore{
		pending: map[uuid.UUID][]model.Notification{
			userID: {{
				ID:        notificationID,
				UserID:    userID,
				TaskID:    taskID,
				Kind:      model.NotificationKindReminderDue,
				Title:     "Чай",
				Message:   "Чай заварился.",
				DueAt:     time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC),
				Status:    model.NotificationStatusPending,
				CreatedAt: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC),
			}},
		},
		delivered: map[uuid.UUID]map[uuid.UUID]bool{},
	}
	hub := NewHub(slog.New(slog.DiscardHandler), &fakeSessionStore{users: map[string]uuid.UUID{token: userID}}, pending, time.Second)
	bus := reminder.NewBus(4)
	ch, stop := bus.Subscribe()
	defer stop()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx, ch)

	ts := httptest.NewServer(http.HandlerFunc(hub.Handle))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	if err = conn.WriteJSON(authMessage{Type: msgAuth, Token: token}); err != nil {
		t.Fatal(err)
	}
	var ready readyMessage
	if err = conn.ReadJSON(&ready); err != nil {
		t.Fatal(err)
	}
	if ready.Type != msgReady || ready.UserID != userID {
		t.Fatalf("ready = %+v", ready)
	}
	var notification notificationMessage
	if err = conn.ReadJSON(&notification); err != nil {
		t.Fatal(err)
	}
	if notification.Type != msgNotification || notification.Notification.ID != notificationID {
		t.Fatalf("notification = %+v", notification)
	}
	if err = conn.WriteJSON(ackMessage{Type: msgAck, NotificationID: notificationID}); err != nil {
		t.Fatal(err)
	}
	// wait for the ack to be stored
	for range 10 {
		pending.mu.Lock()
		ok := pending.delivered[userID][notificationID]
		pending.mu.Unlock()
		if ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	pending.mu.Lock()
	acked := pending.delivered[userID][notificationID]
	pending.mu.Unlock()
	if !acked {
		t.Fatalf("ack was not stored")
	}

	dispatchedID := uuid.MustParse("c3f58d11-7394-4214-a525-fd33be3c1e6a")
	err = bus.Publish(ctx, model.Notification{
		ID:        dispatchedID,
		UserID:    userID,
		TaskID:    taskID,
		Kind:      model.NotificationKindReminderDue,
		Title:     "Чай",
		Message:   "Второй.",
		DueAt:     time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC),
		Status:    model.NotificationStatusPending,
		CreatedAt: time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	var next notificationMessage
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err = conn.ReadJSON(&next); err != nil {
		t.Fatalf("live notification read failed: %v", err)
	}
	if next.Notification.ID != dispatchedID {
		t.Fatalf("live notification id = %s, want %s", next.Notification.ID, dispatchedID)
	}
}
