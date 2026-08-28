package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"confighub.local/internal/auth"
	"confighub.local/internal/revisions"
)

var revisionVersionPattern = regexp.MustCompile(`^[1-9][0-9]*$`)

type RevisionService interface {
	CurrentForProject(context.Context, auth.User, string, string, string) (revisions.Revision, error)
	ReplaceForProject(context.Context, auth.User, string, string, revisions.ReplaceInput) (revisions.Revision, error)
	ListForProject(context.Context, auth.User, string, string) ([]revisions.RevisionSummary, error)
	GetForProject(context.Context, auth.User, string, string, int64) (revisions.Revision, error)
	DiffResultForProject(context.Context, auth.User, string, string, int64) (revisions.DiffResult, error)
	RollbackForProject(context.Context, auth.User, string, string, int64, string) (revisions.Revision, error)
}

type revisionHandlers struct {
	service RevisionService
	auth    *authHandlers
}

type replaceRevisionRequest struct {
	BaseRevision int64             `json:"base_revision"`
	Message      string            `json:"message"`
	Entries      []revisions.Entry `json:"entries"`
}

type rollbackRevisionRequest struct {
	Message string `json:"message"`
}

func (h *revisionHandlers) current(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	service, ok := revisionServiceQuery(w, r)
	if !ok {
		return
	}
	revision, err := h.service.CurrentForProject(r.Context(), actor, r.PathValue("project"), r.PathValue("environment"), service)
	if err != nil {
		writeRevisionServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revision": revision})
}

func (h *revisionHandlers) replace(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := h.authenticateWrite(w, r)
	if !ok {
		return
	}
	if !revisionQueryAbsent(w, r) {
		return
	}
	var request replaceRevisionRequest
	if !decodeRevisionJSON(w, r, &request) {
		return
	}
	revision, err := h.service.ReplaceForProject(r.Context(), actor, r.PathValue("project"), r.PathValue("environment"), revisions.ReplaceInput{
		BaseRevision: request.BaseRevision, Message: request.Message, Entries: request.Entries,
	})
	if err != nil {
		writeRevisionServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"revision": revision})
}

func (h *revisionHandlers) list(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	if !revisionQueryAbsent(w, r) {
		return
	}
	result, err := h.service.ListForProject(r.Context(), actor, r.PathValue("project"), r.PathValue("environment"))
	if err != nil {
		writeRevisionServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revisions": result})
}

func (h *revisionHandlers) detail(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	if !revisionQueryAbsent(w, r) {
		return
	}
	version, ok := revisionVersion(w, r)
	if !ok {
		return
	}
	revision, err := h.service.GetForProject(r.Context(), actor, r.PathValue("project"), r.PathValue("environment"), version)
	if err != nil {
		writeRevisionServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revision": revision})
}

func (h *revisionHandlers) diff(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	if !revisionQueryAbsent(w, r) {
		return
	}
	version, ok := revisionVersion(w, r)
	if !ok {
		return
	}
	result, err := h.service.DiffResultForProject(r.Context(), actor, r.PathValue("project"), r.PathValue("environment"), version)
	if err != nil {
		writeRevisionServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *revisionHandlers) rollback(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := h.authenticateWrite(w, r)
	if !ok {
		return
	}
	if !revisionQueryAbsent(w, r) {
		return
	}
	version, ok := revisionVersion(w, r)
	if !ok {
		return
	}
	var request rollbackRevisionRequest
	if !decodeRevisionJSON(w, r, &request) {
		return
	}
	revision, err := h.service.RollbackForProject(r.Context(), actor, r.PathValue("project"), r.PathValue("environment"), version, request.Message)
	if err != nil {
		writeRevisionServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"revision": revision})
}

func (h *revisionHandlers) authenticate(w http.ResponseWriter, r *http.Request) (auth.User, *http.Cookie, bool) {
	cookie, ok := h.auth.sessionCookie(w, r)
	if !ok {
		return auth.User{}, nil, false
	}
	actor, err := h.auth.sessions.Authenticate(r.Context(), cookie.Value)
	if err != nil {
		writeSessionAuthenticationError(w, r, err)
		return auth.User{}, nil, false
	}
	return actor, cookie, true
}

func (h *revisionHandlers) authenticateWrite(w http.ResponseWriter, r *http.Request) (auth.User, *http.Cookie, bool) {
	actor, cookie, ok := h.authenticate(w, r)
	if !ok {
		return auth.User{}, nil, false
	}
	if !h.auth.validRequestOrigin(r) {
		writeError(w, r, http.StatusForbidden, "invalid_origin", "Request origin is not allowed")
		return auth.User{}, nil, false
	}
	if !h.auth.sessions.ValidateCSRF(cookie.Value, r.Header.Get(CSRFHeaderName)) {
		writeError(w, r, http.StatusForbidden, "invalid_csrf", "CSRF validation failed")
		return auth.User{}, nil, false
	}
	return actor, cookie, true
}

func revisionServiceQuery(w http.ResponseWriter, r *http.Request) (string, bool) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "malformed_query", "Query parameters are invalid")
		return "", false
	}
	for key := range query {
		if key != "service" {
			writeError(w, r, http.StatusBadRequest, "malformed_query", "Query parameters are invalid")
			return "", false
		}
	}
	values, exists := query["service"]
	if !exists {
		return "", true
	}
	if len(values) != 1 {
		writeError(w, r, http.StatusBadRequest, "malformed_query", "Query parameters are invalid")
		return "", false
	}
	service := strings.TrimSpace(values[0])
	if service == "" {
		writeRevisionServiceError(w, r, &revisions.ValidationError{Fields: map[string]string{"service": "must not be empty when provided"}})
		return "", false
	}
	return service, true
}

