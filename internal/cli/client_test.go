package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientFetchConfigUsesBearerAuthAndSafeURLJoining(t *testing.T) {
	const token = "token-secret"
	var gotPath, gotQuery, gotAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		gotQuery = r.URL.RawQuery
		gotAuthorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"project":"shop","environment":"production","revision":13,"values":{"PORT":"8080"}}`)
	}))
	defer server.Close()

	client, err := NewClient(server.URL+"/gateway/", token)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.FetchConfig(context.Background(), "shop", "production", "api & worker/+?")
	if err != nil {
		t.Fatal(err)
	}

	if gotAuthorization != "Bearer "+token {
		t.Fatalf("Authorization=%q", gotAuthorization)
	}
	if gotPath != "/gateway/api/v1/projects/shop/environments/production/config" {
		t.Fatalf("path=%q", gotPath)
	}
	if gotQuery != "service=api+%26+worker%2F%2B%3F" {
		t.Fatalf("query=%q", gotQuery)
	}
	if response.Project != "shop" || response.Environment != "production" || response.Revision != 13 || response.Values["PORT"] != "8080" {
		t.Fatalf("response=%+v", response)
	}
}

func TestClientAcceptsEnvironmentWithoutCurrentRevision(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"project":"shop","environment":"development","revision":0,"values":{}}`)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}

	response, err := client.FetchConfig(context.Background(), "shop", "development", "")
	if err != nil {
		t.Fatal(err)
	}
	if response.Revision != 0 || response.Values == nil || len(response.Values) != 0 {
		t.Fatalf("response=%+v", response)
	}
}

func TestClientRejectsResponseWithoutRevisionField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"project":"shop","environment":"development","values":{}}`)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := client.FetchConfig(context.Background(), "shop", "development", ""); err == nil {
		t.Fatal("FetchConfig accepted a response without revision")
	}
}

func TestClientRejectsInvalidSuccessfulResponses(t *testing.T) {
	invalidUTF8 := append([]byte(`{"project":"shop","environment":"production","revision":1,"values":{"KEY":"`), 0xff)
	invalidUTF8 = append(invalidUTF8, []byte(`"}}`)...)
	tests := []struct {
		name string
		body []byte
	}{
		{name: "invalid UTF-8", body: invalidUTF8},
		{name: "duplicate top-level field", body: []byte(`{"project":"other","project":"shop","environment":"production","revision":1,"values":{}}`)},
		{name: "duplicate value key", body: []byte(`{"project":"shop","environment":"production","revision":1,"values":{"KEY":"first","KEY":"second"}}`)},
		{name: "unsafe value key", body: []byte(`{"project":"shop","environment":"production","revision":1,"values":{"BAD-NAME":"value"}}`)},
		{name: "zero revision with values", body: []byte(`{"project":"shop","environment":"production","revision":0,"values":{"KEY":"value"}}`)},
		{name: "unpaired high surrogate", body: []byte(`{"project":"shop","environment":"production","revision":1,"values":{"KEY":"\ud800"}}`)},
		{name: "unpaired low surrogate", body: []byte(`{"project":"shop","environment":"production","revision":1,"values":{"KEY":"\udc00"}}`)},
		{name: "trailing JSON", body: []byte(`{"project":"shop","environment":"production","revision":1,"values":{}} {}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(test.body)
			}))
			defer server.Close()
			client, err := NewClient(server.URL, "token")
			if err != nil {
				t.Fatal(err)
			}

			if _, err := client.FetchConfig(context.Background(), "shop", "production", ""); err == nil {
				t.Fatal("FetchConfig accepted an invalid successful response")
			}
		})
	}
}

func TestClientAcceptsUnicodeAndPairedSurrogateInSuccessfulResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"project":"shop","environment":"production","revision":1,"values":{"UNICODE":"雪-\ud83d\ude03"}}`)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}

	response, err := client.FetchConfig(context.Background(), "shop", "production", "")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := response.Values["UNICODE"], "雪-😃"; got != want {
		t.Fatalf("value=%q want=%q", got, want)
	}
}

func TestClientRejectsUnsafeProjectAndEnvironmentBeforeRequest(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		attempts.Add(1)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		project     string
		environment string
	}{
		{name: "parent traversal", project: "..", environment: "production"},
		{name: "slash", project: "shop/api", environment: "production"},
		{name: "encoded slash", project: "shop%2fapi", environment: "production"},
		{name: "uppercase", project: "Shop", environment: "production"},
		{name: "invalid environment", project: "shop", environment: "../production"},
		{name: "too long", project: strings.Repeat("a", 64), environment: "production"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := client.FetchConfig(context.Background(), test.project, test.environment, ""); err == nil {
				t.Fatal("FetchConfig accepted an unsafe slug")
			}
		})
	}
	if got := attempts.Load(); got != 0 {
		t.Fatalf("unsafe slugs caused %d requests", got)
	}
}

