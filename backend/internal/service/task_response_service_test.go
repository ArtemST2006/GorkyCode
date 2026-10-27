package service

import (
	"encoding/json"
	"github.com/google/uuid"
	api "gorkycode/backend/internal/generated"
	"gorkycode/backend/internal/model"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNarrationKeepsCandidateQuestionInRedisContext(t *testing.T) {
	owner, dialogue := uuid.New(), uuid.New()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"user_id": owner, "task_id": dialogue, "message": "Какую из этих встреч удалить?"})
	}))
	defer server.Close()
	draft := &fakeDraftStore{value: `{"recent_messages":[{"role":"assistant","text":"Старый вопрос"}],"draft":{"title_query":"Иван"},"candidates":[{"number":1,"id":"saved-id"}]}`}
	process := (&TaskProcess{aiURL: server.URL, draft: draft}).WithResultNarration()
	body := &api.TaskDeleteCandidatesResponse{UserId: owner, TaskId: dialogue, Status: "candidates", Message: "Старый вопрос"}
	process.narrateResult(t.Context(), &model.TaskResult{Intent: "on_delete", Body: body}, "Удали встречу", "Europe/Moscow")
	if body.Message != "Какую из этих встреч удалить?" || !strings.Contains(draft.value, body.Message) || !strings.Contains(draft.value, "saved-id") {
		t.Fatalf("body=%+v, context=%s", body, draft.value)
	}
}

func TestNarrationUsesStoredResultAndPreservesItOnFailure(t *testing.T) {
	for _, variant := range []string{"success", "unavailable", "wrong ID", "invalid JSON"} {
		t.Run(variant, func(t *testing.T) {
			owner, dialogue := uuid.New(), uuid.New()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/respond" {
					t.Errorf("path=%s", r.URL.Path)
				}
				var input struct {
					Data struct {
						Items []api.TaskItem `json:"items"`
					} `json:"data"`
					Text   string `json:"text"`
					Intent string `json:"intent"`
				}
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
				}
				if len(input.Data.Items) != 1 || input.Data.Items[0].Title != "Приём у врача" || input.Intent != "search_events" {
					t.Errorf("input=%+v", input)
				}
				if variant == "unavailable" {
					w.WriteHeader(503)
					return
				}
				if variant == "invalid JSON" {
					_, _ = w.Write([]byte("broken"))
					return
				}
				responseOwner := owner
				if variant == "wrong ID" {
					responseOwner = uuid.New()
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"user_id": responseOwner, "task_id": dialogue, "message": "Нашёл вашу запись к врачу."})
			}))
			defer server.Close()
			body := &api.TaskCollectionResponse{UserId: owner, TaskId: dialogue, Status: "ok", Message: "Резервный ответ", Data: api.TaskCollection{Items: []api.TaskItem{{Id: uuid.New(), UserId: owner, Title: "Приём у врача"}}}}
			process := (&TaskProcess{aiURL: server.URL}).WithResultNarration()
			process.narrateResult(t.Context(), &model.TaskResult{Intent: "search_events", Body: body}, "Найди врача", "Europe/Moscow")
			want := "Резервный ответ"
			if variant == "success" {
				want = "Нашёл вашу запись к врачу."
			}
			if body.Message != want || len(body.Data.Items) != 1 || body.Data.Items[0].Title != "Приём у врача" {
				t.Fatalf("result=%+v", body)
			}
		})
	}
}

func TestClarificationDoesNotRequestNarration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("clarification called narrator") }))
	defer server.Close()
	process := (&TaskProcess{aiURL: server.URL}).WithResultNarration()
	process.narrateResult(t.Context(), &model.TaskResult{Intent: "create_event", Body: &api.TaskClarificationResponse{Message: "Когда?"}}, "Встреча", "Europe/Moscow")
}
