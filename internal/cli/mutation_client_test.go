package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestClientMutateConfigSendsStrictPatchRequest(t *testing.T) {
	const token = "MUTATION_TOKEN_SENTINEL"
	const value = "MUTATION_VALUE_SENTINEL"
	var method, path, accept, contentType, authorization string
	var exactBody, nilService, emptyService bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		path = r.URL.EscapedPath()
		accept = r.Header.Get("Accept")
		contentType = r.Header.Get("Content-Type")
		authorization = r.Header.Get("Authorization")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error("request body could not be read")
			return
		}
		exactBody = bytes.Equal(body, []byte(`{"base_revision":7,"message":"change","operation":{"type":"set","key":"FEATURE","value":"MUTATION_VALUE_SENTINEL"}}`))
		var payload struct {
			Operation struct {
				Value   *string         `json:"value"`
				Service json.RawMessage `json:"service"`
			} `json:"operation"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Error("request body was not JSON")
			return
		}
		nilService = payload.Operation.Value != nil && *payload.Operation.Value == value && payload.Operation.Service == nil
		if payload.Operation.Service == nil {
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"project":"shop","environment":"production","revision":8,"created":true}`)
			return
		}
		emptyService = bytes.Equal(payload.Operation.Service, []byte(`""`))
		_, _ = io.WriteString(w, `{"project":"shop","environment":"production","revision":7,"created":false}`)
	}))
	defer server.Close()

	client, err := NewClient(server.URL+"/gateway/", token)
	if err != nil {
		t.Fatal("mutation client could not be created")
	}
	response, err := client.MutateConfig(context.Background(), "shop", "production", MutationRequest{
		BaseRevision: 7,
		Message:      "change",
		Operation:    MutationOperation{Type: "set", Key: "FEATURE", Value: mutationStringPointer(value)},
	})
	if err != nil {
		t.Fatal("strict mutation request failed")
	}
	if method != http.MethodPatch || path != "/gateway/api/v1/projects/shop/environments/production/config" || accept != "application/json" || contentType != "application/json" || authorization != "Bearer "+token || !exactBody || !nilService || response.Project != "shop" || response.Environment != "production" || response.Revision != 8 || !response.Created {
		t.Fatal("mutation request or response did not match the strict contract")
	}

	_, err = client.MutateConfig(context.Background(), "shop", "production", MutationRequest{
		BaseRevision: 7,
		Operation:    MutationOperation{Type: "set", Key: "FEATURE", Value: mutationStringPointer(value), Service: mutationStringPointer("")},
	})
	if err != nil {
		t.Fatal("explicit global mutation request failed")
	}
	if !emptyService {
		t.Fatal("explicit empty service was not encoded")
	}
}

func TestClientMutateConfigAcceptsOnlyCoherentSuccessfulResponses(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       []byte
		wantErr    bool
		wantRev    int64
		wantCreate bool
	}{
		{name: "no operation", status: http.StatusOK, body: []byte(`{"project":"shop","environment":"production","revision":7,"created":false}`), wantRev: 7},
		{name: "created", status: http.StatusCreated, body: []byte(`{"project":"shop","environment":"production","revision":8,"created":true}`), wantRev: 8, wantCreate: true},
		{name: "extra field", status: http.StatusOK, body: []byte(`{"project":"shop","environment":"production","revision":7,"created":false,"extra":true}`), wantErr: true},
		{name: "duplicate field", status: http.StatusOK, body: []byte(`{"project":"shop","project":"shop","environment":"production","revision":7,"created":false}`), wantErr: true},
		{name: "missing field", status: http.StatusOK, body: []byte(`{"project":"shop","environment":"production","revision":7}`), wantErr: true},
		{name: "wrong project", status: http.StatusOK, body: []byte(`{"project":"other","environment":"production","revision":7,"created":false}`), wantErr: true},
		{name: "wrong environment", status: http.StatusOK, body: []byte(`{"project":"shop","environment":"other","revision":7,"created":false}`), wantErr: true},
		{name: "created status without created", status: http.StatusCreated, body: []byte(`{"project":"shop","environment":"production","revision":8,"created":false}`), wantErr: true},
		{name: "created revision jump", status: http.StatusCreated, body: []byte(`{"project":"shop","environment":"production","revision":9,"created":true}`), wantErr: true},
		{name: "no operation with created", status: http.StatusOK, body: []byte(`{"project":"shop","environment":"production","revision":7,"created":true}`), wantErr: true},
		{name: "no operation revision jump", status: http.StatusOK, body: []byte(`{"project":"shop","environment":"production","revision":8,"created":false}`), wantErr: true},
		{name: "malformed unicode", status: http.StatusOK, body: []byte(`{"project":"\ud800","environment":"production","revision":7,"created":false}`), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write(test.body)
			}))
			defer server.Close()
			client := newMutationClient(t, server.URL, "token")
			response, err := client.MutateConfig(context.Background(), "shop", "production", validMutationRequestFixture())
			if (err != nil) != test.wantErr {
				t.Fatal("successful response acceptance did not match expectation")
			}
			if !test.wantErr && (response.Revision != test.wantRev || response.Created != test.wantCreate) {
				t.Fatal("successful response was decoded incorrectly")
			}
		})
	}
}

