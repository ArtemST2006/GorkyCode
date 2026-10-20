package service

import (
	"encoding/json"
	"gorkycode/backend/internal/generated/ai"
	"gorkycode/backend/internal/model"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestValidateAIResult(t *testing.T) {
	userID, taskID := uuid.New(), uuid.New()
	request := &ai.JobRequest{UserId: userID, TaskId: taskID}
	base := `{"user_id":"` + userID.String() + `","task_id":"` + taskID.String() + `",`
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{
			name: "valid clarification",
			body: base + `"status":"needs_clarification","intent":"create_event","message":"Во сколько?","action":null,"context":{"recent_messages":[],"draft":{"title":"Встреча"}}}`,
		},
		{
			name: "valid create action",
			body: base + `"status":"ready","intent":"create_event","message":null,"action":{"record_type":"meeting","title":"Встреча","date":"2026-10-21","time":"15:00"},"context":{"recent_messages":[],"draft":null}}`,
		},
		{
			name: "valid list action",
			body: base + `"status":"ready","intent":"list_events","message":null,"action":{"date_from":"2026-10-20","date_to_exclusive":"2026-10-21"},"context":{"recent_messages":[],"draft":null}}`,
		},
		{
			name: "valid search action",
			body: base + `"status":"ready","intent":"search_events","message":null,"action":{"query":"Иван"},"context":{"recent_messages":[],"draft":null}}`,
		},
		{
			name: "mismatched ID",
			body: `{"user_id":"` + uuid.NewString() + `","task_id":"` + taskID.String() + `","status":"ready","intent":"search_events"}`,
			want: "does not match",
		},
		{
			name: "missing action",
			body: base + `"status":"ready","intent":"search_events","message":null,"context":{"recent_messages":[],"draft":null}}`,
			want: "invalid AI action",
		},
		{
			name: "invalid list interval",
			body: base + `"status":"ready","intent":"list_events","message":null,"action":{"date_from":"2026-10-21","date_to_exclusive":"2026-10-20"},"context":{"recent_messages":[],"draft":null}}`,
			want: "invalid AI action",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var result model.AiRes
			if err := json.Unmarshal([]byte(tc.body), &result); err != nil {
				t.Fatal(err)
			}
			err := validateAIResult(result, request)
			if tc.want == "" && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
			if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("validation error = %v, want %q", err, tc.want)
			}
		})
	}
}
