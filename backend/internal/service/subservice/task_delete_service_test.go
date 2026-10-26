package subservice

import (
	"context"
	"encoding/json"
	"errors"
	"gorkycode/backend/internal/generated/ai"
	"gorkycode/backend/internal/model"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeTaskDeleter struct {
	tasks []model.Task
	err   error

	called   bool
	userID   uuid.UUID
	title    string
	dateFrom *time.Time
	dateTo   *time.Time
	timeFrom *string
	timeTo   *string
	types    []string

	semanticQueried bool
	semanticVector  []float32
	semanticModel   string
	semanticVersion string
	semanticTasks   []model.Task

	deletedIDs []uuid.UUID
}

func (f *fakeTaskDeleter) FindCandidates(
	_ context.Context,
	userID uuid.UUID,
	title string,
	dateFrom, dateTo *time.Time,
	timeFrom, timeTo *string,
	types []string,
) ([]model.Task, error) {
	f.called = true
	f.userID, f.title = userID, title
	f.dateFrom, f.dateTo, f.timeFrom, f.timeTo, f.types = dateFrom, dateTo, timeFrom, timeTo, types
	if f.err != nil {
		return nil, f.err
	}
	return f.tasks, nil
}

func (f *fakeTaskDeleter) FindSemantic(
	_ context.Context,
	_ uuid.UUID,
	query []float32,
	modelName, version string,
	_, _ *time.Time,
	_, _ *string,
	_ []string,
) ([]model.Task, error) {
	f.semanticQueried = true
	f.semanticVector, f.semanticModel, f.semanticVersion = query, modelName, version
	return f.semanticTasks, nil
}

func (f *fakeTaskDeleter) DeleteTasks(_ context.Context, userID uuid.UUID, ids []uuid.UUID, _ []model.Task) ([]model.Task, error) {
	f.called = true
	f.userID, f.deletedIDs = userID, ids
	if f.err != nil {
		return nil, f.err
	}
	return f.tasks, nil
}

func deleteTask(t *testing.T, userID uuid.UUID, title string) model.Task {
	t.Helper()
	date, clock := date(t, "2026-10-20"), "15:30"
	return model.Task{
		ID: uuid.New(), UserID: userID, Type: "meeting",
		Title: title, Date: &date, Time: &clock,
		Status: "active", CreatedAt: time.Now(),
	}
}

func TestFindCandidatesNumbersTasksAndPassesFilter(t *testing.T) {
	userID := uuid.New()
	first, second := deleteTask(t, userID, "Встреча"), deleteTask(t, userID, "Созвон")
	second.Time = nil
	store := &fakeTaskDeleter{tasks: []model.Task{first, second}}
	action := json.RawMessage(`{
		"phase":"find_candidates",
		"title_query":"  Иван  ",
		"date_from":"2026-10-20","date_to_exclusive":"2026-10-21",
		"time_from":"10:00","time_to_exclusive":"17:00",
		"record_types":["meeting","task"]
	}`)
	set, err := NewTaskDeleteService(store, nil).FindCandidates(t.Context(), userID, action)
	if err != nil {
		t.Fatal(err)
	}
	if !store.called || store.userID != userID || store.title != "Иван" {
		t.Fatalf("store arguments = %+v", store)
	}
	if store.dateFrom == nil || store.dateFrom.Format(time.DateOnly) != "2026-10-20" ||
		store.dateTo == nil || store.dateTo.Format(time.DateOnly) != "2026-10-21" {
		t.Fatalf("store dates = %v..%v", store.dateFrom, store.dateTo)
	}
	if store.timeFrom == nil || *store.timeFrom != "10:00" || store.timeTo == nil || *store.timeTo != "17:00" {
		t.Fatalf("store times = %v..%v", store.timeFrom, store.timeTo)
	}
	if len(store.types) != 2 || store.types[0] != "meeting" || store.types[1] != "task" {
		t.Fatalf("store types = %v", store.types)
	}

	if len(set.Items) != 2 || len(set.Stored) != 2 {
		t.Fatalf("candidates = %+v", set)
	}
	taskIDs := []uuid.UUID{first.ID, second.ID}
	for i, candidate := range set.Stored {
		if candidate.Number != i+1 || candidate.Id != taskIDs[i] {
			t.Fatalf("stored candidate %d = %+v", i, candidate)
		}
		if set.Items[i].Number != i+1 || set.Items[i].Event.Id != taskIDs[i] {
			t.Fatalf("item %d = %+v", i, set.Items[i])
		}
	}
	if set.Draft.TitleQuery == nil || *set.Draft.TitleQuery != "Иван" {
		t.Fatalf("draft = %+v", set.Draft)
	}
}

func TestFindCandidatesPassesEmptyTitleWithoutBounds(t *testing.T) {
	userID := uuid.New()
	store := &fakeTaskDeleter{tasks: []model.Task{deleteTask(t, userID, "Встреча")}}
	action := json.RawMessage(`{"phase":"find_candidates","title_query":null,"date_from":null,"date_to_exclusive":null,"time_from":"10:00","time_to_exclusive":null,"record_types":null}`)

	set, err := NewTaskDeleteService(store, &fakeEmbedder{}).FindCandidates(t.Context(), userID, action)
	if err != nil {
		t.Fatal(err)
	}
	if !store.called || store.title != "" || store.dateFrom != nil || store.dateTo != nil || store.types != nil ||
		store.timeFrom == nil || *store.timeFrom != "10:00" || store.timeTo != nil {
		t.Fatalf("store arguments = %+v", store)
	}
	if len(set.Items) != 1 || set.Items[0].Number != 1 {
		t.Fatalf("items = %+v", set.Items)
	}
}

func TestFindCandidatesAddsSemanticMatches(t *testing.T) {
	userID := uuid.New()
	sub := deleteTask(t, userID, "Встреча с Иваном")
	semantic := deleteTask(t, userID, "Совещание с Иваном")
	store := &fakeTaskDeleter{tasks: []model.Task{sub}, semanticTasks: []model.Task{sub, semantic}}
	vec := make([]float32, model.EmbeddingDimension)
	vec[0] = 0.1
	embedder := &fakeEmbedder{result: embeddingResult(vec...)}
	action := json.RawMessage(`{"phase":"find_candidates","title_query":"Иван"}`)

	set, err := NewTaskDeleteService(store, embedder).FindCandidates(t.Context(), userID, action)
	if err != nil {
		t.Fatal(err)
	}
	if !embedder.called || embedder.purpose != "query" || embedder.text != "Иван" {
		t.Fatalf("embedder = %+v", embedder)
	}
	if !store.semanticQueried || store.semanticModel != "intfloat/multilingual-e5-small" ||
		len(store.semanticVector) != model.EmbeddingDimension || store.semanticVector[0] != 0.1 {
		t.Fatalf("semantic call = %+v", store)
	}
	if len(set.Items) != 2 || set.Items[0].Event.Id != sub.ID || set.Items[1].Event.Id != semantic.ID {
		t.Fatalf("items = %+v", set.Items)
	}
	if set.Items[0].Number != 1 || set.Items[1].Number != 2 {
		t.Fatalf("numbers = %d..%d", set.Items[0].Number, set.Items[1].Number)
	}
}

func TestFindCandidatesDeduplicatesSemanticMatches(t *testing.T) {
	userID := uuid.New()
	only := deleteTask(t, userID, "Встреча с Иваном")
	store := &fakeTaskDeleter{tasks: []model.Task{only}, semanticTasks: []model.Task{only}}
	vec := make([]float32, model.EmbeddingDimension)
	vec[0] = 0.5
	action := json.RawMessage(`{"phase":"find_candidates","title_query":"Иван"}`)

	set, err := NewTaskDeleteService(store, &fakeEmbedder{result: embeddingResult(vec...)}).FindCandidates(t.Context(), userID, action)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Items) != 1 || set.Items[0].Number != 1 {
		t.Fatalf("items = %+v", set.Items)
	}
}

