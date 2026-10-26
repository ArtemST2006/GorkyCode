package subservice

import (
	"context"
	"encoding/json"
	"fmt"
	"gorkycode/backend/internal/generated/ai"
	"gorkycode/backend/internal/model"
	"strings"
	"time"

	api "gorkycode/backend/internal/generated"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// TaskDeleteRepository reads and removes the user's tasks for on_delete.
type TaskDeleteRepository interface {
	// FindCandidates filters tasks by title substring, dates, times and
	// record types: every filled filter must match (AND). The date bounds
	// are inclusive and exclusive respectively, the time bounds repeat the
	// same rule for the time column.
	FindCandidates(ctx context.Context, userID uuid.UUID, title string, dateFrom, dateTo *time.Time, timeFrom, timeTo *string, types []string) ([]model.Task, error)
	// FindSemantic supplies the tasks whose stored embedding from the same
	// model version is closest to the query vector; every non-title filter
	// applies the same way as in FindCandidates.
	FindSemantic(ctx context.Context, userID uuid.UUID, query []float32, modelName, version string, dateFrom, dateTo *time.Time, timeFrom, timeTo *string, types []string) ([]model.Task, error)
	// DeleteTasks removes the tasks in one transaction only if every ID
	// belongs to the user; it returns model.ErrStaleSelection otherwise.
	DeleteTasks(ctx context.Context, userID uuid.UUID, ids []uuid.UUID, expected []model.Task) ([]model.Task, error)
}

// DeleteCandidates collects the find_candidates result.
type DeleteCandidates struct {
	// Tasks are the exact database rows shown to the user; on_update saves
	// them as snapshots for optimistic verification at confirmation.
	Tasks []model.Task
	// Stored carries the shown numbers with task IDs for the AI context and
	// the Redis check before delete_confirmed.
	Stored []ai.DeleteCandidate
	// Items is the numbered list sent to the frontend.
	Items []api.TaskDeleteCandidate
	// Draft saves the search traits for the next AI request.
	Draft ai.DeleteSearchDraft
}

// TaskDelete implements the on_delete dialog: candidate search without
// changes in storage and confirmed deletion.
type TaskDelete struct {
	rep      TaskDeleteRepository
	embedder model.Embedder
}

// NewTaskDeleteService constructs an on_delete service. The embedder may be
// nil: then only the substring search answers find_candidates.
func NewTaskDeleteService(rep TaskDeleteRepository, embedder model.Embedder) *TaskDelete {
	return &TaskDelete{rep: rep, embedder: embedder}
}

// FindCandidates parses a find_candidates action, searches the user's tasks
// and numbers them from one. The action must keep at least one search trait.
func (s *TaskDelete) FindCandidates(
	ctx context.Context,
	userID uuid.UUID,
	action json.RawMessage,
) (DeleteCandidates, error) {
	if userID == uuid.Nil {
		return DeleteCandidates{}, fmt.Errorf("%w: empty user_id", model.ErrInvalidAction)
	}
	var act ai.DeleteFindCandidatesAction
	if err := json.Unmarshal(action, &act); err != nil {
		return DeleteCandidates{}, fmt.Errorf("%w: decode: %w", model.ErrInvalidAction, err)
	}
	if act.Phase != ai.DeleteFindCandidatesActionPhaseFindCandidates {
		return DeleteCandidates{}, fmt.Errorf("%w: unknown phase %q", model.ErrInvalidAction, act.Phase)
	}

	draft, err := draftFromAction(act)
	if err != nil {
		return DeleteCandidates{}, err
	}

	var dateFrom, dateTo *time.Time
	if draft.DateFrom != nil {
		from := draft.DateFrom.Time
		dateFrom = &from
	}
	if draft.DateToExclusive != nil {
		to := draft.DateToExclusive.Time
		dateTo = &to
	}
	var types []string
	if draft.RecordTypes != nil {
		types = make([]string, len(*draft.RecordTypes))
		for i, value := range *draft.RecordTypes {
			types[i] = string(value)
		}
	}
	tasks, err := s.rep.FindCandidates(
		ctx,
		userID,
		derefText(draft.TitleQuery),
		dateFrom,
		dateTo,
		draft.TimeFrom,
		draft.TimeToExclusive,
		types,
	)
	if err != nil {
		return DeleteCandidates{}, fmt.Errorf("find candidates: %w", err)
	}
	if draft.TitleQuery != nil && s.embedder != nil {
		extra, enabled := s.semanticCandidates(ctx, userID, derefText(draft.TitleQuery), dateFrom, dateTo, draft.TimeFrom, draft.TimeToExclusive, types)
		if enabled {
			tasks = mergeCandidates(tasks, filterNamedMatches(derefText(draft.TitleQuery), extra))
		}
	}

	stored := make([]ai.DeleteCandidate, 0, len(tasks))
	items := make([]api.TaskDeleteCandidate, 0, len(tasks))
	for _, task := range tasks {
		item, err := toItem(task)
		if err != nil {
			return DeleteCandidates{}, err
		}
		number := len(stored) + 1
		stored = append(stored, storedCandidate(number, item))
		items = append(items, api.TaskDeleteCandidate{Number: number, Event: item})
	}

	return DeleteCandidates{Tasks: tasks, Stored: stored, Items: items, Draft: draft}, nil
}

// DeleteConfirmed parses a delete_confirmed action and removes every selected
// ID, but only while every ID is inside the saved candidates. One missing ID
// fails the whole selection with model.ErrStaleSelection. The result keeps
// the order of the confirmed IDs.
func (s *TaskDelete) DeleteConfirmed(
	ctx context.Context,
	userID uuid.UUID,
	action json.RawMessage,
	saved []ai.DeleteCandidate,
) ([]api.TaskItem, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("%w: empty user_id", model.ErrInvalidAction)
	}
	var act ai.DeleteConfirmedAction
	if err := json.Unmarshal(action, &act); err != nil {
		return nil, fmt.Errorf("%w: decode: %w", model.ErrInvalidAction, err)
	}
	if act.Phase != ai.DeleteConfirmed {
		return nil, fmt.Errorf("%w: unknown phase %q", model.ErrInvalidAction, act.Phase)
	}
	if len(act.TaskIds) == 0 || len(saved) == 0 {
		return nil, model.ErrStaleSelection
	}

	shown := make(map[uuid.UUID]bool, len(saved))
	expected := make([]model.Task, 0, len(saved))
	for _, candidate := range saved {
		shown[candidate.Id] = true
		var date *time.Time
		if candidate.Date != nil {
			value := candidate.Date.Time
			date = &value
		}
		expected = append(expected, model.Task{
			ID: candidate.Id, UserID: userID, Type: string(candidate.RecordType),
			Title: candidate.Title, Date: date, Time: candidate.Time, Status: string(candidate.Status),
		})
	}
	seen := make(map[uuid.UUID]bool, len(act.TaskIds))
	for _, id := range act.TaskIds {
		if !shown[id] || seen[id] {
			return nil, model.ErrStaleSelection
		}
		seen[id] = true
	}

	tasks, err := s.rep.DeleteTasks(ctx, userID, act.TaskIds, expected)
	if err != nil {
		return nil, fmt.Errorf("delete tasks: %w", err)
	}

	byID := make(map[uuid.UUID]model.Task, len(tasks))
	for _, task := range tasks {
		byID[task.ID] = task
	}
	ordered := make([]api.TaskItem, 0, len(act.TaskIds))
	for _, id := range act.TaskIds {
		item, err := toItem(byID[id])
		if err != nil {
			return nil, err
		}
		ordered = append(ordered, item)
	}
	return ordered, nil
}

