package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	defaultHTTPTimeout   = 10 * time.Second
	maxResponseBodyBytes = 8 << 20
)

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type ConfigResponse struct {
	Project     string            `json:"project"`
	Environment string            `json:"environment"`
	Revision    int64             `json:"revision"`
	Values      map[string]string `json:"values"`
}

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

type APIError struct {
	Status    int
	Code      string
	Message   string
	RequestID string
	Fields    map[string]string
}

func (e *APIError) Error() string { return e.Message }

func NewClient(baseURL, token string) (*Client, error) {
	parsed, err := validateBaseURL(baseURL)
	if err != nil {
		return nil, errors.New("invalid server URL")
	}
	if !validToken(token) {
		return nil, errors.New("invalid token")
	}
	return &Client{
		baseURL: strings.TrimRight(parsed.String(), "/"),
		token:   token,
		http: &http.Client{
			Timeout: defaultHTTPTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (c *Client) FetchConfig(ctx context.Context, project, environment, service string) (ConfigResponse, error) {
	if c == nil || c.http == nil || !slugPattern.MatchString(project) || !slugPattern.MatchString(environment) {
		return ConfigResponse{}, errors.New("invalid project or environment")
	}
	endpoint, err := url.Parse(c.baseURL)
	if err != nil {
		return ConfigResponse{}, errors.New("invalid client configuration")
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/api/v1/projects/" + project + "/environments/" + environment + "/config"
	endpoint.RawPath = ""
	query := make(url.Values)
	if service != "" {
		query.Set("service", service)
	}
	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return ConfigResponse{}, errors.New("could not create request")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.token)

	response, err := c.http.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ConfigResponse{}, ctxErr
		}
		return ConfigResponse{}, errors.New("request failed")
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, int64(maxResponseBodyBytes)+1))
	if err != nil {
		return ConfigResponse{}, errors.New("could not read response")
	}
	if len(body) > maxResponseBodyBytes {
		return ConfigResponse{}, errors.New("response is too large")
	}
	if response.StatusCode != http.StatusOK {
		return ConfigResponse{}, decodeAPIError(response.StatusCode, body)
	}

	var payload struct {
		Project     string            `json:"project"`
		Environment string            `json:"environment"`
		Revision    *int64            `json:"revision"`
		Values      map[string]string `json:"values"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ConfigResponse{}, errors.New("invalid server response")
	}
	if payload.Project != project || payload.Environment != environment || payload.Revision == nil || *payload.Revision < 0 || payload.Values == nil {
		return ConfigResponse{}, errors.New("invalid server response")
	}
	return ConfigResponse{
		Project: payload.Project, Environment: payload.Environment, Revision: *payload.Revision, Values: payload.Values,
	}, nil
}

func validateBaseURL(raw string) (*url.URL, error) {
	if raw == "" || strings.TrimSpace(raw) != raw || strings.Contains(raw, "#") {
		return nil, errors.New("invalid URL")
	}
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() || parsed.Opaque != "" || parsed.Host == "" || parsed.Hostname() == "" {
		return nil, errors.New("invalid URL")
	}
	if !validURLPort(parsed) {
		return nil, errors.New("invalid URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.RawFragment != "" || parsed.RawPath != "" {
		return nil, errors.New("invalid URL")
	}
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment == "." || segment == ".." {
			return nil, errors.New("invalid URL")
		}
	}
	switch parsed.Scheme {
	case "https":
		return parsed, nil
	case "http":
		if isLoopbackHost(parsed.Hostname()) {
			return parsed, nil
		}
	}
	return nil, errors.New("invalid URL")
}

func validURLPort(parsed *url.URL) bool {
	if strings.Contains(parsed.Hostname(), ":") && !strings.HasPrefix(parsed.Host, "[") {
		return false
	}
	port := parsed.Port()
	if port == "" {
		return !strings.HasSuffix(parsed.Host, ":")
	}
	number, err := strconv.ParseUint(port, 10, 16)
	return err == nil && number != 0
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validToken(token string) bool {
	if token == "" || !utf8.ValidString(token) {
		return false
	}
	return !strings.ContainsFunc(token, func(value rune) bool {
		return unicode.IsSpace(value) || unicode.IsControl(value)
	})
}

func decodeAPIError(status int, body []byte) error {
	var envelope struct {
		Error struct {
			Code      string            `json:"code"`
			Message   string            `json:"message"`
			RequestID string            `json:"request_id"`
			Fields    map[string]string `json:"fields"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Error.Code == "" || envelope.Error.Message == "" {
		return errors.New("server request failed")
	}
	return &APIError{
		Status:    status,
		Code:      envelope.Error.Code,
		Message:   envelope.Error.Message,
		RequestID: envelope.Error.RequestID,
		Fields:    envelope.Error.Fields,
	}
}
