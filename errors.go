package epay

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Sentinel errors for use with [errors.Is]. An *APIError reports itself as the
// sentinel matching its status code, so callers branch on these rather than on
// status numbers:
//
//	if errors.Is(err, epay.ErrNotFound) { ... }
var (
	// ErrConfig means the client was constructed with unusable options.
	ErrConfig = errors.New("epay: invalid configuration")
	// ErrValidation means a value failed local validation, so no request was sent.
	ErrValidation = errors.New("epay: validation failed")
	// ErrWebhookSignature means a webhook payload failed signature verification.
	ErrWebhookSignature = errors.New("epay: webhook signature verification failed")
	// ErrConnection means the request never produced an HTTP response.
	ErrConnection = errors.New("epay: could not reach the API")
	// ErrTimeout means the request exceeded the configured timeout.
	ErrTimeout = errors.New("epay: request timed out")

	// ErrBadRequest is returned for 400.
	ErrBadRequest = errors.New("epay: bad request")
	// ErrAuthentication is returned for 401: key missing, invalid, or revoked.
	ErrAuthentication = errors.New("epay: authentication failed")
	// ErrPermissionDenied is returned for 403: IP not whitelisted, or the key
	// lacks a required permission.
	ErrPermissionDenied = errors.New("epay: permission denied")
	// ErrNotFound is returned for 404.
	ErrNotFound = errors.New("epay: not found")
	// ErrConflict is returned for 409.
	ErrConflict = errors.New("epay: conflict")
	// ErrUnprocessableEntity is returned for 422.
	ErrUnprocessableEntity = errors.New("epay: unprocessable entity")
	// ErrRateLimit is returned for 429: the 100 requests / 60s per-key budget is
	// exhausted. The client already retries these, so seeing it means the retry
	// budget was also exhausted.
	ErrRateLimit = errors.New("epay: rate limit exceeded")
	// ErrServer is returned for any 5xx.
	ErrServer = errors.New("epay: server error")
)

// APIError describes a non-2xx HTTP response.
type APIError struct {
	// Status is the HTTP status code.
	Status int
	// Message is the human-readable message extracted from the response body.
	Message string
	// Body is the raw response body, for logging or bespoke parsing.
	Body []byte
	// Header holds the response headers.
	Header http.Header
	// Request is the method and path of the originating request, such as
	// "GET /transactions/PAB1".
	Request string
}

func (e *APIError) Error() string {
	if e.Request == "" {
		return fmt.Sprintf("epay: HTTP %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("epay: %s failed with %d: %s", e.Request, e.Status, e.Message)
}

// Is reports the sentinel matching this error's status code, so callers can use
// [errors.Is] instead of comparing numbers.
func (e *APIError) Is(target error) bool {
	return target != nil && target == sentinelForStatus(e.Status)
}

// RetryAfter reports how long to wait before retrying, from the Retry-After
// header. It returns 0 when the header is absent or unparseable.
func (e *APIError) RetryAfter() time.Duration {
	value := e.Header.Get("Retry-After")
	if value == "" {
		return 0
	}

	if seconds, err := strconv.ParseFloat(value, 64); err == nil {
		if seconds < 0 {
			return 0
		}
		return time.Duration(seconds * float64(time.Second))
	}

	if when, err := http.ParseTime(value); err == nil {
		if delay := time.Until(when); delay > 0 {
			return delay
		}
	}
	return 0
}

func sentinelForStatus(status int) error {
	switch {
	case status == http.StatusBadRequest:
		return ErrBadRequest
	case status == http.StatusUnauthorized:
		return ErrAuthentication
	case status == http.StatusForbidden:
		return ErrPermissionDenied
	case status == http.StatusNotFound:
		return ErrNotFound
	case status == http.StatusConflict:
		return ErrConflict
	case status == http.StatusUnprocessableEntity:
		return ErrUnprocessableEntity
	case status == http.StatusTooManyRequests:
		return ErrRateLimit
	case status >= 500:
		return ErrServer
	default:
		return nil
	}
}

// errorEnvelope covers both shapes the API uses to report failures:
// {"status":"error","data":{"message":...}} on the transaction endpoints and
// {"statusCode","message","error"} on the provider endpoints, where message may
// itself be a list of validation strings.
type errorEnvelope struct {
	Message json.RawMessage `json:"message"`
	Error   string          `json:"error"`
	Data    *struct {
		Message json.RawMessage `json:"message"`
	} `json:"data"`
}

// parseErrorMessage extracts a human-readable message from an error body.
func parseErrorMessage(body []byte, status int) string {
	fallback := fmt.Sprintf("HTTP %d", status)

	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return fallback
	}

	var envelope errorEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		// Not JSON: the raw text is the most useful thing available.
		return trimmed
	}

	if envelope.Data != nil {
		if message := flattenMessage(envelope.Data.Message); message != "" {
			return message
		}
	}
	if message := flattenMessage(envelope.Message); message != "" {
		return message
	}
	if strings.TrimSpace(envelope.Error) != "" {
		return strings.TrimSpace(envelope.Error)
	}

	return fallback
}

// flattenMessage reduces a message field to a string, joining validation lists.
func flattenMessage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return strings.TrimSpace(single)
	}

	var many []string
	if err := json.Unmarshal(raw, &many); err == nil && len(many) > 0 {
		return strings.Join(many, "; ")
	}

	return ""
}
