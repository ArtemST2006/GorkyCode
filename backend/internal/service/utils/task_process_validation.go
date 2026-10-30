// Package utils validates AI responses before the backend performs actions.
package utils

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"gorkycode/backend/internal/generated/ai"
	"gorkycode/backend/internal/model"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ValidateAIResult checks the fields the backend relies on before dispatching.
func ValidateAIResult(result model.AiRes, request *ai.JobRequest) error {
	if result.Status != "ready" && result.Status != "needs_clarification" {
		return fmt.Errorf("invalid AI status %s", result.Status)
	}
	if result.Intent != "chat" && result.Intent != "create_event" && result.Intent != "list_events" &&
		result.Intent != "search_events" && result.Intent != "on_delete" && result.Intent != "on_update" {
		return fmt.Errorf("invalid intent %s", result.Intent)
	}
	if result.UserID != request.UserId || result.TaskID != request.TaskId {
		return errors.New("AI response user_id or task_id does not match request")
	}

	var contextFields map[string]json.RawMessage
	if err := json.Unmarshal(result.Context, &contextFields); err != nil || contextFields == nil {
		return errors.New("invalid AI context")
	}
	if len(contextFields) != 2 {
		return errors.New("AI context contains unsupported fields")
	}
	var messages []ai.DialogueMessage
	if raw := bytes.TrimSpace(contextFields["recent_messages"]); len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &messages) != nil || len(messages) == 0 {
		return errors.New("AI context is missing recent_messages")
	}
	for _, message := range messages {
		if !message.Role.Valid() || strings.TrimSpace(message.Text) == "" {
			return errors.New("AI context contains an invalid message")
		}
	}
	draft := bytes.TrimSpace(contextFields["draft"])
	if len(draft) == 0 {
		return errors.New("AI context is missing draft")
	}

	if result.Status == "needs_clarification" {
		var message string
		if err := json.Unmarshal(result.Message, &message); err != nil || strings.TrimSpace(message) == "" {
			return errors.New("AI clarification is missing message")
		}
		if string(bytes.TrimSpace(result.Action)) != "null" || draft[0] != '{' {
			return errors.New("invalid AI clarification action or draft")
		}
		return nil
	}
	var message string
	if err := json.Unmarshal(result.Message, &message); err != nil || strings.TrimSpace(message) == "" || string(draft) != "null" {
		return errors.New("invalid AI ready message or draft")
	}
	if result.Intent == "chat" {
		if string(bytes.TrimSpace(result.Action)) != "null" {
			return errors.New("chat action must be null")
		}
		return nil
	}
	var action map[string]json.RawMessage
	if err := json.Unmarshal(result.Action, &action); err != nil || action == nil {
		return errors.New("invalid AI action")
	}
	if err := validateReadyAction(result.Intent, action); err != nil {
		return fmt.Errorf("invalid AI action: %w", err)
	}
	if err := validateConfirmationContext(result.Intent, action, request); err != nil {
		return err
	}
	return nil
}

