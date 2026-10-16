package apierr

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	api "gorkycode/backend/internal/generated"
)

func TestWriteProblemDetails(t *testing.T) {
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/items?id=1", nil)
	response := httptest.NewRecorder()
	Write(response, request, InvalidRequest("invalid id"))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if got := response.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("content type = %q", got)
	}

	var problem api.Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "invalid_request" || problem.Status != http.StatusBadRequest {
		t.Fatalf("unexpected problem: %+v", problem)
	}
	if problem.Instance == nil || *problem.Instance != "/api/v1/items" {
		t.Fatalf("unexpected instance: %+v", problem.Instance)
	}
	if problem.Detail == nil || *problem.Detail != "invalid id" {
		t.Fatalf("unexpected detail: %+v", problem.Detail)
	}
}

func TestInternalDoesNotExposeDetail(t *testing.T) {
	problem := Internal()
	if problem.Status != http.StatusInternalServerError || problem.Detail != nil {
		t.Fatalf("unexpected internal problem: %+v", problem)
	}
}