func TestClientValidatesServiceBeforeRequest(t *testing.T) {
	var attempts atomic.Int32
	var gotService string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		gotService = r.URL.Query().Get("service")
		_, _ = io.WriteString(w, `{"project":"shop","environment":"production","revision":1,"values":{}}`)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		service string
	}{
		{name: "invalid UTF-8", service: string([]byte{0xff})},
		{name: "too long", service: strings.Repeat("s", 129)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := client.FetchConfig(context.Background(), "shop", "production", test.service); err == nil {
				t.Fatalf("FetchConfig accepted invalid service of %d bytes", len(test.service))
			}
		})
	}
	if attempts.Load() != 0 {
		t.Fatalf("invalid services caused %d requests", attempts.Load())
	}

	service := strings.Repeat("界", 42) + "ab"
	if len(service) != 128 {
		t.Fatalf("boundary fixture is %d bytes", len(service))
	}
	if _, err := client.FetchConfig(context.Background(), "shop", "production", service); err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 1 || gotService != service {
		t.Fatalf("attempts=%d service=%q", attempts.Load(), gotService)
	}
}

func TestNewClientEnforcesHTTPSWithLoopbackHTTPException(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		wantErr bool
	}{
		{name: "https", baseURL: "https://config.example.com"},
		{name: "https minimum port", baseURL: "https://config.example.com:1"},
		{name: "https default port", baseURL: "https://config.example.com:443"},
		{name: "https maximum port", baseURL: "https://config.example.com:65535"},
		{name: "https ipv4 port", baseURL: "https://192.0.2.1:443"},
		{name: "https bracketed ipv6 without port", baseURL: "https://[2001:db8::1]"},
		{name: "https bracketed ipv6 port", baseURL: "https://[2001:db8::1]:443"},
		{name: "https ipv4-mapped ipv6", baseURL: "https://[::ffff:192.0.2.1]"},
		{name: "https ipv6 zone", baseURL: "https://[fe80::1%25eth0]"},
		{name: "localhost", baseURL: "http://localhost:8080"},
		{name: "ipv4 loopback range", baseURL: "http://127.42.1.9:8080"},
		{name: "ipv6 loopback", baseURL: "http://[::1]:8080"},
		{name: "empty port", baseURL: "https://localhost:", wantErr: true},
		{name: "zero port", baseURL: "https://localhost:0", wantErr: true},
		{name: "port above maximum", baseURL: "https://localhost:65536", wantErr: true},
		{name: "oversized port", baseURL: "https://localhost:999999999999999999999999999999999999", wantErr: true},
		{name: "bracketed ipv6 empty port", baseURL: "https://[::1]:", wantErr: true},
		{name: "bracketed ipv6 port above maximum", baseURL: "https://[::1]:65536", wantErr: true},
		{name: "malformed bracketed literal", baseURL: "https://[not:ipv6]", wantErr: true},
		{name: "malformed bracketed ipv6", baseURL: "https://[::gg]", wantErr: true},
		{name: "bracketed ipv4", baseURL: "https://[192.0.2.1]", wantErr: true},
		{name: "unbracketed ipv6", baseURL: "https://2001:db8::1", wantErr: true},
		{name: "remote http", baseURL: "http://config.example.com", wantErr: true},
		{name: "localhost suffix", baseURL: "http://localhost.evil:8080", wantErr: true},
		{name: "loopback prefix hostname", baseURL: "http://127.0.0.1.example:8080", wantErr: true},
		{name: "relative", baseURL: "/config", wantErr: true},
		{name: "userinfo", baseURL: "https://user:pass@config.example.com", wantErr: true},
		{name: "query", baseURL: "https://config.example.com?target=other", wantErr: true},
		{name: "fragment", baseURL: "https://config.example.com/#other", wantErr: true},
		{name: "empty fragment", baseURL: "https://config.example.com#", wantErr: true},
		{name: "unsupported scheme", baseURL: "ftp://config.example.com", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewClient(test.baseURL, "token")
			if (err != nil) != test.wantErr {
				t.Fatalf("NewClient() error=%v wantErr=%v", err, test.wantErr)
			}
		})
	}
}