func TestClientMutateConfigClassifiesRuntimeFailuresWithoutSecrets(t *testing.T) {
	const token = "MUTATION_TOKEN_SENTINEL"
	const value = "MUTATION_VALUE_SENTINEL"
	tests := []struct {
		name      string
		transport http.RoundTripper
		want      error
	}{
		{name: "response read", want: errResponseRead, transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(readerFunc(func([]byte) (int, error) { return 0, errors.New("READ_FAILURE_SENTINEL") }))}, nil
		})},
		{name: "oversized", want: errResponseTooLarge, transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat("x", maxResponseBodyBytes+1)))}, nil
		})},
		{name: "transport", want: errRequestTransport, transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("TRANSPORT_FAILURE_SENTINEL")
		})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newMutationClient(t, "https://config.example.com/private", token)
			client.http.Transport = test.transport
			_, err := client.MutateConfig(context.Background(), "shop", "production", MutationRequest{BaseRevision: 7, Operation: MutationOperation{Type: "set", Key: "FEATURE", Value: mutationStringPointer(value)}})
			if !errors.Is(err, test.want) {
				t.Fatal("runtime failure was not classified")
			}
			assertMutationErrorOmits(t, err, token, value, "READ_FAILURE_SENTINEL", "TRANSPORT_FAILURE_SENTINEL")
		})
	}
}

func TestClientMutateConfigPrioritizesCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := newMutationClient(t, "https://config.example.com", "MUTATION_TOKEN_SENTINEL")
	client.http.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(readerFunc(func([]byte) (int, error) {
			cancel()
			return 0, errors.New("READ_FAILURE_SENTINEL")
		}))}, nil
	})
	_, err := client.MutateConfig(ctx, "shop", "production", validMutationRequestFixture())
	if !errors.Is(err, context.Canceled) {
		t.Fatal("canceled context did not take precedence")
	}
	assertMutationErrorOmits(t, err, "MUTATION_TOKEN_SENTINEL", "READ_FAILURE_SENTINEL")
}

func TestClientMutateConfigDoesNotFollowRedirectsAndDecodesAPIErrors(t *testing.T) {
	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetRequests.Add(1) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusFound)
		_, _ = io.WriteString(w, `{"error":{"code":"redirect","message":"redirect refused","request_id":"req_redirect","fields":{}}}`)
	}))
	defer source.Close()
	client := newMutationClient(t, source.URL, "token")
	_, err := client.MutateConfig(context.Background(), "shop", "production", validMutationRequestFixture())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusFound || apiErr.Code != "redirect" || targetRequests.Load() != 0 {
		t.Fatal("redirect handling did not preserve the API error without following it")
	}

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":{"code":"revision_conflict","message":"conflict","request_id":"req_42","fields":{}}}`)
	}))
	defer apiServer.Close()
	client = newMutationClient(t, apiServer.URL, "MUTATION_TOKEN_SENTINEL")
	_, err = client.MutateConfig(context.Background(), "shop", "production", MutationRequest{BaseRevision: 7, Operation: MutationOperation{Type: "set", Key: "FEATURE", Value: mutationStringPointer("MUTATION_VALUE_SENTINEL")}})
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict || apiErr.Code != "revision_conflict" || apiErr.RequestID != "req_42" {
		t.Fatal("typed API error was not decoded")
	}
	assertMutationErrorOmits(t, err, "MUTATION_TOKEN_SENTINEL", "MUTATION_VALUE_SENTINEL")
}

func TestClientMutateConfigScrubsEchoedAPIErrorData(t *testing.T) {
	const token = "MUTATION_TOKEN_SENTINEL"
	const value = "MUTATION_VALUE_SENTINEL"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":{"code":"revision_conflict","message":"MUTATION_TOKEN_SENTINEL MUTATION_VALUE_SENTINEL","request_id":"req_42","fields":{"operation.value":"MUTATION_VALUE_SENTINEL","authorization":"MUTATION_TOKEN_SENTINEL"}}}`)
	}))
	defer server.Close()
	client := newMutationClient(t, server.URL, token)
	_, err := client.MutateConfig(context.Background(), "shop", "production", MutationRequest{
		BaseRevision: 7,
		Operation:    MutationOperation{Type: "set", Key: "FEATURE", Value: mutationStringPointer(value)},
	})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict || apiErr.Code != "revision_conflict" || apiErr.RequestID != "req_42" || apiErr.Message != "" || len(apiErr.Fields) != 0 {
		t.Fatal("echoed API error was not safely reduced")
	}
	assertMutationErrorOmits(t, err, token, value)
}

