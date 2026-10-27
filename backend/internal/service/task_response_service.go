package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	api "gorkycode/backend/internal/generated"
	"gorkycode/backend/internal/model"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

func (t *TaskProcess) WithResultNarration() *TaskProcess { t.narrate = true; return t }

// Wording is optional: a committed operation must never become an HTTP error
// merely because the model could not describe its result.
func (t *TaskProcess) narrateResult(ctx context.Context, result *model.TaskResult, text, timezone string) {
	if !t.narrate || result == nil {
		return
	}
	var envelope struct {
		UserID uuid.UUID       `json:"user_id"`
		TaskID uuid.UUID       `json:"task_id"`
		Status string          `json:"status"`
		Data   json.RawMessage `json:"data"`
	}
	raw, err := json.Marshal(result.Body)
	if err != nil {
		return
	}
	if json.Unmarshal(raw, &envelope) != nil || len(envelope.Data) == 0 {
		return
	}
	payload, err := json.Marshal(struct {
		UserID   uuid.UUID       `json:"user_id"`
		TaskID   uuid.UUID       `json:"task_id"`
		Text     string          `json:"text"`
		Timezone string          `json:"timezone"`
		Intent   string          `json:"intent"`
		Status   string          `json:"status"`
		Data     json.RawMessage `json:"data"`
	}{envelope.UserID, envelope.TaskID, text, timezone, result.Intent, envelope.Status, envelope.Data})
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.aiURL, "/")+"/api/respond", bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.WarnContext(ctx, "AI wording unavailable; retaining successful result")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		slog.WarnContext(ctx, "AI wording failed; retaining successful result", "status", resp.StatusCode)
		return
	}
	var reply struct {
		UserID  uuid.UUID `json:"user_id"`
		TaskID  uuid.UUID `json:"task_id"`
		Message string    `json:"message"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&reply) != nil || decoder.Decode(&struct{}{}) != io.EOF || reply.UserID != envelope.UserID || reply.TaskID != envelope.TaskID || strings.TrimSpace(reply.Message) == "" || len([]rune(reply.Message)) > 6000 {
		return
	}
	if envelope.Status == "candidates" || envelope.Status == "update_candidates" {
		key := fmt.Sprintf("task_draft:%s:%s", envelope.UserID, envelope.TaskID)
		saved, err := t.draft.GetDraft(ctx, key)
		if err != nil {
			return
		}
		var state map[string]json.RawMessage
		if json.Unmarshal([]byte(saved), &state) != nil {
			return
		}
		var history []struct {
			Role string `json:"role"`
			Text string `json:"text"`
		}
		if json.Unmarshal(state["recent_messages"], &history) != nil || len(history) == 0 {
			return
		}
		if history[len(history)-1].Role != "assistant" {
			return
		}
		history[len(history)-1].Text = reply.Message
		state["recent_messages"], err = json.Marshal(history)
		if err != nil {
			return
		}
		body, err := json.Marshal(state)
		if err != nil {
			return
		}
		if t.draft.PutDraft(ctx, key, string(body)) != nil {
			return
		}
	}
	switch body := result.Body.(type) {
	case *api.TaskCreatedResponse:
		body.Message = reply.Message
	case *api.TaskCollectionResponse:
		body.Message = reply.Message
	case *api.TasksDeletedResponse:
		body.Message = reply.Message
	case *api.FrontendUpdated:
		body.Message = reply.Message
	case *api.TaskDeleteCandidatesResponse:
		body.Message = reply.Message
	case *api.FrontendUpdateCandidates:
		body.Message = reply.Message
	}
}
