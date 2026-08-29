package httpapi

import (
	"context"
	"errors"
	"net/http"

	"confighub.local/internal/administration"
	"confighub.local/internal/auth"
)

type AdministrationService interface {
	ListUsers(context.Context, auth.User) (administration.UserRegister, error)
	System(context.Context, auth.User) (administration.SystemStatus, error)
}

type administrationHandlers struct {
	service AdministrationService
	auth    *authHandlers
}

func (h *administrationHandlers) users(w http.ResponseWriter, r *http.Request) {
	if !administrationMethodAllowed(w, r) {
		return
	}
	actor, ok := h.authenticate(w, r)
	if !ok || !administrationQueryAbsent(w, r) {
		return
	}
	register, err := h.service.ListUsers(r.Context(), actor)
	if err != nil {
		writeAdministrationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, register)
}

func (h *administrationHandlers) system(w http.ResponseWriter, r *http.Request) {
	if !administrationMethodAllowed(w, r) {
		return
	}
	actor, ok := h.authenticate(w, r)
	if !ok || !administrationQueryAbsent(w, r) {
		return
	}
	status, err := h.service.System(r.Context(), actor)
	if err != nil {
		writeAdministrationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (h *administrationHandlers) authenticate(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	cookie, ok := h.auth.sessionCookie(w, r)
	if !ok {
		return auth.User{}, false
	}
	actor, err := h.auth.sessions.Authenticate(r.Context(), cookie.Value)
	if err != nil {
		writeSessionAuthenticationError(w, r, err)
		return auth.User{}, false
	}
	return actor, true
}

func administrationMethodAllowed(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet {
		return true
	}
	w.Header().Set("Allow", http.MethodGet)
	writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed")
	return false
}

func administrationQueryAbsent(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.RawQuery == "" {
		return true
	}
	writeError(w, r, http.StatusBadRequest, "malformed_query", "Query parameters are invalid")
	return false
}

func writeAdministrationError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, administration.ErrForbidden) {
		writeError(w, r, http.StatusForbidden, "forbidden", "Access to this resource is forbidden")
		return
	}
	writeOperationalError(w, r, err)
}