func TestFindCandidatesKeepsSubstringSearchOnEmbeddingFailure(t *testing.T) {
	userID := uuid.New()
	only := deleteTask(t, userID, "Встреча с Иваном")
	store := &fakeTaskDeleter{tasks: []model.Task{only}}
	action := json.RawMessage(`{"phase":"find_candidates","title_query":"Иван"}`)

	short := make([]float32, model.EmbeddingDimension-2) // wrong vector size
	uses := []*fakeEmbedder{
		{err: errors.New("embed endpoint down")},
		{result: embeddingResult(short...)},
	}
	for i, embedder := range uses {
		set, err := NewTaskDeleteService(store, embedder).FindCandidates(t.Context(), userID, action)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if store.semanticQueried {
			t.Fatalf("case %d: semantic query must not run", i)
		}
		if len(set.Items) != 1 || set.Items[0].Event.Id != only.ID {
			t.Fatalf("case %d: items = %+v", i, set.Items)
		}
	}
}

func TestFindCandidatesRejectsInvalidActionBeforeStorage(t *testing.T) {
	for _, action := range []string{
		`{"phase":"delete_confirmed","task_ids":["` + uuid.NewString() + `"]}`,
		`{"phase":"find_candidates","title_query":null,"date_from":null,"date_to_exclusive":null,"time_from":null,"time_to_exclusive":null,"record_types":null}`,
		`{"phase":"find_candidates","title_query":"  "}`,
		`{"phase":"find_candidates","date_from":"2026-10-21","date_to_exclusive":"2026-10-20"}`,
		`{"phase":"find_candidates","time_from":"17:00","time_to_exclusive":"10:00"}`,
		`{"phase":"find_candidates","time_from":"24:00"}`,
		`{"phase":"find_candidates","record_types":["meeting","meeting"]}`,
		`{"phase":"find_candidates","record_types":["unknown"]}`,
	} {
		store := &fakeTaskDeleter{}
		_, err := NewTaskDeleteService(store, nil).FindCandidates(t.Context(), uuid.New(), json.RawMessage(action))
		if !errors.Is(err, model.ErrInvalidAction) || store.called {
			t.Fatalf("action %s: error = %v, store called = %v", action, err, store.called)
		}
	}
}

