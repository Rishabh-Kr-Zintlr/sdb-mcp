package client

import (
	"encoding/json"
	"fmt"
	"strings"
)

// APIError represents a non-successful response from the upstream API: either a
// non-2xx HTTP status or a JSON envelope with "success":false. The upstream
// "Unsupported or unsafe operator" validation failure surfaces here.
type APIError struct {
	// Status is the HTTP status code.
	Status int
	// Message is the human-readable error, taken from the response body's
	// "error"/"message" field when present, else a status-derived fallback.
	Message string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("api error: HTTP %d", e.Status)
	}
	return fmt.Sprintf("api error (HTTP %d): %s", e.Status, e.Message)
}

// newAPIError builds an APIError from a status code and raw response body,
// extracting a message from common envelope shapes ({error} or {message}).
func newAPIError(status int, body []byte) *APIError {
	msg := extractErrorMessage(body)
	return &APIError{Status: status, Message: msg}
}

// extractErrorMessage pulls a message out of an error envelope, falling back to
// a trimmed snippet of the raw body.
func extractErrorMessage(body []byte) string {
	var env struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &env); err == nil {
		if env.Error != "" {
			return env.Error
		}
		if env.Message != "" {
			return env.Message
		}
	}
	s := strings.TrimSpace(string(body))
	const max = 300
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}
