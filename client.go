// Package epay is the official client for the ePay Business API.
//
// Import it as github.com/epay-et/go-sdk; the package name is epay.
//
// Accept payments from every major Ethiopian mobile wallet and bank with one
// integration:
//
//	client, err := epay.NewClient()          // reads EPAY_SECRET_KEY
//	if err != nil {
//		return err
//	}
//
//	session, err := client.Payments.Initialize(ctx, epay.InitializeParams{
//		Amount:            "250.00",
//		CurrencyCode:      "ETB",
//		CustomerPhone:     "+251911234567",
//		MerchantReference: "order_123",
//	})
//
// The key prefix selects the environment: sk_test_… hits the sandbox and
// sk_live_… moves real money, so there is no separate mode to configure.
//
// Failed requests return an [*APIError], which reports itself as the sentinel
// matching its status code, so callers use [errors.Is]:
//
//	if errors.Is(err, epay.ErrNotFound) { ... }
//
// Transient failures — 429, 5xx, and network errors — are retried with
// exponential backoff and jitter before surfacing.
package epay

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Version is the SDK version, reported in the User-Agent.
const Version = "0.1.1"

// DefaultBaseURL is the production API root.
const DefaultBaseURL = "https://api.epayethiopia.com/v1"

// DefaultTimeout is the default per-attempt timeout.
const DefaultTimeout = 30 * time.Second

// DefaultMaxRetries is the default number of retries after the first attempt.
const DefaultMaxRetries = 2

const (
	backoffBase = 500 * time.Millisecond
	backoffMax  = 8 * time.Second
)

// Client is a client for the ePay Business API. It is safe for concurrent use.
type Client struct {
	// Payments initializes, verifies, and cancels payment sessions.
	Payments *PaymentsService
	// Transactions lists, retrieves, and inspects transaction timelines.
	Transactions *TransactionsService
	// Providers discovers which providers and flows the account can use.
	Providers *ProvidersService
	// Webhooks verifies webhook signatures and parses events.
	Webhooks *WebhookVerifier

	apiKey         string
	baseURL        string
	mode           Mode
	maxRetries     int
	timeout        time.Duration
	defaultHeaders map[string]string
	httpClient     *http.Client
}

// Option configures a [Client].
type Option func(*Client)

// WithAPIKey sets the secret key. It defaults to the EPAY_SECRET_KEY
// environment variable.
func WithAPIKey(apiKey string) Option {
	return func(c *Client) { c.apiKey = strings.TrimSpace(apiKey) }
}

// WithWebhookSecret sets the webhook signing secret. It defaults to the
// EPAY_WEBHOOK_SECRET environment variable.
func WithWebhookSecret(secret string) Option {
	return func(c *Client) { c.Webhooks = &WebhookVerifier{secret: secret} }
}

// WithBaseURL overrides the API root. It defaults to the EPAY_BASE_URL
// environment variable, then [DefaultBaseURL].
func WithBaseURL(baseURL string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(baseURL, "/") }
}

// WithTimeout sets the per-attempt timeout. It defaults to [DefaultTimeout].
func WithTimeout(timeout time.Duration) Option {
	return func(c *Client) { c.timeout = timeout }
}

// WithMaxRetries sets how many retries follow the first attempt, for 429, 5xx,
// and network failures. It defaults to [DefaultMaxRetries].
func WithMaxRetries(retries int) Option {
	return func(c *Client) { c.maxRetries = retries }
}

// WithHTTPClient supplies the [http.Client] to use, for connection pooling, an
// outbound proxy, or mTLS.
//
// Its Timeout, if set, bounds the whole attempt including retries within a
// single call; prefer [WithTimeout], which the SDK applies per attempt.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) { c.httpClient = httpClient }
}

// WithHeader adds a header to every request.
func WithHeader(name, value string) Option {
	return func(c *Client) {
		if c.defaultHeaders == nil {
			c.defaultHeaders = make(map[string]string)
		}
		c.defaultHeaders[name] = value
	}
}