// semanticCandidates supplements the substring search with the nearest
// stored embeddings. enabled=false means the embedder step failed or gave an
// unusable vector: the substring search is the contract minimum and must
// keep working even when the optional semantic query fails.
func (s *TaskDelete) semanticCandidates(
	ctx context.Context,
	userID uuid.UUID,
	title string,
	dateFrom, dateTo *time.Time,
	timeFrom, timeTo *string,
	types []string,
) ([]model.Task, bool) {
	result, err := s.embedder.Embed(ctx, "query", title)
	if err == nil && len(result.Vector) == model.EmbeddingDimension {
		tasks, findErr := s.rep.FindSemantic(ctx, userID, result.Vector, result.Model, result.Version, dateFrom, dateTo, timeFrom, timeTo, types)
		if findErr == nil {
			return tasks, true
		}
	}
	// Эмбеддер недоступен или вернул непригодный вектор: семантическое
	// дополнение отключается, подстрочный поиск продолжает работать —
	// это минимум контракта.
	return nil, false
}

// mergeCandidates appends the semantic results the substring search missed.
func mergeCandidates(base, extra []model.Task) []model.Task {
	if len(extra) == 0 {
		return base
	}
	seen := make(map[uuid.UUID]bool, len(base))
	for _, task := range base {
		seen[task.ID] = true
	}
	merged := base
	for _, task := range extra {
		if !seen[task.ID] {
			seen[task.ID] = true
			merged = append(merged, task)
		}
	}
	return merged
}

