package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	mutationClientMaxKeyBytes     = 128
	mutationClientMaxValueBytes   = 1 << 20
	mutationClientMaxMessageBytes = 1024
)

type MutationOperation struct {
	Type    string  `json:"type"`
	Key     string  `json:"key"`
	Value   *string `json:"value,omitempty"`
	Service *string `json:"service,omitempty"`
}

type MutationRequest struct {
	BaseRevision int64             `json:"base_revision"`
	Message      string            `json:"message"`
	Operation    MutationOperation `json:"operation"`
}

type MutationResponse struct {
	Project     string `json:"project"`
	Environment string `json:"environment"`
	Revision    int64  `json:"revision"`
	Created     bool   `json:"created"`
}

func (c *Client) MutateConfig(ctx context.Context, project, environment string, mutation MutationRequest) (MutationResponse, error) {
	if c == nil || c.http == nil || !slugPattern.MatchString(project) || !slugPattern.MatchString(environment) {
		return MutationResponse{}, errors.New("invalid project or environment")
	}
	if !validMutationRequest(mutation) {
		return MutationResponse{}, errors.New("invalid mutation request")
	}

	endpoint, err := url.Parse(c.baseURL)
	if err != nil {
		return MutationResponse{}, errors.New("invalid client configuration")
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/api/v1/projects/" + project + "/environments/" + environment + "/config"
	endpoint.RawPath = ""
	endpoint.RawQuery = ""

	body, err := json.Marshal(mutation)
	if err != nil {
		return MutationResponse{}, errors.New("could not encode request")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return MutationResponse{}, errors.New("could not create request")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.token)

	response, err := c.http.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return MutationResponse{}, ctxErr
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return MutationResponse{}, context.DeadlineExceeded
		}
		if errors.Is(err, context.Canceled) {
			return MutationResponse{}, context.Canceled
		}
		return MutationResponse{}, errRequestTransport
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, int64(maxResponseBodyBytes)+1))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return MutationResponse{}, ctxErr
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return MutationResponse{}, context.DeadlineExceeded
		}
		if errors.Is(err, context.Canceled) {
			return MutationResponse{}, context.Canceled
		}
		return MutationResponse{}, errResponseRead
	}
	if len(responseBody) > maxResponseBodyBytes {
		return MutationResponse{}, errResponseTooLarge
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		return MutationResponse{}, decodeAPIError(response.StatusCode, responseBody)
	}

	payload, err := decodeMutationResponse(responseBody)
	if err != nil || payload.Project != project || payload.Environment != environment {
		return MutationResponse{}, errInvalidResponse
	}
	if response.StatusCode == http.StatusCreated && (!payload.Created || payload.Revision != mutation.BaseRevision+1) {
		return MutationResponse{}, errInvalidResponse
	}
	if response.StatusCode == http.StatusOK && (payload.Created || payload.Revision != mutation.BaseRevision) {
		return MutationResponse{}, errInvalidResponse
	}
	return payload, nil
}

func validMutationRequest(mutation MutationRequest) bool {
	if mutation.BaseRevision < 0 || mutation.BaseRevision == math.MaxInt64 || !utf8.ValidString(mutation.Message) || len(mutation.Message) > mutationClientMaxMessageBytes {
		return false
	}
	operation := mutation.Operation
	if !utf8.ValidString(operation.Key) || len(operation.Key) > mutationClientMaxKeyBytes || !environmentKeyPattern.MatchString(operation.Key) {
		return false
	}
	switch operation.Type {
	case "set":
		if operation.Value == nil || !utf8.ValidString(*operation.Value) || len(*operation.Value) > mutationClientMaxValueBytes {
			return false
		}
		return operation.Service == nil || validService(*operation.Service)
	case "unset":
		return operation.Value == nil && operation.Service == nil
	default:
		return false
	}
}

func decodeMutationResponse(body []byte) (MutationResponse, error) {
	if err := validateStrictJSON(body); err != nil {
		return MutationResponse{}, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || len(fields) != 4 {
		return MutationResponse{}, errors.New("invalid server response")
	}
	for field := range fields {
		switch field {
		case "project", "environment", "revision", "created":
		default:
			return MutationResponse{}, errors.New("invalid server response")
		}
	}
	var payload struct {
		Project     *string `json:"project"`
		Environment *string `json:"environment"`
		Revision    *int64  `json:"revision"`
		Created     *bool   `json:"created"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Project == nil || payload.Environment == nil || payload.Revision == nil || payload.Created == nil || *payload.Revision < 0 {
		return MutationResponse{}, errors.New("invalid server response")
	}
	return MutationResponse{
		Project:     *payload.Project,
		Environment: *payload.Environment,
		Revision:    *payload.Revision,
		Created:     *payload.Created,
	}, nil
}