// NewClient builds a client, reading EPAY_SECRET_KEY, EPAY_WEBHOOK_SECRET, and
// EPAY_BASE_URL for anything the options leave unset.
//
// The returned error wraps [ErrConfig] when no API key is available, the key is
// not a secret key, or an option is out of range.
func NewClient(opts ...Option) (*Client, error) {
	client := &Client{
		apiKey:     strings.TrimSpace(os.Getenv("EPAY_SECRET_KEY")),
		baseURL:    strings.TrimRight(envOr("EPAY_BASE_URL", DefaultBaseURL), "/"),
		timeout:    DefaultTimeout,
		maxRetries: DefaultMaxRetries,
		Webhooks:   &WebhookVerifier{secret: os.Getenv("EPAY_WEBHOOK_SECRET")},
	}

	for _, opt := range opts {
		opt(client)
	}

	if client.apiKey == "" {
		return nil, fmt.Errorf(
			"%w: missing ePay API key, pass epay.WithAPIKey or set EPAY_SECRET_KEY", ErrConfig)
	}
	if !strings.HasPrefix(client.apiKey, "sk_") {
		return nil, fmt.Errorf(
			"%w: expected a secret key beginning with \"sk_live_\" or \"sk_test_\"; "+
				"secret keys are in the dashboard under Developers -> API Keys", ErrConfig)
	}
	if client.timeout <= 0 {
		return nil, fmt.Errorf("%w: timeout must be positive, got %s", ErrConfig, client.timeout)
	}
	if client.maxRetries < 0 {
		return nil, fmt.Errorf("%w: maxRetries must not be negative, got %d", ErrConfig, client.maxRetries)
	}
	if client.httpClient == nil {
		client.httpClient = &http.Client{}
	}

	client.mode = modeFromAPIKey(client.apiKey)

	client.Payments = &PaymentsService{client: client}
	client.Transactions = &TransactionsService{client: client}
	client.Providers = &ProvidersService{client: client}

	return client, nil
}

// Mode reports which environment the configured key targets. It is empty for a
// key whose prefix this SDK version does not recognise.
func (c *Client) Mode() Mode { return c.mode }

// IsSandbox reports whether the configured key targets the sandbox.
func (c *Client) IsSandbox() bool { return c.mode == ModeSandbox }

// BaseURL reports the API root every request is sent to.
func (c *Client) BaseURL() string { return c.baseURL }

// String renders the client without leaking the API key.
func (c *Client) String() string {
	mode := string(c.mode)
	if mode == "" {
		mode = "unknown"
	}
	return fmt.Sprintf("epay.Client{mode: %s, apiKey: %s}", mode, maskSecret(c.apiKey))
}

