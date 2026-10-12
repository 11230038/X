package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// maxResponseBytes bounds a response the probe will read. Every response it
// asks for is a small object; the turn's own traffic arrives on the socket.
const maxResponseBytes = 1 << 20

// httpClient talks to the server's JSON API, which the probe needs only to get
// a token and a session to talk in.
type httpClient struct {
	base string
	http *http.Client
}

// login exchanges credentials for an access token.
//
// The credentials are the caller's and are never printed: the probe's output is
// meant to be read and pasted into a review.
func (c *httpClient) login(ctx context.Context, username, password string) (string, error) {
	body := map[string]string{"username": username, "password": password}
	var response struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := c.post(ctx, "/api/v1/auth/login", "", body, http.StatusOK, &response); err != nil {
		return "", fmt.Errorf("log in: %w", err)
	}
	if response.Data.Token == "" {
		return "", fmt.Errorf("log in: the server returned no token")
	}
	return response.Data.Token, nil
}

// createSession opens a conversation for the probe to talk in.
func (c *httpClient) createSession(ctx context.Context, token string) (string, error) {
	var response struct {
		Data struct {
			SessionID string `json:"session_id"`
		} `json:"data"`
	}
	if err := c.post(ctx, "/api/v1/sessions", token, nil, http.StatusCreated, &response); err != nil {
		return "", fmt.Errorf("create a session: %w", err)
	}
	if response.Data.SessionID == "" {
		return "", fmt.Errorf("create a session: the server returned no session_id")
	}
	return response.Data.SessionID, nil
}

func (c *httpClient) post(ctx context.Context, path, token string, body any, want int, into any) error {
	var payload []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode the request: %w", err)
		}
		payload = encoded
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build the request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("reach %s: %w", c.base, err)
	}
	defer func() { _ = response.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("read the response: %w", err)
	}
	if response.StatusCode != want {
		// The envelope's message is the server's own words about what went
		// wrong, which is what the person running the probe needs.
		var failure struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(raw, &failure); err == nil && failure.Error.Code != "" {
			return fmt.Errorf("%s returned %d: %s (%s)", path, response.StatusCode, failure.Error.Message, failure.Error.Code)
		}
		return fmt.Errorf("%s returned %d", path, response.StatusCode)
	}
	if into == nil {
		return nil
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("the response is not the API envelope: %w", err)
	}
	return nil
}