// Confirmation IDs must belong to the candidates sent in this exact turn,
// not an older list still present under another Redis key.
func validateConfirmationContext(intent string, action map[string]json.RawMessage, request *ai.JobRequest) error {
	phase, _ := actionString(action, "phase")
	if phase != "delete_confirmed" && phase != "update_confirmed" {
		return nil
	}
	raw, err := json.Marshal(request.Context)
	if err != nil {
		return err
	}
	var saved struct {
		Draft struct {
			Changes json.RawMessage `json:"changes"`
		} `json:"draft"`
		Candidates []struct {
			ID uuid.UUID `json:"id"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(raw, &saved); err != nil || len(saved.Candidates) == 0 {
		return errors.New("confirmation requires candidates in the current dialogue")
	}
	changes := bytes.TrimSpace(saved.Draft.Changes)
	isUpdate := len(changes) > 0 && string(changes) != "null"
	if (intent == "on_update") != isUpdate {
		return errors.New("confirmation intent does not match saved candidates")
	}
	field := "task_ids"
	if isUpdate {
		field = "record_ids"
	}
	var ids []uuid.UUID
	if err := json.Unmarshal(action[field], &ids); err != nil {
		return err
	}
	shown := make(map[uuid.UUID]bool, len(saved.Candidates))
	for _, candidate := range saved.Candidates {
		shown[candidate.ID] = true
	}
	for _, id := range ids {
		if !shown[id] {
			return errors.New("confirmation selects a record not shown in this turn")
		}
	}
	return nil
}

func validateReadyAction(intent string, action map[string]json.RawMessage) error {
	if intent == "on_delete" {
		return validateDeleteAction(action)
	}
	if intent == "on_update" {
		return validateUpdateAction(action)
	}
	allowed := map[string]bool{}
	switch intent {
	case "create_event":
		allowed = map[string]bool{"record_type": true, "title": true, "date": true, "time": true}
	case "list_events":
		allowed = map[string]bool{"date_from": true, "date_to_exclusive": true, "record_types": true, "statuses": true, "upcoming_minutes": true}
	case "search_events":
		allowed = map[string]bool{"query": true, "date_from": true, "date_to_exclusive": true, "record_types": true}
	}
	for name := range action {
		if !allowed[name] {
			return fmt.Errorf("unsupported action field %s", name)
		}
	}
	getString := func(name string) (string, error) {
		var value string
		if err := json.Unmarshal(action[name], &value); err != nil || strings.TrimSpace(value) == "" {
			return "", fmt.Errorf("missing or invalid %s", name)
		}
		return value, nil
	}
	checkDate := func(name string) (time.Time, error) {
		value, err := getString(name)
		if err != nil {
			return time.Time{}, err
		}
		date, err := time.Parse("2006-01-02", value)
		if err != nil || date.Format("2006-01-02") != value {
			return time.Time{}, fmt.Errorf("invalid %s", name)
		}
		return date, nil
	}
	checkTypes := func() error {
		raw, present := action["record_types"]
		if !present {
			return nil
		}
		var values []string
		if err := json.Unmarshal(raw, &values); err != nil || len(values) == 0 {
			return errors.New("invalid record_types")
		}
		seen := make(map[string]bool, len(values))
		for _, value := range values {
			if seen[value] || (value != "task" && value != "reminder" && value != "meeting") {
				return errors.New("invalid record_types")
			}
			seen[value] = true
		}
		return nil
	}

	switch intent {
	case "create_event":
		recordType, err := getString("record_type")
		if err != nil || (recordType != "task" && recordType != "reminder" && recordType != "meeting") {
			return errors.New("invalid record_type")
		}
		if _, err = getString("title"); err != nil {
			return err
		}
		if _, err = checkDate("date"); err != nil {
			return err
		}
		if raw, present := action["time"]; present && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			value, err := getString("time")
			if err != nil {
				return err
			}
			parsed, err := time.Parse("15:04", value)
			if err != nil || parsed.Format("15:04") != value {
				return errors.New("invalid time")
			}
		} else if recordType != "task" {
			return errors.New("missing or invalid time")
		}
	case "list_events":
		if raw, present := action["upcoming_minutes"]; present {
			var minutes int
			if err := json.Unmarshal(raw, &minutes); err != nil || minutes < 1 || minutes > 10080 {
				return errors.New("invalid upcoming_minutes")
			}
		}
		var from, to time.Time
		if _, present := action["date_from"]; present {
			var err error
			from, err = checkDate("date_from")
			if err != nil {
				return err
			}
		}
		if _, present := action["date_to_exclusive"]; present {
			var err error
			to, err = checkDate("date_to_exclusive")
			if err != nil {
				return err
			}
		}
		if !from.IsZero() && !to.IsZero() && !from.Before(to) {
			return errors.New("invalid date_to_exclusive")
		}
		if raw, present := action["statuses"]; present {
			var values []string
			if err := json.Unmarshal(raw, &values); err != nil || len(values) == 0 || len(values) > 3 {
				return errors.New("invalid statuses")
			}
			seen := map[string]bool{}
			for _, value := range values {
				if (value != "active" && value != "in_progress" && value != "completed") || seen[value] {
					return errors.New("invalid statuses")
				}
				seen[value] = true
			}
		}
		return checkTypes()
	case "search_events":
		if _, err := getString("query"); err != nil {
			return err
		}
		var from, to time.Time
		if _, present := action["date_from"]; present {
			var err error
			from, err = checkDate("date_from")
			if err != nil {
				return err
			}
		}
		if _, present := action["date_to_exclusive"]; present {
			var err error
			to, err = checkDate("date_to_exclusive")
			if err != nil {
				return err
			}
		}
		if !from.IsZero() && !to.IsZero() && !from.Before(to) {
			return errors.New("invalid date interval")
		}
		return checkTypes()
	}
	return nil
}

func validateUpdateAction(action map[string]json.RawMessage) error {
	phase, err := actionString(action, "phase")
	if err != nil {
		return err
	}
	switch phase {
	case "find_candidates":
		if len(action) != len(deleteFindFields)+1 {
			return errors.New("update find_candidates contains unsupported fields")
		}
		if err := validateUpdateChanges(action["changes"]); err != nil {
			return err
		}
		search := make(map[string]json.RawMessage, len(action)-1)
		for key, value := range action {
			if key != "changes" {
				search[key] = value
			}
		}
		return validateDeleteAction(search)
	case "update_confirmed":
		if len(action) != 2 {
			return errors.New("update_confirmed contains unsupported fields")
		}
		return validateDeleteAction(map[string]json.RawMessage{
			"phase":    []byte(`"delete_confirmed"`),
			"task_ids": action["record_ids"],
		})
	default:
		return fmt.Errorf("unknown update phase %s", phase)
	}
}

func validateUpdateChanges(raw json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || len(fields) == 0 {
		return errors.New("update changes must contain a field")
	}
	for name, value := range fields {
		if string(bytes.TrimSpace(value)) == "null" {
			return fmt.Errorf("update change %s cannot be null", name)
		}
		switch name {
		case "title":
			if _, err := actionString(fields, name); err != nil {
				return err
			}
		case "date":
			if _, err := actionTime(fields, name, "2006-01-02"); err != nil {
				return err
			}
		case "time":
			if _, err := actionTime(fields, name, "15:04"); err != nil {
				return err
			}
		case "status":
			status, err := actionString(fields, name)
			if err != nil || (status != "active" && status != "in_progress" && status != "completed") {
				return errors.New("invalid update status")
			}
		default:
			return fmt.Errorf("unsupported update change %s", name)
		}
	}
	return nil
}

// deleteFindFields lists the find_candidates fields; the contract requires
// all of them to be present, unfilled ones are null.
var deleteFindFields = []string{
	"phase", "title_query", "date_from", "date_to_exclusive",
	"time_from", "time_to_exclusive", "record_types",
}

// validateDeleteAction checks the phase-specific action of on_delete ready
// answers. find_candidates must carry at least one non-null search trait;
// delete_confirmed must carry at least one unique task ID.
func validateDeleteAction(action map[string]json.RawMessage) error {
	phase, err := actionString(action, "phase")
	if err != nil {
		return err
	}
	switch phase {
	case "find_candidates":
		if len(action) != len(deleteFindFields) {
			return errors.New("find_candidates contains unsupported fields")
		}
		for _, name := range deleteFindFields {
			if _, present := action[name]; !present {
				return fmt.Errorf("missing field %s", name)
			}
		}

		hasTrait := false
		if raw, present := action["title_query"]; present && string(raw) != "null" {
			if _, err = actionString(action, "title_query"); err != nil {
				return err
			}
			hasTrait = true
		}

		dateFrom, dateTo := time.Time{}, time.Time{}
		for _, name := range []string{"date_from", "date_to_exclusive"} {
			raw, present := action[name]
			if !present || string(raw) == "null" {
				continue
			}
			date, dateErr := actionTime(action, name, "2006-01-02")
			if dateErr != nil {
				return dateErr
			}
			if name == "date_from" {
				dateFrom = date
			} else {
				dateTo = date
			}
			hasTrait = true
		}
		if !dateFrom.IsZero() && !dateTo.IsZero() && !dateFrom.Before(dateTo) {
			return errors.New("invalid date interval")
		}

		timeFrom, timeTo := "", ""
		for _, name := range []string{"time_from", "time_to_exclusive"} {
			raw, present := action[name]
			if !present || string(raw) == "null" {
				continue
			}
			value, timeErr := actionTime(action, name, "15:04")
			if timeErr != nil {
				return timeErr
			}
			if name == "time_from" {
				timeFrom = value.Format("15:04")
			} else {
				timeTo = value.Format("15:04")
			}
			hasTrait = true
		}
		if timeFrom != "" && timeTo != "" && !timeFromBeforeTo(timeFrom, timeTo) {
			return errors.New("invalid time interval")
		}

		raw, present := action["record_types"]
		if present && string(raw) != "null" {
			if err = checkRecordTypes(raw); err != nil {
				return err
			}
			hasTrait = true
		}

		if !hasTrait {
			return errors.New("action is missing search traits")
		}
		return nil

	case "delete_confirmed":
		if len(action) != 2 {
			return errors.New("delete_confirmed contains unsupported fields")
		}
		raw, present := action["task_ids"]
		if !present {
			return errors.New("missing field task_ids")
		}
		var ids []string
		if err = json.Unmarshal(raw, &ids); err != nil {
			return errors.New("invalid task_ids")
		}
		if len(ids) == 0 {
			return errors.New("empty task_ids")
		}
		seen := make(map[string]bool, len(ids))
		for _, id := range ids {
			if seen[id] {
				return errors.New("duplicate task_ids")
			}
			if _, err = uuid.Parse(id); err != nil {
				return errors.New("invalid task_ids")
			}
			seen[id] = true
		}
		return nil

	default:
		return fmt.Errorf("unknown phase %s", phase)
	}
}

// timeFromBeforeTo compares HH:MM strings, both already validated.
func timeFromBeforeTo(from, to string) bool {
	fromTime, _ := time.Parse("15:04", from)
	toTime, _ := time.Parse("15:04", to)
	return fromTime.Format("15:04") < toTime.Format("15:04")
}

// actionString reads a string field.
func actionString(action map[string]json.RawMessage, name string) (string, error) {
	raw, present := action[name]
	if !present {
		return "", fmt.Errorf("missing or invalid %s", name)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("missing or invalid %s", name)
	}
	return value, nil
}

// actionTime reads a field with the given layout and rejects unpadded shorthand.
func actionTime(action map[string]json.RawMessage, name, layout string) (time.Time, error) {
	value, err := actionString(action, name)
	if err != nil {
		return time.Time{}, err
	}
	parsed, err := time.Parse(layout, value)
	if err != nil || parsed.Format(layout) != value {
		return time.Time{}, fmt.Errorf("invalid %s", name)
	}
	return parsed, nil
}

// checkRecordTypes validates a non-null record_types array.
func checkRecordTypes(raw json.RawMessage) error {
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil || len(values) == 0 {
		return errors.New("invalid record_types")
	}
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if seen[value] || (value != "task" && value != "reminder" && value != "meeting") {
			return errors.New("invalid record_types")
		}
		seen[value] = true
	}
	return nil
}
