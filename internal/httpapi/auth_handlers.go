package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"time"

	"confighub.local/internal/auth"
)

type authHandlers struct {
	credentials  CredentialAuthenticator
	sessions     *auth.SessionManager
	publicOrigin *url.URL
	sourceIP     func(*http.Request) string
	limiter      *loginLimiter
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type userResponse struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

type sessionResponse struct {
	User      userResponse `json:"user"`
	CSRFToken string       `json:"csrf_token"`
	ExpiresAt time.Time    `json:"expires_at"`
}

func (h *authHandlers) login(w http.ResponseWriter, r *http.Request) {
	if !h.validRequestOrigin(r) {
		writeError(w, r, http.StatusForbidden, "invalid_origin", "Request origin is not allowed")
		return
	}
	var input loginRequest
	if err := decodeJSON(w, r, &input); err != nil {
		if isBodyTooLarge(err) {
			writeError(w, r, http.StatusRequestEntityTooLarge, "request_too_large", "Request body is too large")
		} else {
			writeError(w, r, http.StatusBadRequest, "malformed_request", "Malformed JSON request")
		}
		return
	}
	if !h.limiter.Allow(h.sourceIP(r), input.Username) {
		writeError(w, r, http.StatusTooManyRequests, "rate_limited", "Too many login attempts")
		return
	}
	credential, err := h.credentials.Verify(r.Context(), input.Username, input.Password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			writeError(w, r, http.StatusUnauthorized, "invalid_credentials", "Invalid username or password")
		} else {
			writeOperationalError(w, r, err)
		}
		return
	}
	issued, err := h.sessions.CreateVerified(r.Context(), credential)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			writeError(w, r, http.StatusUnauthorized, "invalid_credentials", "Invalid username or password")
		} else {
			writeOperationalError(w, r, err)
		}
		return
	}
	user := credential.User
	h.setSessionCookie(w, issued.CookieValue, issued.ExpiresAt)
	writeJSON(w, http.StatusOK, sessionResponse{User: publicUser(user), CSRFToken: issued.CSRFToken, ExpiresAt: issued.ExpiresAt})
}

func (h *authHandlers) logout(w http.ResponseWriter, r *http.Request) {
	cookie, ok := h.sessionCookie(w, r)
	if !ok {
		return
	}
	if _, err := h.sessions.Authenticate(r.Context(), cookie.Value); err != nil {
		writeSessionAuthenticationError(w, r, err)
		return
	}
	if !h.validRequestOrigin(r) {
		writeError(w, r, http.StatusForbidden, "invalid_origin", "Request origin is not allowed")
		return
	}
	if !h.sessions.ValidateCSRF(cookie.Value, r.Header.Get(CSRFHeaderName)) {
		writeError(w, r, http.StatusForbidden, "invalid_csrf", "CSRF validation failed")
		return
	}
	if err := h.sessions.Revoke(r.Context(), cookie.Value); err != nil {
		writeOperationalError(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: SessionCookieName, Value: "", Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, Expires: time.Unix(1, 0).UTC(), MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (h *authHandlers) session(w http.ResponseWriter, r *http.Request) {
	cookie, ok := h.sessionCookie(w, r)
	if !ok {
		return
	}
	user, expires, err := h.sessions.AuthenticateWithExpiry(r.Context(), cookie.Value)
	if err != nil {
		writeSessionAuthenticationError(w, r, err)
		return
	}
	csrf, ok := h.sessions.CSRFToken(cookie.Value)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "invalid_session", "Invalid or expired session")
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse{User: publicUser(user), CSRFToken: csrf, ExpiresAt: expires})
}

func writeSessionAuthenticationError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, auth.ErrInvalidSession) {
		writeError(w, r, http.StatusUnauthorized, "invalid_session", "Invalid or expired session")
		return
	}
	writeOperationalError(w, r, err)
}

func (h *authHandlers) sessionCookie(w http.ResponseWriter, r *http.Request) (*http.Cookie, bool) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		writeError(w, r, http.StatusUnauthorized, "invalid_session", "Invalid or expired session")
		return nil, false
	}
	return cookie, true
}

func (h *authHandlers) validRequestOrigin(r *http.Request) bool {
	values := r.Header.Values("Origin")
	if len(values) != 1 {
		return false
	}
	value := values[0]
	if value == "" || value == "null" {
		return false
	}
	origin, err := url.Parse(value)
	return err == nil && origin.Scheme == h.publicOrigin.Scheme && origin.Host == h.publicOrigin.Host && origin.User == nil && origin.Path == "" && origin.RawQuery == "" && origin.Fragment == ""
}

func (h *authHandlers) setSessionCookie(w http.ResponseWriter, value string, expires time.Time) {
	maxAge := int(time.Until(expires).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(w, &http.Cookie{Name: SessionCookieName, Value: value, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, Expires: expires, MaxAge: maxAge})
}

func publicUser(user auth.User) userResponse {
	return userResponse{ID: user.ID, Username: user.Username, DisplayName: user.DisplayName, Role: user.Role}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errors.New("invalid content type")
	}
	body, err := readStrictJSONBody(r.Body, requestBodyLimit(r.Pattern))
	if err != nil {
		return err
	}
	if err := validateStrictJSON(body); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func isBodyTooLarge(err error) bool {
	var tooLarge *http.MaxBytesError
	return errors.As(err, &tooLarge)
}
