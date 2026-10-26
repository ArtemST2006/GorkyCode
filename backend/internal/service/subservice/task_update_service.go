package subservice

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	api "gorkycode/backend/internal/generated"
	"gorkycode/backend/internal/generated/ai"
	"gorkycode/backend/internal/model"

	"github.com/google/uuid"
)

// TaskUpdateRepository commits a checked batch update atomically.
type TaskUpdateRepository interface {
	UpdateTasks(context.Context, uuid.UUID, []uuid.UUID, []model.Task, model.TaskChanges, *model.EmbeddingResult) ([]model.Task, error)
}

type UpdateCandidates struct {
	Items     []api.TaskDeleteCandidate
	Stored    []ai.UpdateCandidate
	Snapshots []model.Task
	Draft     json.RawMessage
	Changes   api.UpdateChanges
}

type TaskUpdate struct {
	rep      TaskUpdateRepository
	deleter  *TaskDelete
	embedder model.Embedder
}

func NewTaskUpdateService(rep TaskUpdateRepository, deleter *TaskDelete, embedder model.Embedder) *TaskUpdate {
	return &TaskUpdate{rep: rep, deleter: deleter, embedder: embedder}
}

// FindCandidates reuses exactly the same owner-scoped search and numbering as
// on_delete. changes are preserved separately from the search filters.
func (s *TaskUpdate) FindCandidates(ctx context.Context, userID uuid.UUID, action json.RawMessage) (UpdateCandidates, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(action, &fields); err != nil || fields == nil {
		return UpdateCandidates{}, fmt.Errorf("%w: invalid update action", model.ErrInvalidAction)
	}
	var changes api.UpdateChanges
	if err := json.Unmarshal(fields["changes"], &changes); err != nil {
		return UpdateCandidates{}, fmt.Errorf("%w: invalid changes: %w", model.ErrInvalidAction, err)
	}
	if _, err := parseTaskChanges(changes); err != nil {
		return UpdateCandidates{}, err
	}
	delete(fields, "changes")
	search, err := json.Marshal(fields)
	if err != nil {
		return UpdateCandidates{}, err
	}
	set, err := s.deleter.FindCandidates(ctx, userID, search)
	if err != nil {
		return UpdateCandidates{}, err
	}
	stored := make([]ai.UpdateCandidate, 0, len(set.Stored))
	for _, item := range set.Stored {
		raw, err := json.Marshal(item)
		if err != nil {
			return UpdateCandidates{}, err
		}
		var candidate ai.UpdateCandidate
		if err := json.Unmarshal(raw, &candidate); err != nil {
			return UpdateCandidates{}, err
		}
		stored = append(stored, candidate)
	}
	draft, err := json.Marshal(set.Draft)
	if err != nil {
		return UpdateCandidates{}, err
	}
	var draftFields map[string]json.RawMessage
	if err := json.Unmarshal(draft, &draftFields); err != nil {
		return UpdateCandidates{}, err
	}
	draftFields["changes"] = fieldsChanges(action)
	draft, err = json.Marshal(draftFields)
	if err != nil {
		return UpdateCandidates{}, err
	}
	return UpdateCandidates{
		Items: set.Items, Stored: stored, Snapshots: set.Tasks, Draft: draft, Changes: changes,
	}, nil
}

func fieldsChanges(action json.RawMessage) json.RawMessage {
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(action, &fields)
	return fields["changes"]
}

func (s *TaskUpdate) UpdateConfirmed(ctx context.Context, userID uuid.UUID, action json.RawMessage, saved []model.Task, changes api.UpdateChanges) ([]api.TaskItem, error) {
	var confirmed ai.UpdateConfirmedAction
	if err := json.Unmarshal(action, &confirmed); err != nil || confirmed.Phase != ai.UpdateConfirmedActionPhaseUpdateConfirmed {
		return nil, fmt.Errorf("%w: invalid update confirmation", model.ErrInvalidAction)
	}
	if len(confirmed.RecordIds) == 0 || len(saved) == 0 {
		return nil, model.ErrStaleSelection
	}
	shown := make(map[uuid.UUID]bool, len(saved))
	for _, item := range saved {
		shown[item.ID] = true
	}
	seen := make(map[uuid.UUID]bool, len(confirmed.RecordIds))
	for _, id := range confirmed.RecordIds {
		if !shown[id] || seen[id] {
			return nil, model.ErrStaleSelection
		}
		seen[id] = true
	}
	command, err := parseTaskChanges(changes)
	if err != nil {
		return nil, err
	}
	var embedding *model.EmbeddingResult
	if command.Title != nil {
		if s.embedder == nil {
			return nil, fmt.Errorf("%w: title embedding service is missing", model.ErrServiceUnavailable)
		}
		result, err := s.embedder.Embed(ctx, "document", *command.Title)
		if err != nil || len(result.Vector) != model.EmbeddingDimension || strings.TrimSpace(result.Model) == "" || strings.TrimSpace(result.Version) == "" {
			return nil, fmt.Errorf("%w: could not compute updated title embedding", model.ErrServiceUnavailable)
		}
		embedding = &result
	}
	tasks, err := s.rep.UpdateTasks(ctx, userID, confirmed.RecordIds, saved, command, embedding)
	if err != nil {
		return nil, fmt.Errorf("update tasks: %w", err)
	}
	byID := make(map[uuid.UUID]model.Task, len(tasks))
	for _, task := range tasks {
		byID[task.ID] = task
	}
	ordered := make([]api.TaskItem, 0, len(confirmed.RecordIds))
	for _, id := range confirmed.RecordIds {
		item, err := toItem(byID[id])
		if err != nil {
			return nil, err
		}
		ordered = append(ordered, item)
	}
	return ordered, nil
}

func parseTaskChanges(changes api.UpdateChanges) (model.TaskChanges, error) {
	var result model.TaskChanges
	if changes.Title != nil {
		value := strings.TrimSpace(*changes.Title)
		if value == "" {
			return result, fmt.Errorf("%w: empty updated title", model.ErrInvalidAction)
		}
		result.Title = &value
	}
	if changes.Date != nil {
		date := changes.Date.Time
		if date.IsZero() {
			return result, fmt.Errorf("%w: invalid updated date", model.ErrInvalidAction)
		}
		result.Date = &date
	}
	if changes.Time != nil {
		value := string(*changes.Time)
		parsed, err := time.Parse("15:04", value)
		if err != nil || parsed.Format("15:04") != value {
			return result, fmt.Errorf("%w: invalid updated time", model.ErrInvalidAction)
		}
		result.Time = &value
	}
	if changes.Status != nil {
		value := string(*changes.Status)
		if value != "active" && value != "completed" {
			return result, fmt.Errorf("%w: invalid updated status", model.ErrInvalidAction)
		}
		result.Status = &value
	}
	if result.Title == nil && result.Date == nil && result.Time == nil && result.Status == nil {
		return result, fmt.Errorf("%w: empty update changes", model.ErrInvalidAction)
	}
	return result, nil
}
