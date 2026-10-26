package subservice

import (
	"context"
	"encoding/json"
	"errors"
	"gorkycode/backend/internal/model"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeSemanticSearcher struct {
	called  bool
	userID  uuid.UUID
	vector  []float32
	model   string
	version string
	from    *time.Time
	to      *time.Time
	types   []string
	tasks   []model.Task
	err     error
}

func (f *fakeSemanticSearcher) FindSemantic(
	_ context.Context, userID uuid.UUID, vector []float32, modelName, version string,
	from, to *time.Time, _, _ *string, types []string,
) ([]model.Task, error) {
	f.called = true
	f.userID, f.vector, f.model, f.version = userID, vector, modelName, version
	f.from, f.to, f.types = from, to, types
	return f.tasks, f.err
}

func searchTask(userID uuid.UUID, title string) model.Task {
	return model.Task{
		ID: uuid.New(), UserID: userID, Type: "meeting", Title: title,
		Status: "active", CreatedAt: time.Now(),
	}
}

func testVector() []float32 {
	vector := make([]float32, model.EmbeddingDimension)
	vector[0] = 1
	return vector
}

func TestSearchEventsCombinesLiteralAndSemanticMatches(t *testing.T) {
	userID := uuid.New()
	literal := searchTask(userID, "Зубы: запись к врачу")
	semantic := searchTask(userID, "Приём у стоматолога")
	literalStore := &fakeTaskLister{tasks: []model.Task{literal}}
	semanticStore := &fakeSemanticSearcher{tasks: []model.Task{literal, semantic}}
	embedder := &fakeEmbedder{result: model.EmbeddingResult{
		Vector: testVector(), Model: "intfloat/multilingual-e5-small", Version: "v1",
	}}
	action := json.RawMessage(`{"query":"зубы","date_from":"2026-10-20","date_to_exclusive":"2026-10-22","record_types":["meeting"]}`)
	got, err := NewSemanticLister(literalStore, semanticStore, embedder).SearchEvents(t.Context(), userID, action)
	if err != nil {
		t.Fatal(err)
	}
	if !embedder.called || embedder.purpose != "query" || embedder.text != "зубы" {
		t.Fatalf("query embedding call = %+v", embedder)
	}
	if !semanticStore.called || semanticStore.userID != userID ||
		semanticStore.model != embedder.result.Model || semanticStore.version != embedder.result.Version ||
		!slices.Equal(semanticStore.vector, embedder.result.Vector) ||
		semanticStore.from == nil || semanticStore.to == nil ||
		!slices.Equal(semanticStore.types, []string{"meeting"}) {
		t.Fatalf("semantic search arguments = %+v", semanticStore)
	}
	if len(got.Items) != 2 || got.Items[0].Id != literal.ID || got.Items[1].Id != semantic.ID {
		t.Fatalf("combined items = %+v", got.Items)
	}
}

func TestSearchEventsFindsIvanovForIvanomWithoutReturningTanya(t *testing.T) {
	userID := uuid.New()
	ivanov := searchTask(userID, "Встреча с Иванов")
	tanya := searchTask(userID, "Встреча с Таней")
	searcher := &fakeSemanticSearcher{tasks: []model.Task{ivanov, tanya}}
	embedder := &fakeEmbedder{result: model.EmbeddingResult{
		Vector: testVector(), Model: "intfloat/multilingual-e5-small", Version: "v1",
	}}
	got, err := NewSemanticLister(&fakeTaskLister{}, searcher, embedder).
		SearchEvents(t.Context(), userID, json.RawMessage(`{"query":"с Иваном","record_types":["meeting"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || got.Items[0].Id != ivanov.ID {
		t.Fatalf("search results = %+v, want only %s", got.Items, ivanov.ID)
	}
}

func TestNamedSearchDoesNotDependOnCapitalization(t *testing.T) {
	owner := uuid.New()
	ivan, tanya := searchTask(owner, "Встреча с Иваном"), searchTask(owner, "Встреча с Таней")
	for _, query := range []string{"с Таней", "с таней", "встреча с таней"} {
		got := filterNamedMatches(query, []model.Task{ivan, tanya})
		if len(got) != 1 || got[0].ID != tanya.ID {
			t.Fatalf("%q returned %+v", query, got)
		}
		if len(filterNamedMatches(query, []model.Task{ivan})) != 0 {
			t.Fatalf("%q returned another person", query)
		}
	}
}

func TestSearchEventsKeepsLiteralMatchesWhenSemanticSearchFails(t *testing.T) {
	userID := uuid.New()
	literal := searchTask(userID, "Записаться к стоматологу")
	for _, tc := range []struct {
		name     string
		embedder *fakeEmbedder
		storeErr error
	}{
		{name: "embedding unavailable", embedder: &fakeEmbedder{err: errors.New("encoder unavailable")}},
		{name: "vector query failed", embedder: &fakeEmbedder{result: model.EmbeddingResult{
			Vector: testVector(), Model: "e5", Version: "v1",
		}}, storeErr: errors.New("database unavailable")},
		{name: "invalid vector", embedder: &fakeEmbedder{result: model.EmbeddingResult{
			Vector: []float32{1}, Model: "e5", Version: "v1",
		}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			semanticStore := &fakeSemanticSearcher{err: tc.storeErr}
			got, err := NewSemanticLister(&fakeTaskLister{tasks: []model.Task{literal}}, semanticStore, tc.embedder).
				SearchEvents(t.Context(), userID, json.RawMessage(`{"query":"стоматолог"}`))
			if err != nil || len(got.Items) != 1 || got.Items[0].Id != literal.ID {
				t.Fatalf("literal fallback = %+v, %v; want the stored title match", got.Items, err)
			}
		})
	}
}