func TestDeleteConfirmedChecksIDsAgainstSavedCandidates(t *testing.T) {
	userID := uuid.New()
	first, second := deleteTask(t, userID, "Встреча"), deleteTask(t, userID, "Созвон")
	saved := []ai.DeleteCandidate{
		{Number: 1, Id: first.ID},
		{Number: 2, Id: second.ID},
	}
	store := &fakeTaskDeleter{tasks: []model.Task{second, first}}
	action := json.RawMessage(`{"phase":"delete_confirmed","task_ids":["` + first.ID.String() + `","` + second.ID.String() + `"]}`)

	items, err := NewTaskDeleteService(store, nil).DeleteConfirmed(t.Context(), userID, action, saved)
	if err != nil {
		t.Fatal(err)
	}
	if !store.called || len(store.deletedIDs) != 2 || store.deletedIDs[0] != first.ID || store.deletedIDs[1] != second.ID {
		t.Fatalf("delete ids = %v", store.deletedIDs)
	}
	if len(items) != 2 || items[0].Id != first.ID || items[1].Id != second.ID {
		t.Fatalf("items = %+v", items)
	}
}

func TestDeleteConfirmedRejectsSelectionOutsideCandidates(t *testing.T) {
	userID := uuid.New()
	first, stranger := deleteTask(t, userID, "Встреча"), deleteTask(t, uuid.New(), "Чужая")

	t.Run("unknown id", func(t *testing.T) {
		store := &fakeTaskDeleter{tasks: []model.Task{first}}
		action := json.RawMessage(`{"phase":"delete_confirmed","task_ids":["` + stranger.ID.String() + `"]}`)
		items, err := NewTaskDeleteService(store, nil).DeleteConfirmed(t.Context(), userID, action, []ai.DeleteCandidate{{Number: 1, Id: first.ID}})
		if !errors.Is(err, model.ErrStaleSelection) || items != nil || store.called {
			t.Fatalf("error = %v, items = %v, store called = %v", err, items, store.called)
		}
	})

	t.Run("empty saved list", func(t *testing.T) {
		store := &fakeTaskDeleter{tasks: []model.Task{first}}
		action := json.RawMessage(`{"phase":"delete_confirmed","task_ids":["` + first.ID.String() + `"]}`)
		items, err := NewTaskDeleteService(store, nil).DeleteConfirmed(t.Context(), userID, action, nil)
		if !errors.Is(err, model.ErrStaleSelection) || items != nil || store.called {
			t.Fatalf("error = %v, items = %v, store called = %v", err, items, store.called)
		}
	})

	t.Run("missing task_ids", func(t *testing.T) {
		store := &fakeTaskDeleter{tasks: []model.Task{first}}
		items, err := NewTaskDeleteService(store, nil).DeleteConfirmed(t.Context(), userID, json.RawMessage(`{"phase":"delete_confirmed"}`), []ai.DeleteCandidate{{Number: 1, Id: first.ID}})
		if !errors.Is(err, model.ErrStaleSelection) || items != nil || store.called {
			t.Fatalf("error = %v, items = %v, store called = %v", err, items, store.called)
		}
	})
}

func TestDeleteConfirmedReportsIncompleteDeletion(t *testing.T) {
	userID := uuid.New()
	first := deleteTask(t, userID, "Встреча")
	store := &fakeTaskDeleter{err: model.ErrStaleSelection}
	action := json.RawMessage(`{"phase":"delete_confirmed","task_ids":["` + first.ID.String() + `"]}`)

	items, err := NewTaskDeleteService(store, nil).DeleteConfirmed(t.Context(), userID, action, []ai.DeleteCandidate{{Number: 1, Id: first.ID}})
	if !errors.Is(err, model.ErrStaleSelection) || items != nil {
		t.Fatalf("error = %v, items = %v, want ErrStaleSelection", err, items)
	}
}
