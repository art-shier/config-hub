package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"confighub.local/internal/auth"
	"confighub.local/internal/machineaccess"
)

type MachineAccessService interface {
	CreateIdentity(context.Context, auth.User, machineaccess.CreateIdentity) (machineaccess.Identity, error)
	ListIdentities(context.Context, auth.User) ([]machineaccess.Identity, error)
	GetIdentity(context.Context, auth.User, string) (machineaccess.IdentityDetail, error)
	UpdateIdentity(context.Context, auth.User, string, machineaccess.UpdateIdentityInput) (machineaccess.Identity, error)
	ReplaceGrants(context.Context, auth.User, string, []machineaccess.EnvironmentGrant) error
	IssueToken(context.Context, auth.User, string, machineaccess.IssueToken) (machineaccess.IssuedToken, error)
	RevokeToken(context.Context, auth.User, string, string) error
	ReadCurrentForProject(context.Context, string, string, string, string) (machineaccess.CurrentConfig, error)
}

type machineHandlers struct {
	service MachineAccessService
	auth    *authHandlers
}

type createMachineIdentityRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

type updateMachineIdentityRequest struct {
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

type replaceMachineGrantsRequest struct {
	Grants []machineaccess.EnvironmentGrant `json:"grants"`
}

type issueMachineTokenRequest struct {
	Name      string    `json:"name"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (h *machineHandlers) list(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := h.authenticate(w, r)
	if !ok || !machineQueryAbsent(w, r) {
		return
	}
	identities, err := h.service.ListIdentities(r.Context(), actor)
	if err != nil {
		writeMachineServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"identities": identities})
}

func (h *machineHandlers) create(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := h.authenticateWrite(w, r)
	if !ok || !machineQueryAbsent(w, r) {
		return
	}
	var request createMachineIdentityRequest
	if !decodeMachineJSON(w, r, &request) {
		return
	}
	identity, err := h.service.CreateIdentity(r.Context(), actor, machineaccess.CreateIdentity{
		Name: request.Name, Description: request.Description, Enabled: request.Enabled,
	})
	if err != nil {
		writeMachineServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"identity": identity})
}

func (h *machineHandlers) detail(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := h.authenticate(w, r)
	if !ok || !machineQueryAbsent(w, r) {
		return
	}
	detail, err := h.service.GetIdentity(r.Context(), actor, r.PathValue("identity"))
	if err != nil {
		writeMachineServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"identity": detail})
}

func (h *machineHandlers) update(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := h.authenticateWrite(w, r)
	if !ok || !machineQueryAbsent(w, r) {
		return
	}
	var request updateMachineIdentityRequest
	if !decodeMachineJSON(w, r, &request) {
		return
	}
	identity, err := h.service.UpdateIdentity(r.Context(), actor, r.PathValue("identity"), machineaccess.UpdateIdentityInput{
		Description: request.Description, Enabled: request.Enabled,
	})
	if err != nil {
		writeMachineServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"identity": identity})
}

func (h *machineHandlers) replaceGrants(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := h.authenticateWrite(w, r)
	if !ok || !machineQueryAbsent(w, r) {
		return
	}
	var request replaceMachineGrantsRequest
	if !decodeMachineJSON(w, r, &request) {
		return
	}
	if err := h.service.ReplaceGrants(r.Context(), actor, r.PathValue("identity"), request.Grants); err != nil {
		writeMachineServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *machineHandlers) issueToken(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := h.authenticateWrite(w, r)
	if !ok || !machineQueryAbsent(w, r) {
		return
	}
	var request issueMachineTokenRequest
	if !decodeMachineJSON(w, r, &request) {
		return
	}
	issued, err := h.service.IssueToken(r.Context(), actor, r.PathValue("identity"), machineaccess.IssueToken{Name: request.Name, ExpiresAt: request.ExpiresAt})
	if err != nil {
		writeMachineServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"token": issued})
}

func (h *machineHandlers) revokeToken(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := h.authenticateWrite(w, r)
	if !ok || !machineQueryAbsent(w, r) {
		return
	}
	if err := h.service.RevokeToken(r.Context(), actor, r.PathValue("identity"), r.PathValue("token")); err != nil {
		writeMachineServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *machineHandlers) authenticate(w http.ResponseWriter, r *http.Request) (auth.User, *http.Cookie, bool) {
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

func (h *machineHandlers) authenticateWrite(w http.ResponseWriter, r *http.Request) (auth.User, *http.Cookie, bool) {
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

func decodeMachineJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
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

func machineQueryAbsent(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.RawQuery == "" {
		return true
	}
	writeError(w, r, http.StatusBadRequest, "malformed_query", "Query parameters are invalid")
	return false
}

func writeMachineServiceError(w http.ResponseWriter, r *http.Request, err error) {
	var validation *machineaccess.ValidationError
	switch {
	case errors.As(err, &validation):
		fields := make(map[string]string, len(validation.Fields))
		for field, message := range validation.Fields {
			fields[field] = message
		}
		writeJSON(w, http.StatusUnprocessableEntity, errorEnvelope{Error: apiError{
			Code: "validation_failed", Message: "Request fields are invalid", RequestID: requestIDFromContext(r.Context()), Fields: fields,
		}})
	case errors.Is(err, machineaccess.ErrInvalidToken):
		writeError(w, r, http.StatusUnauthorized, "invalid_token", "Invalid or expired machine token")
	case errors.Is(err, machineaccess.ErrScopeDenied):
		writeError(w, r, http.StatusForbidden, "scope_denied", "Machine token is not authorized for this environment")
	case errors.Is(err, machineaccess.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "forbidden", "Access to this resource is forbidden")
	case errors.Is(err, machineaccess.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "Resource not found")
	case errors.Is(err, machineaccess.ErrConflict):
		writeError(w, r, http.StatusConflict, "resource_conflict", "Resource already exists")
	default:
		writeOperationalError(w, r, err)
	}
}

func machineRouteMethods(path string) (string, bool) {
	if path == "/api/v1/machine-identities" {
		return "GET, POST", true
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/v1/machine-identities/"), "/")
	if len(parts) == 1 && parts[0] != "" {
		return "GET, PUT", true
	}
	if len(parts) == 2 && parts[0] != "" {
		switch parts[1] {
		case "grants":
			return http.MethodPut, true
		case "tokens":
			return http.MethodPost, true
		}
	}
	if len(parts) == 3 && parts[0] != "" && parts[1] == "tokens" && parts[2] != "" {
		return http.MethodDelete, true
	}
	return "", false
}