func TestNewClientUsesTenSecondTimeout(t *testing.T) {
	client, err := NewClient("https://config.example.com", "token")
	if err != nil {
		t.Fatal(err)
	}
	if client.http.Timeout != 10*time.Second {
		t.Fatalf("Timeout=%s", client.http.Timeout)
	}
}

func TestClientDoesNotRetryUnauthorizedOrForbidden(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				attempts.Add(1)
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":{"code":"denied","message":"access denied","request_id":"req_1","fields":{}}}`)
			}))
			defer server.Close()
			client, err := NewClient(server.URL, "token")
			if err != nil {
				t.Fatal(err)
			}

			_, err = client.FetchConfig(context.Background(), "shop", "production", "")
			if err == nil {
				t.Fatal("FetchConfig succeeded")
			}
			if got := attempts.Load(); got != 1 {
				t.Fatalf("attempts=%d", got)
			}
		})
	}
}

func TestClientDecodesTypedErrorEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"error":{"code":"validation_failed","message":"Request fields are invalid","request_id":"req_42","fields":{"service":"invalid"}}}`)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.FetchConfig(context.Background(), "shop", "production", "bad")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type=%T, want *APIError", err)
	}
	if apiErr.Status != http.StatusUnprocessableEntity || apiErr.Code != "validation_failed" || apiErr.Message != "Request fields are invalid" || apiErr.RequestID != "req_42" || apiErr.Fields["service"] != "invalid" {
		t.Fatalf("APIError=%+v", apiErr)
	}
	if apiErr.Error() != apiErr.Message {
		t.Fatalf("Error()=%q", apiErr.Error())
	}
}

func TestClientErrorsDoNotLeakTokenResponseBodyOrConfig(t *testing.T) {
	const token = "NEVER_LOG_TOKEN"
	client, err := NewClient("https://config.example.com", token)
	if err != nil {
		t.Fatal(err)
	}
	client.http.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("transport echoed %s", r.Header.Get("Authorization"))
	})
	_, err = client.FetchConfig(context.Background(), "shop", "production", "")
	assertErrorOmits(t, err, token, "Bearer "+token)

	const bodySecret = "RAW_RESPONSE_BODY_SECRET"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, bodySecret)
	}))
	defer server.Close()
	client, err = NewClient(server.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.FetchConfig(context.Background(), "shop", "production", "")
	assertErrorOmits(t, err, bodySecret, token)

	const configSecret = "CONFIG_VALUE_SECRET"
	configServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"project":"shop","environment":"production","revision":1,"values":{"SECRET":"`+configSecret+`"}`)
	}))
	defer configServer.Close()
	client, err = NewClient(configServer.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.FetchConfig(context.Background(), "shop", "production", "")
	assertErrorOmits(t, err, configSecret, token)
}

