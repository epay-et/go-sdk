package epay

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func apiError(status int, body string, header http.Header) *APIError {
	if header == nil {
		header = http.Header{}
	}
	return &APIError{
		Status:  status,
		Message: parseErrorMessage([]byte(body), status),
		Body:    []byte(body),
		Header:  header,
		Request: "GET /transactions",
	}
}

func TestAPIErrorMapsStatusesToSentinels(t *testing.T) {
	cases := map[int]error{
		400: ErrBadRequest,
		401: ErrAuthentication,
		403: ErrPermissionDenied,
		404: ErrNotFound,
		409: ErrConflict,
		422: ErrUnprocessableEntity,
		429: ErrRateLimit,
		500: ErrServer,
		503: ErrServer,
	}

	for status, sentinel := range cases {
		err := apiError(status, "", nil)
		if !errors.Is(err, sentinel) {
			t.Errorf("status %d should match its sentinel", status)
		}
	}

	// An unmapped status matches no sentinel, but is still an *APIError.
	teapot := apiError(418, "", nil)
	for _, sentinel := range []error{ErrBadRequest, ErrNotFound, ErrServer} {
		if errors.Is(teapot, sentinel) {
			t.Errorf("418 should not match %v", sentinel)
		}
	}

	var target *APIError
	if !errors.As(error(teapot), &target) || target.Status != 418 {
		t.Error("errors.As should recover the *APIError")
	}
}

func TestParseErrorMessageHandlesBothEnvelopes(t *testing.T) {
	cases := map[string]string{
		// Transaction endpoints.
		`{"status":"error","data":{"message":"Missing or malformed Authorization header"}}`: "Missing or malformed Authorization header",
		// Payment-provider endpoints.
		`{"statusCode":401,"message":"Invalid or inactive API key","error":"Unauthorized"}`: "Invalid or inactive API key",
		// A list of validation messages.
		`{"statusCode":400,"message":["amount must be positive","currencyCode is required"]}`: "amount must be positive; currencyCode is required",
		// Only an error field.
		`{"error":"Unauthorized"}`: "Unauthorized",
		// Non-JSON.
		`Bad Gateway`: "Bad Gateway",
	}

	for body, want := range cases {
		if got := parseErrorMessage([]byte(body), 400); got != want {
			t.Errorf("parseErrorMessage(%s) = %q, want %q", body, got, want)
		}
	}

	for _, body := range []string{"", "{}", "null"} {
		if got := parseErrorMessage([]byte(body), 500); got != "HTTP 500" {
			t.Errorf("parseErrorMessage(%q) = %q, want HTTP 500", body, got)
		}
	}
}

func TestAPIErrorRetryAfter(t *testing.T) {
	numeric := apiError(429, "", http.Header{"Retry-After": []string{"30"}})
	if got := numeric.RetryAfter(); got != 30*time.Second {
		t.Errorf("RetryAfter = %s, want 30s", got)
	}

	if got := apiError(429, "", nil).RetryAfter(); got != 0 {
		t.Errorf("RetryAfter without a header = %s, want 0", got)
	}

	httpDate := time.Now().Add(10 * time.Second).UTC().Format(http.TimeFormat)
	dated := apiError(429, "", http.Header{"Retry-After": []string{httpDate}})
	if got := dated.RetryAfter(); got <= 5*time.Second {
		t.Errorf("RetryAfter from an HTTP-date = %s, want > 5s", got)
	}
}

func TestAPIErrorMessageIncludesTheRequest(t *testing.T) {
	err := apiError(404, `{"status":"error","data":{"message":"gone"}}`, nil)

	want := "epay: GET /transactions failed with 404: gone"
	if err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
}