func revisionQueryAbsent(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.RawQuery == "" {
		return true
	}
	writeError(w, r, http.StatusBadRequest, "malformed_query", "Query parameters are invalid")
	return false
}

func revisionVersion(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.PathValue("version")
	if !revisionVersionPattern.MatchString(raw) {
		writeError(w, r, http.StatusBadRequest, "malformed_request", "Revision version is invalid")
		return 0, false
	}
	version, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "malformed_request", "Revision version is invalid")
		return 0, false
	}
	return version, true
}

func decodeRevisionJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	if err := decodeJSON(w, r, destination); err != nil {
		if isBodyTooLarge(err) {
			writeError(w, r, http.StatusRequestEntityTooLarge, "request_too_large", "Request body is too large")
		} else {
			writeError(w, r, http.StatusBadRequest, "malformed_request", "Malformed JSON request")
		}
		return false
	}
	return true
}

func writeRevisionServiceError(w http.ResponseWriter, r *http.Request, err error) {
	var validation *revisions.ValidationError
	switch {
	case errors.As(err, &validation):
		fields := make(map[string]string, len(validation.Fields))
		for field, message := range validation.Fields {
			fields[field] = message
		}
		writeJSON(w, http.StatusUnprocessableEntity, errorEnvelope{Error: apiError{
			Code: "validation_failed", Message: "Request fields are invalid", RequestID: requestIDFromContext(r.Context()), Fields: fields,
		}})
	case errors.Is(err, revisions.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "forbidden", "Access to this resource is forbidden")
	case errors.Is(err, revisions.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "Resource not found")
	case errors.Is(err, revisions.ErrRevisionConflict):
		writeError(w, r, http.StatusConflict, "revision_conflict", "Configuration changed since it was loaded")
	default:
		writeOperationalError(w, r, err)
	}
}

func revisionRouteMethods(path string) (string, bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/api/v1/projects/"), "/")
	if len(parts) < 4 || parts[0] == "" || parts[1] != "environments" || parts[2] == "" {
		return "", false
	}
	switch {
	case len(parts) == 4 && parts[3] == "config":
		return "GET, PUT", true
	case len(parts) == 4 && parts[3] == "revisions":
		return http.MethodGet, true
	case len(parts) == 5 && parts[3] == "revisions" && parts[4] != "":
		return http.MethodGet, true
	case len(parts) == 6 && parts[3] == "revisions" && parts[4] != "" && parts[5] == "diff":
		return http.MethodGet, true
	case len(parts) == 6 && parts[3] == "revisions" && parts[4] != "" && parts[5] == "rollback":
		return http.MethodPost, true
	default:
		return "", false
	}
}