func TestClientClassifiesRuntimeFailuresWithoutLeakingCauses(t *testing.T) {
	tests := []struct {
		name      string
		transport http.RoundTripper
		want      error
		secret    string
	}{
		{
			name:   "transport",
			want:   errRequestTransport,
			secret: "TRANSPORT_CAUSE_SECRET",
			transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("TRANSPORT_CAUSE_SECRET")
			}),
		},
		{
			name: "timeout",
			want: context.DeadlineExceeded,
			transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return nil, context.DeadlineExceeded
			}),
		},
		{
			name:   "response read",
			want:   errResponseRead,
			secret: "RESPONSE_READ_CAUSE_SECRET",
			transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				body := io.NopCloser(readerFunc(func([]byte) (int, error) {
					return 0, errors.New("RESPONSE_READ_CAUSE_SECRET")
				}))
				return &http.Response{StatusCode: http.StatusOK, Body: body, Header: make(http.Header)}, nil
			}),
		},
		{
			name: "response too large",
			want: errResponseTooLarge,
			transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				body := io.NopCloser(strings.NewReader(strings.Repeat("x", maxResponseBodyBytes+1)))
				return &http.Response{StatusCode: http.StatusOK, Body: body, Header: make(http.Header)}, nil
			}),
		},
		{
			name:   "invalid response",
			want:   errInvalidResponse,
			secret: "INVALID_RESPONSE_CONFIG_SECRET",
			transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				body := io.NopCloser(strings.NewReader(`{"project":"shop","environment":"production","revision":1,"values":{"KEY":"INVALID_RESPONSE_CONFIG_SECRET"}`))
				return &http.Response{StatusCode: http.StatusOK, Body: body, Header: make(http.Header)}, nil
			}),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := NewClient("https://config.example.com/private-sensitive-path", "token")
			if err != nil {
				t.Fatal(err)
			}
			client.http.Transport = test.transport
			_, err = client.FetchConfig(context.Background(), "shop", "production", "")
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v want category %v", err, test.want)
			}
			secrets := []string{"private-sensitive-path", "token"}
			if test.secret != "" {
				secrets = append(secrets, test.secret)
			}
			assertErrorOmits(t, err, secrets...)
		})
	}
}

func TestClientPrioritizesContextErrorWhenResponseReadFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client, err := NewClient("https://config.example.com", "token")
	if err != nil {
		t.Fatal(err)
	}
	client.http.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		body := io.NopCloser(readerFunc(func([]byte) (int, error) {
			cancel()
			return 0, errors.New("READ_CAUSE_SECRET")
		}))
		return &http.Response{StatusCode: http.StatusOK, Body: body, Header: make(http.Header)}, nil
	})

	_, err = client.FetchConfig(ctx, "shop", "production", "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v want context canceled", err)
	}
	assertErrorOmits(t, err, "READ_CAUSE_SECRET", "token")
}

func TestClientPreservesAPIStatusForMalformedErrorBody(t *testing.T) {
	const bodySecret = "MALFORMED_API_BODY_SECRET"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, bodySecret)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.FetchConfig(context.Background(), "shop", "production", "")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadGateway {
		t.Fatalf("error=%T %v", err, err)
	}
	assertErrorOmits(t, err, bodySecret, "token")
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetRequests.Add(1)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusFound)
		_, _ = io.WriteString(w, `{"error":{"code":"redirect","message":"redirect refused","request_id":"req_redirect","fields":{}}}`)
	}))
	defer source.Close()
	client, err := NewClient(source.URL, "token")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := client.FetchConfig(context.Background(), "shop", "production", ""); err == nil {
		t.Fatal("FetchConfig succeeded on redirect")
	}
	if got := targetRequests.Load(); got != 0 {
		t.Fatalf("followed redirect %d times", got)
	}
}

func TestClientLimitsResponseBodies(t *testing.T) {
	const responseSecret = "OVERSIZED_RESPONSE_SECRET"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", maxResponseBodyBytes)+responseSecret)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.FetchConfig(context.Background(), "shop", "production", "")
	assertErrorOmits(t, err, responseSecret)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(value []byte) (int, error) { return f(value) }

func assertErrorOmits(t *testing.T, err error, secrets ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, secret := range secrets {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaked %q: %v", secret, err)
		}
	}
}
