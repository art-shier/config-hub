package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"confighub.local/internal/database"
)

type errorEnvelope struct {
	Error apiError `json:"error"`
}

func writeOperationalError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, database.ErrBusy) {
		writeError(w, r, http.StatusServiceUnavailable, "service_unavailable", "Service temporarily unavailable")
		return
	}
	writeError(w, r, http.StatusInternalServerError, "internal_error", "Internal server error")
}

type apiError struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	RequestID string            `json:"request_id"`
	Fields    map[string]string `json:"fields"`
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: apiError{
		Code: code, Message: message, RequestID: requestIDFromContext(r.Context()), Fields: map[string]string{},
	}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
