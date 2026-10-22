// Package apierr builds RFC 9457 HTTP errors (application/problem+json).
package apierr

import (
	"encoding/json"
	"net/http"

	api "gorkycode/backend/internal/generated"
)

const typeBase = "https://gorkycode.example/problems/"

// InvalidRequest reports an invalid HTTP request.
func InvalidRequest(detail string) api.Problem {
	return newProblem(http.StatusBadRequest, "invalid_request", "invalid-request", "Invalid request", detail)
}

// NotFound reports a missing API resource.
func NotFound(detail string) api.Problem {
	return newProblem(http.StatusNotFound, "not_found", "not-found", "Not found", detail)
}

// MethodNotAllowed reports an unsupported HTTP method for a route.
func MethodNotAllowed(detail string) api.Problem {
	return newProblem(http.StatusMethodNotAllowed, "method_not_allowed", "method-not-allowed", "Method not allowed", detail)
}

// Conflict reports a conflicting request.
func Conflict(detail string) api.Problem {
	return newProblem(http.StatusConflict, "conflict", "conflict", "Conflict", detail)
}

// Unavailable reports a temporarily unavailable dependency.
func Unavailable(detail string) api.Problem {
	return newProblem(http.StatusServiceUnavailable, "service_unavailable", "service-unavailable", "Service unavailable", detail)
}

// Internal reports an unexpected error without exposing its details.
func Internal() api.Problem {
	return newProblem(http.StatusInternalServerError, "internal_error", "internal-error", "Internal server error", "")
}

// TaskInternal reports a task processing error with the neutral detail required by its API contract.
func TaskInternal() api.Problem {
	return newProblem(http.StatusInternalServerError, "internal_error", "internal-error", "Internal server error", "Извините, возникли внутренние проблемы.")
}

// PayloadTooLarge reports an audio upload over the configured limit.
func PayloadTooLarge() api.Problem {
	return newProblem(http.StatusRequestEntityTooLarge, "payload_too_large", "payload-too-large", "Payload too large", "WAV-файл слишком большой.")
}

// BadGateway reports an invalid response from STT or AI-service.
func BadGateway() api.Problem {
	return newProblem(http.StatusBadGateway, "bad_gateway", "bad-gateway", "Bad gateway", "Ошибка обработки команды.")
}

// GatewayTimeout reports a timeout in STT or AI-service.
func GatewayTimeout() api.Problem {
	return newProblem(http.StatusGatewayTimeout, "gateway_timeout", "gateway-timeout", "Gateway timeout", "Сервис обработки не ответил вовремя.")
}

// InvalidCredentials reports a wrong email or password.
func InvalidCredentials(detail string) api.Problem {
	return newProblem(http.StatusUnauthorized, "invalid_credentials", "invalid-credentials", "Invalid credentials", detail)
}

// Unauthorized reports a missing, expired or ended session.
func Unauthorized(detail string) api.Problem {
	return newProblem(http.StatusUnauthorized, "unauthorized", "unauthorized", "Unauthorized", detail)
}

// Forbidden reports that an authenticated user cannot act for another user.
func Forbidden(detail string) api.Problem {
	return newProblem(http.StatusForbidden, "forbidden", "forbidden", "Forbidden", detail)
}

// RequestTimeout reports a request that took too long to complete.
func RequestTimeout(detail string) api.Problem {
	return newProblem(http.StatusRequestTimeout, "request_timeout", "request-timeout", "Request timeout", detail)
}

// Write sends a Problem response and sets instance to the request path.
func Write(w http.ResponseWriter, r *http.Request, p api.Problem) {
	instance := r.URL.Path
	p.Instance = &instance

	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(int(p.Status))
	_ = json.NewEncoder(w).Encode(p)
}

func newProblem(status int32, code, slug, title, detail string) api.Problem {
	p := api.Problem{
		Type:   typeBase + slug,
		Title:  title,
		Status: status,
		Code:   code,
	}
	if detail != "" {
		p.Detail = &detail
	}
	return p
}
