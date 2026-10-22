package subservice

import (
	"encoding/json"
	"errors"
	"gorkycode/backend/internal/model"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSearchEventsPassesFiltersAndMapsResults(t *testing.T) {
	userID := uuid.New()
	date := date(t, "2026-10-20")
	clock := "15:00"
	store := &fakeTaskLister{tasks: []model.Task{{
		ID: uuid.New(), UserID: userID, Type: "meeting", Title: "Встреча с Иваном",
		Date: &date, Time: &clock, Status: "completed", CreatedAt: time.Now(),
	}}}
	action := json.RawMessage(`{"query":"  Иван  ","date_from":"2026-10-20","date_to_exclusive":"2026-10-21","record_types":["meeting"]}`)
	result, err := NewLister(store).SearchEvents(t.Context(), userID, action)
	if err != nil {
		t.Fatal(err)
	}
	if !store.called || store.userID != userID || store.searchQuery != "Иван" || !slices.Equal(store.types, []string{"meeting"}) {
		t.Fatalf("search arguments = %+v", store)
	}
	if store.searchFrom == nil || store.searchFrom.Format(time.DateOnly) != "2026-10-20" || store.searchTo == nil || store.searchTo.Format(time.DateOnly) != "2026-10-21" {
		t.Fatalf("search period = %v..%v", store.searchFrom, store.searchTo)
	}
	if len(result.Items) != 1 || result.Items[0].Title != "Встреча с Иваном" || string(result.Items[0].Status) != "completed" {
		t.Fatalf("items = %+v", result.Items)
	}
}

func TestSearchEventsRejectsInvalidActionBeforeStorage(t *testing.T) {
	for _, action := range []string{
		`{}`,
		`{"query":"  "}`,
		`{"query":"Иван","date_from":"2026-10-21","date_to_exclusive":"2026-10-20"}`,
		`{"query":"Иван","record_types":["meeting","meeting"]}`,
		`{"query":"Иван","record_types":["unknown"]}`,
	} {
		store := &fakeTaskLister{}
		_, err := NewLister(store).SearchEvents(t.Context(), uuid.New(), json.RawMessage(action))
		if !errors.Is(err, model.ErrInvalidAction) || store.called {
			t.Fatalf("action %s: error = %v, store called = %v", action, err, store.called)
		}
	}
}