// modeFromAPIKey derives the traffic mode from a key prefix. It returns an
// empty Mode for an sk_-prefixed key with an unrecognised environment segment,
// so a future key format does not break the client.
func modeFromAPIKey(apiKey string) Mode {
	switch {
	case strings.HasPrefix(apiKey, "sk_live_"):
		return ModeLive
	case strings.HasPrefix(apiKey, "sk_test_"):
		return ModeSandbox
	default:
		return ""
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// request describes a single API call.
type request struct {
	method string
	path   string
	query  url.Values
	body   any
	header map[string]string
	// retryable overrides the default, which retries GET only. A POST is
	// retryable when every attempt carries the same idempotency key.
	retryable *bool
}

// Do sends a request and decodes the response body into out, which may be nil
// for endpoints that return no content.
//
// It is the escape hatch for endpoints this SDK version does not model yet, and
// applies the same authentication, timeout, retry, and error handling as the
// typed methods.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	return c.do(ctx, request{method: method, path: path, query: query, body: body}, out)
}

func (c *Client) do(ctx context.Context, req request, out any) error {
	endpoint, err := c.buildURL(req.path, req.query)
	if err != nil {
		return err
	}

	label := req.method + " " + req.path

	var payload []byte
	if req.body != nil {
		payload, err = json.Marshal(req.body)
		if err != nil {
			return fmt.Errorf("epay: could not encode request body for %s: %w", label, err)
		}
	}

	retryable := req.method == http.MethodGet
	if req.retryable != nil {
		retryable = *req.retryable
	}

	attempts := 1
	if retryable {
		attempts = c.maxRetries + 1
	}

	var lastErr error

	for attempt := 0; attempt < attempts; attempt++ {
		isLast := attempt == attempts-1

		status, respBody, header, err := c.attempt(ctx, req, endpoint, payload)
		if err != nil {
			lastErr = err
			if isLast || ctx.Err() != nil {
				return err
			}
			if waitErr := sleepCtx(ctx, backoffDelay(attempt, 0)); waitErr != nil {
				return waitErr
			}
			continue
		}

		if status >= 200 && status < 300 {
			return decodeInto(respBody, out, label)
		}

		apiErr := &APIError{
			Status:  status,
			Message: parseErrorMessage(respBody, status),
			Body:    respBody,
			Header:  header,
			Request: label,
		}

		if isLast || !shouldRetryStatus(status) {
			return apiErr
		}

		lastErr = apiErr
		if waitErr := sleepCtx(ctx, backoffDelay(attempt, apiErr.RetryAfter())); waitErr != nil {
			return waitErr
		}
	}

	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("%w: %s failed after %d attempts", ErrConnection, label, attempts)
}

// attempt performs one HTTP round trip and returns the status, body, and headers.
func (c *Client) attempt(
	ctx context.Context,
	req request,
	endpoint string,
	payload []byte,
) (int, []byte, http.Header, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	var bodyReader io.Reader
	if payload != nil {
		// A fresh reader per attempt, so a retry re-sends the same bytes.
		bodyReader = bytes.NewReader(payload)
	}

	httpReq, err := http.NewRequestWithContext(attemptCtx, req.method, endpoint, bodyReader)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("%w: could not build request: %v", ErrConnection, err)
	}

	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("User-Agent", "epay-go/"+Version)
	for name, value := range c.defaultHeaders {
		httpReq.Header.Set(name, value)
	}
	for name, value := range req.header {
		httpReq.Header.Set(name, value)
	}
	if payload != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		// A deadline the SDK imposed is a timeout; a cancelled parent context
		// belongs to the caller and is returned as-is.
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return 0, nil, nil, fmt.Errorf(
				"%w: %s did not respond within %s", ErrTimeout, endpoint, c.timeout)
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, nil, nil, ctxErr
		}
		return 0, nil, nil, fmt.Errorf("%w at %s: %v", ErrConnection, endpoint, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("%w: could not read response from %s: %v", ErrConnection, endpoint, err)
	}

	return resp.StatusCode, respBody, resp.Header, nil
}

func (c *Client) buildURL(path string, query url.Values) (string, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	endpoint := c.baseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}

	if _, err := url.Parse(endpoint); err != nil {
		return "", fmt.Errorf("%w: invalid request URL %q: %v", ErrConfig, endpoint, err)
	}

	return endpoint, nil
}

// decodeInto unmarshals a response body, tolerating the empty body that 204
// responses carry.
func decodeInto(body []byte, out any, label string) error {
	if out == nil || len(bytes.TrimSpace(body)) == 0 {
		return nil
	}

	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("epay: could not decode response from %s: %w", label, err)
	}

	return nil
}

func shouldRetryStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooManyRequests:
		return true
	default:
		return status >= 500
	}
}

// backoffDelay computes an exponential backoff with jitter, honouring
// Retry-After when the server sent one. Jitter keeps concurrent clients from
// retrying in lockstep.
func backoffDelay(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return min(retryAfter, backoffMax)
	}

	ceiling := time.Duration(math.Min(
		float64(backoffBase)*math.Pow(2, float64(attempt)),
		float64(backoffMax),
	))

	return ceiling/2 + time.Duration(randInt63n(int64(ceiling/2)+1))
}

// sleepCtx waits for d, or returns early if ctx is done.
func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// randInt63n returns a non-negative random int64 below n, falling back to 0 if
// the system CSPRNG is unavailable. Jitter need not be cryptographic, but
// crypto/rand avoids seeding a package-level PRNG.
func randInt63n(n int64) int64 {
	if n <= 0 {
		return 0
	}

	value, err := rand.Int(rand.Reader, big.NewInt(n))
	if err != nil {
		return 0
	}

	return value.Int64()
}

// newIdempotencyKey generates a per-call key, so an internal retry of
// initialize cannot create a second transaction.
func newIdempotencyKey() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// A key is better than none: the timestamp still differs per call.
		return fmt.Sprintf("epay_%d", time.Now().UnixNano())
	}
	return "epay_" + hex.EncodeToString(buf)
}

func boolPtr(value bool) *bool { return &value }