func TestClientMutateConfigRejectsInvalidInputBeforeRequest(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { attempts.Add(1) }))
	defer server.Close()
	client := newMutationClient(t, server.URL, "token")
	value := "value"
	tests := []struct {
		client      *Client
		project     string
		environment string
		request     MutationRequest
	}{
		{client: nil, project: "shop", environment: "production", request: validMutationRequestFixture()},
		{client: &Client{}, project: "shop", environment: "production", request: validMutationRequestFixture()},
		{client: client, project: "shop/api", environment: "production", request: validMutationRequestFixture()},
		{client: client, project: "shop", environment: "../production", request: validMutationRequestFixture()},
		{client: client, project: "shop", environment: "production", request: MutationRequest{BaseRevision: -1, Operation: MutationOperation{Type: "set", Key: "FEATURE", Value: &value}}},
		{client: client, project: "shop", environment: "production", request: MutationRequest{BaseRevision: math.MaxInt64, Operation: MutationOperation{Type: "set", Key: "FEATURE", Value: &value}}},
		{client: client, project: "shop", environment: "production", request: MutationRequest{BaseRevision: 7, Operation: MutationOperation{Type: "set", Key: "bad-key", Value: &value}}},
		{client: client, project: "shop", environment: "production", request: MutationRequest{BaseRevision: 7, Operation: MutationOperation{Type: "set", Key: strings.Repeat("K", 129), Value: &value}}},
		{client: client, project: "shop", environment: "production", request: MutationRequest{BaseRevision: 7, Message: strings.Repeat("m", 1025), Operation: MutationOperation{Type: "set", Key: "FEATURE", Value: &value}}},
		{client: client, project: "shop", environment: "production", request: MutationRequest{BaseRevision: 7, Operation: MutationOperation{Type: "set", Key: "FEATURE", Value: mutationStringPointer(strings.Repeat("v", 1<<20+1))}}},
		{client: client, project: "shop", environment: "production", request: MutationRequest{BaseRevision: 7, Operation: MutationOperation{Type: "set", Key: "FEATURE"}}},
		{client: client, project: "shop", environment: "production", request: MutationRequest{BaseRevision: 7, Operation: MutationOperation{Type: "unset", Key: "FEATURE", Value: &value}}},
		{client: client, project: "shop", environment: "production", request: MutationRequest{BaseRevision: 7, Operation: MutationOperation{Type: "unset", Key: "FEATURE", Service: mutationStringPointer("")}}},
		{client: client, project: "shop", environment: "production", request: MutationRequest{BaseRevision: 7, Operation: MutationOperation{Type: "replace", Key: "FEATURE", Value: &value}}},
		{client: client, project: "shop", environment: "production", request: MutationRequest{BaseRevision: 7, Message: string([]byte{0xff}), Operation: MutationOperation{Type: "set", Key: "FEATURE", Value: &value}}},
		{client: client, project: "shop", environment: "production", request: MutationRequest{BaseRevision: 7, Operation: MutationOperation{Type: "set", Key: "FEATURE", Value: mutationStringPointer(string([]byte{0xff}))}}},
		{client: client, project: "shop", environment: "production", request: MutationRequest{BaseRevision: 7, Operation: MutationOperation{Type: "set", Key: "FEATURE", Service: mutationStringPointer(strings.Repeat("s", maxServiceBytes+1)), Value: &value}}},
	}
	for _, test := range tests {
		if _, err := test.client.MutateConfig(context.Background(), test.project, test.environment, test.request); err == nil {
			t.Fatal("invalid mutation input was accepted")
		}
	}
	if attempts.Load() != 0 {
		t.Fatal("invalid mutation input made a request")
	}
}

func TestClientMutateConfigAcceptsBoundaryInputSizes(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		_, _ = io.WriteString(w, `{"project":"shop","environment":"production","revision":7,"created":false}`)
	}))
	defer server.Close()
	client := newMutationClient(t, server.URL, "token")
	_, err := client.MutateConfig(context.Background(), "shop", "production", MutationRequest{
		BaseRevision: 7,
		Message:      strings.Repeat("m", 1024),
		Operation: MutationOperation{
			Type:  "set",
			Key:   strings.Repeat("K", 128),
			Value: mutationStringPointer(strings.Repeat("v", 1<<20)),
		},
	})
	if err != nil || attempts.Load() != 1 {
		t.Fatal("boundary mutation input was not sent exactly once")
	}
}

func validMutationRequestFixture() MutationRequest {
	return MutationRequest{BaseRevision: 7, Operation: MutationOperation{Type: "unset", Key: "FEATURE"}}
}

func newMutationClient(t *testing.T, baseURL, token string) *Client {
	t.Helper()
	client, err := NewClient(baseURL, token)
	if err != nil {
		t.Fatal("mutation client could not be created")
	}
	return client
}

func mutationStringPointer(value string) *string { return &value }

func assertMutationErrorOmits(t *testing.T, err error, secrets ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, secret := range secrets {
		if secret != "" && strings.Contains(err.Error(), secret) {
			t.Fatal("error contains sensitive data")
		}
	}
}