// storedCandidate converts a shown task into the AI candidate list entry.
func storedCandidate(number int, item api.TaskItem) ai.DeleteCandidate {
	return ai.DeleteCandidate{
		Number:     number,
		Id:         item.Id,
		RecordType: ai.RecordType(item.RecordType),
		Title:      item.Title,
		Date:       item.Date,
		Time:       item.Time,
		Status:     ai.DeleteCandidateStatus(item.Status),
	}
}

// draftFromAction validates the action traits and copies them into a draft.
func draftFromAction(act ai.DeleteFindCandidatesAction) (ai.DeleteSearchDraft, error) {
	var draft ai.DeleteSearchDraft

	if act.TitleQuery != nil {
		title := strings.TrimSpace(*act.TitleQuery)
		if title == "" {
			return draft, fmt.Errorf("%w: empty title_query", model.ErrInvalidAction)
		}
		draft.TitleQuery = &title
	}

	dateFrom, err := draftDate(act.DateFrom, "date_from")
	if err != nil {
		return draft, err
	}
	dateTo, err := draftDate(act.DateToExclusive, "date_to_exclusive")
	if err != nil {
		return draft, err
	}
	if dateFrom != nil && dateTo != nil {
		from, to := dateFrom.Time, dateTo.Time
		if !from.Before(to) {
			return draft, fmt.Errorf("%w: invalid date interval", model.ErrInvalidAction)
		}
	}
	draft.DateFrom = dateFrom
	draft.DateToExclusive = dateTo

	timeFrom, err := draftTime(act.TimeFrom, "time_from")
	if err != nil {
		return draft, err
	}
	timeTo, err := draftTime(act.TimeToExclusive, "time_to_exclusive")
	if err != nil {
		return draft, err
	}
	if timeFrom != nil && timeTo != nil && *timeFrom >= *timeTo {
		return draft, fmt.Errorf("%w: invalid time interval", model.ErrInvalidAction)
	}
	draft.TimeFrom = timeFrom
	draft.TimeToExclusive = timeTo

	types, err := parseRecordTypes(act.RecordTypes)
	if err != nil {
		return draft, err
	}
	if types != nil {
		converted := make([]ai.RecordType, len(types))
		for i, value := range types {
			converted[i] = ai.RecordType(value)
		}
		draft.RecordTypes = &converted
	}

	if draft.TitleQuery == nil && draft.DateFrom == nil && draft.DateToExclusive == nil &&
		draft.TimeFrom == nil && draft.TimeToExclusive == nil && draft.RecordTypes == nil {
		return draft, fmt.Errorf("%w: action is missing search traits", model.ErrInvalidAction)
	}

	return draft, nil
}

func draftDate(value *openapi_types.Date, name string) (*openapi_types.Date, error) {
	if value == nil {
		return nil, nil
	}
	clock := value.Time
	if clock.IsZero() {
		return nil, fmt.Errorf("%w: invalid %s", model.ErrInvalidAction, name)
	}
	return value, nil
}

func draftTime(value *string, name string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	clock, err := time.Parse("15:04", *value)
	if err != nil || clock.Format("15:04") != *value {
		return nil, fmt.Errorf("%w: invalid %s", model.ErrInvalidAction, name)
	}
	return value, nil
}

// derefText returns an empty string for a missing draft field.
func derefText(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
