package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
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
	maxServiceBytes      = 128
)

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

var (
	errRequestTransport = errors.New("request transport failed")
	errResponseRead     = errors.New("response read failed")
	errResponseTooLarge = errors.New("response too large")
	errInvalidResponse  = errors.New("invalid server response")
)

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

func (e *APIError) Error() string {
	if e == nil || e.Message == "" {
		return "API request failed"
	}
	return e.Message
}

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
	if !validService(service) {
		return ConfigResponse{}, errors.New("invalid service")
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
		if errors.Is(err, context.DeadlineExceeded) {
			return ConfigResponse{}, context.DeadlineExceeded
		}
		if errors.Is(err, context.Canceled) {
			return ConfigResponse{}, context.Canceled
		}
		return ConfigResponse{}, errRequestTransport
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, int64(maxResponseBodyBytes)+1))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ConfigResponse{}, ctxErr
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return ConfigResponse{}, context.DeadlineExceeded
		}
		if errors.Is(err, context.Canceled) {
			return ConfigResponse{}, context.Canceled
		}
		return ConfigResponse{}, errResponseRead
	}
	if len(body) > maxResponseBodyBytes {
		return ConfigResponse{}, errResponseTooLarge
	}
	if response.StatusCode != http.StatusOK {
		return ConfigResponse{}, decodeAPIError(response.StatusCode, body)
	}

	payload, err := decodeConfigResponse(body)
	if err != nil || payload.Project != project || payload.Environment != environment {
		return ConfigResponse{}, errInvalidResponse
	}
	return payload, nil
}

func decodeConfigResponse(body []byte) (ConfigResponse, error) {
	if err := validateStrictJSON(body); err != nil {
		return ConfigResponse{}, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || len(fields) != 4 {
		return ConfigResponse{}, errors.New("invalid server response")
	}
	for field := range fields {
		switch field {
		case "project", "environment", "revision", "values":
		default:
			return ConfigResponse{}, errors.New("invalid server response")
		}
	}
	var payload struct {
		Project     string            `json:"project"`
		Environment string            `json:"environment"`
		Revision    *int64            `json:"revision"`
		Values      map[string]string `json:"values"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ConfigResponse{}, err
	}
	if payload.Project == "" || payload.Environment == "" || payload.Revision == nil || *payload.Revision < 0 || payload.Values == nil || (*payload.Revision == 0 && len(payload.Values) != 0) {
		return ConfigResponse{}, errors.New("invalid server response")
	}
	for key := range payload.Values {
		if !environmentKeyPattern.MatchString(key) {
			return ConfigResponse{}, errors.New("invalid server response")
		}
	}
	return ConfigResponse{
		Project: payload.Project, Environment: payload.Environment, Revision: *payload.Revision, Values: payload.Values,
	}, nil
}

func validateStrictJSON(body []byte) error {
	if !utf8.Valid(body) || !validJSONSurrogates(body) {
		return errors.New("invalid JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := validateJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func validateJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("invalid JSON object key")
			}
			if _, exists := seen[key]; exists {
				return errors.New("duplicate JSON object key")
			}
			seen[key] = struct{}{}
			if err := validateJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("invalid JSON object")
		}
	case '[':
		for decoder.More() {
			if err := validateJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("invalid JSON array")
		}
	default:
		return errors.New("invalid JSON delimiter")
	}
	return nil
}

func validJSONSurrogates(body []byte) bool {
	inString := false
	for index := 0; index < len(body); index++ {
		switch body[index] {
		case '"':
			inString = !inString
		case '\\':
			if !inString || index+1 >= len(body) {
				continue
			}
			if body[index+1] != 'u' {
				index++
				continue
			}
			value, ok := parseJSONUnicodeEscape(body, index)
			if !ok {
				return false
			}
			switch {
			case value >= 0xd800 && value <= 0xdbff:
				next := index + 6
				low, ok := parseJSONUnicodeEscape(body, next)
				if !ok || low < 0xdc00 || low > 0xdfff {
					return false
				}
				index = next + 5
			case value >= 0xdc00 && value <= 0xdfff:
				return false
			default:
				index += 5
			}
		}
	}
	return true
}

func parseJSONUnicodeEscape(body []byte, index int) (uint16, bool) {
	if index < 0 || index+6 > len(body) || body[index] != '\\' || body[index+1] != 'u' {
		return 0, false
	}
	value, err := strconv.ParseUint(string(body[index+2:index+6]), 16, 16)
	return uint16(value), err == nil
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
	case "http", "https":
		return parsed, nil
	}
	return nil, errors.New("invalid URL")
}

func validURLPort(parsed *url.URL) bool {
	hostname := parsed.Hostname()
	if strings.HasPrefix(parsed.Host, "[") {
		address, err := netip.ParseAddr(hostname)
		if err != nil || !address.Is6() {
			return false
		}
	} else if strings.Contains(hostname, ":") {
		return false
	}
	port := parsed.Port()
	if port == "" {
		return !strings.HasSuffix(parsed.Host, ":")
	}
	number, err := strconv.ParseUint(port, 10, 16)
	return err == nil && number != 0
}

func validToken(token string) bool {
	if token == "" || !utf8.ValidString(token) {
		return false
	}
	return !strings.ContainsFunc(token, func(value rune) bool {
		return unicode.IsSpace(value) || unicode.IsControl(value)
	})
}

func validService(service string) bool {
	return utf8.ValidString(service) && len(service) <= maxServiceBytes
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
	if err := json.Unmarshal(body, &envelope); err != nil {
		return &APIError{Status: status}
	}
	return &APIError{
		Status:    status,
		Code:      envelope.Error.Code,
		Message:   envelope.Error.Message,
		RequestID: envelope.Error.RequestID,
		Fields:    envelope.Error.Fields,
	}
}
