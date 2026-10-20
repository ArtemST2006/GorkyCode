package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"gorkycode/backend/internal/generated/ai"
	"gorkycode/backend/internal/model"
	"strings"
	"time"
)

// validateAIResult checks the fields the backend relies on before dispatching.
func validateAIResult(result model.AiRes, request *ai.JobRequest) error {
	if result.Status != "ready" && result.Status != "needs_clarification" {
		return fmt.Errorf("invalid AI status %s", result.Status)
	}
	if result.Intent != "create_event" && result.Intent != "list_events" && result.Intent != "search_events" {
		return fmt.Errorf("invalid intent %s", result.Intent)
	}
	if result.UserId != request.UserId || result.TaskId != request.TaskId {
		return errors.New("AI response user_id or task_id does not match request")
	}

	var contextFields map[string]json.RawMessage
	if err := json.Unmarshal(result.Context, &contextFields); err != nil || contextFields == nil {
		return errors.New("invalid AI context")
	}
	var messages []ai.DialogueMessage
	if raw := bytes.TrimSpace(contextFields["recent_messages"]); len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &messages) != nil {
		return errors.New("AI context is missing recent_messages")
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
	if string(bytes.TrimSpace(result.Message)) != "null" || string(draft) != "null" {
		return errors.New("invalid AI ready message or draft")
	}
	var action map[string]json.RawMessage
	if err := json.Unmarshal(result.Action, &action); err != nil || action == nil {
		return errors.New("invalid AI action")
	}
	if err := validateReadyAction(result.Intent, action); err != nil {
		return fmt.Errorf("invalid AI action: %w", err)
	}
	return nil
}

func validateReadyAction(intent string, action map[string]json.RawMessage) error {
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
		if _, err := getString("title"); err != nil {
			return err
		}
		if _, err := checkDate("date"); err != nil {
			return err
		}
		value, err := getString("time")
		if err != nil {
			return err
		}
		parsed, err := time.Parse("15:04", value)
		if err != nil || parsed.Format("15:04") != value {
			return errors.New("invalid time")
		}
	case "list_events":
		from, err := checkDate("date_from")
		if err != nil {
			return err
		}
		to, err := checkDate("date_to_exclusive")
		if err != nil || !from.Before(to) {
			return errors.New("invalid date_to_exclusive")
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
