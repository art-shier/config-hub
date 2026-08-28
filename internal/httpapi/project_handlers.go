package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"confighub.local/internal/auth"
	"confighub.local/internal/projects"
)

type ProjectService interface {
	ListVisible(context.Context, auth.User) ([]projects.Project, error)
	CreateProject(context.Context, auth.User, projects.CreateProject) (projects.Project, error)
	GetProject(context.Context, auth.User, string) (projects.ProjectDetail, error)
	CreateEnvironment(context.Context, auth.User, string, projects.CreateEnvironment) (projects.Environment, error)
	ListMembers(context.Context, auth.User, string) ([]projects.MemberGrant, error)
	SetMember(context.Context, auth.User, string, string, string) error
	RemoveMember(context.Context, auth.User, string, string) error
}

type projectHandlers struct {
	service ProjectService
	auth    *authHandlers
}

type createProjectRequest struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type createEnvironmentRequest struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type setMemberRequest struct {
	Permission string `json:"permission"`
}

func (h *projectHandlers) list(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	result, err := h.service.ListVisible(r.Context(), actor)
	if err != nil {
		writeProjectServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": result})
}

func (h *projectHandlers) create(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := h.authenticateWrite(w, r)
	if !ok {
		return
	}
	var input createProjectRequest
	if !decodeProjectJSON(w, r, &input) {
		return
	}
	project, err := h.service.CreateProject(r.Context(), actor, projects.CreateProject{
		Slug: input.Slug, Name: input.Name, Description: input.Description,
	})
	if err != nil {
		writeProjectServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"project": project})
}

func (h *projectHandlers) detail(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	detail, err := h.service.GetProject(r.Context(), actor, r.PathValue("project"))
	if err != nil {
		writeProjectServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": detail})
}

func (h *projectHandlers) createEnvironment(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := h.authenticateWrite(w, r)
	if !ok {
		return
	}
	var input createEnvironmentRequest
	if !decodeProjectJSON(w, r, &input) {
		return
	}
	environment, err := h.service.CreateEnvironment(r.Context(), actor, r.PathValue("project"), projects.CreateEnvironment{Slug: input.Slug, Name: input.Name})
	if err != nil {
		writeProjectServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"environment": environment})
}

func (h *projectHandlers) listMembers(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	members, err := h.service.ListMembers(r.Context(), actor, r.PathValue("project"))
	if err != nil {
		writeProjectServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

func (h *projectHandlers) setMember(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := h.authenticateWrite(w, r)
	if !ok {
		return
	}
	var input setMemberRequest
	if !decodeProjectJSON(w, r, &input) {
		return
	}
	err := h.service.SetMember(r.Context(), actor, r.PathValue("project"), r.PathValue("username"), input.Permission)
	if err != nil {
		writeProjectServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *projectHandlers) removeMember(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := h.authenticateWrite(w, r)
	if !ok {
		return
	}
	err := h.service.RemoveMember(r.Context(), actor, r.PathValue("project"), r.PathValue("username"))
	if err != nil {
		writeProjectServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *projectHandlers) authenticate(w http.ResponseWriter, r *http.Request) (auth.User, *http.Cookie, bool) {
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

func (h *projectHandlers) authenticateWrite(w http.ResponseWriter, r *http.Request) (auth.User, *http.Cookie, bool) {
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

func decodeProjectJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
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

func writeProjectServiceError(w http.ResponseWriter, r *http.Request, err error) {
	var validation *projects.ValidationError
	switch {
	case errors.As(err, &validation):
		fields := make(map[string]string, len(validation.Fields))
		for field, message := range validation.Fields {
			fields[field] = message
		}
		writeJSON(w, http.StatusUnprocessableEntity, errorEnvelope{Error: apiError{
			Code: "validation_failed", Message: "Request fields are invalid", RequestID: requestIDFromContext(r.Context()), Fields: fields,
		}})
	case errors.Is(err, projects.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "forbidden", "Access to this resource is forbidden")
	case errors.Is(err, projects.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "Resource not found")
	case errors.Is(err, projects.ErrConflict):
		writeError(w, r, http.StatusConflict, "resource_conflict", "Resource already exists")
	default:
		writeOperationalError(w, r, err)
	}
}

func projectRouteMethods(path string) (string, bool) {
	if path == "/api/v1/projects" {
		return "GET, POST", true
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/v1/projects/"), "/")
	if len(parts) == 1 && parts[0] != "" {
		return http.MethodGet, true
	}
	if len(parts) == 2 && parts[0] != "" {
		switch parts[1] {
		case "environments":
			return http.MethodPost, true
		case "members":
			return http.MethodGet, true
		}
	}
	if len(parts) == 3 && parts[0] != "" && parts[1] == "members" && parts[2] != "" {
		return "DELETE, PUT", true
	}
	return "", false
}
