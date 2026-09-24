package epay

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testAPIKey = "sk_test_0123456789abcdef"

// recordedRequest captures what the client sent.
type recordedRequest struct {
	Method string
	Path   string
	Query  string
	Header http.Header
	Body   []byte
}

// reply is one scripted response.
type reply struct {
	status int
	body   string
	header map[string]string
}

// newTestServer answers with each reply in turn and records every request.
func newTestServer(t *testing.T, replies ...reply) (*httptest.Server, *[]recordedRequest) {
	t.Helper()

	var calls []recordedRequest
	index := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		calls = append(calls, recordedRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Query:  r.URL.RawQuery,
			Header: r.Header.Clone(),
			Body:   body,
		})

		if index >= len(replies) {
			t.Errorf("unexpected extra request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		current := replies[index]
		index++

		for name, value := range current.header {
			w.Header().Set(name, value)
		}
		if current.body != "" {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(current.status)
		if current.body != "" {
			_, _ = w.Write([]byte(current.body))
		}
	}))

	t.Cleanup(server.Close)

	return server, &calls
}

// newTestClient points a client at a test server, with retries off by default.
func newTestClient(t *testing.T, server *httptest.Server, opts ...Option) *Client {
	t.Helper()

	base := []Option{
		WithAPIKey(testAPIKey),
		WithBaseURL(server.URL),
		WithMaxRetries(0),
	}

	client, err := NewClient(append(base, opts...)...)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	return client
}

const sessionJSON = `{"reference":"PAB12CD3420260813",` +
	`"checkoutUrl":"https://checkout.e-pay.et/pay/PAB12CD3420260813",` +
	`"status":"success","expiresAt":"2026-08-13T12:30:00.000Z"}`

// --- construction -----------------------------------------------------------

func TestNewClientDerivesModeFromTheKeyPrefix(t *testing.T) {
	live, err := NewClient(WithAPIKey("sk_live_abc123"))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if live.Mode() != ModeLive || live.IsSandbox() {
		t.Errorf("live key gave mode %q, IsSandbox %v", live.Mode(), live.IsSandbox())
	}

	test, err := NewClient(WithAPIKey("sk_test_abc123"))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if !test.IsSandbox() {
		t.Error("test key should report IsSandbox")
	}
}

func TestNewClientRejectsBadConfiguration(t *testing.T) {
	cases := map[string][]Option{
		"non-secret key":   {WithAPIKey("pk_live_abc123")},
		"empty key":        {WithAPIKey("   ")},
		"zero timeout":     {WithAPIKey(testAPIKey), WithTimeout(0)},
		"negative retries": {WithAPIKey(testAPIKey), WithMaxRetries(-1)},
	}

	for name, opts := range cases {
		if _, err := NewClient(opts...); !errors.Is(err, ErrConfig) {
			t.Errorf("%s should wrap ErrConfig, got %v", name, err)
		}
	}
}

func TestClientStringNeverLeaksTheAPIKey(t *testing.T) {
	client, err := NewClient(WithAPIKey("sk_live_supersecretvalue1234"))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	rendered := client.String()
	if want := "sk_live_…1234"; !strings.Contains(rendered, want) {
		t.Errorf("String() = %q, want it to contain %q", rendered, want)
	}
	if strings.Contains(rendered, "supersecretvalue") {
		t.Errorf("String() leaked the key: %q", rendered)
	}
}

// --- initialize -------------------------------------------------------------

func TestInitializeSendsTheDocumentedRequest(t *testing.T) {
	server, calls := newTestServer(t, reply{status: 200, body: sessionJSON})
	client := newTestClient(t, server)

	session, err := client.Payments.Initialize(context.Background(), InitializeParams{
		Amount:            "250",
		CurrencyCode:      "etb",
		CustomerPhone:     "0911234567",
		MerchantReference: "order_123",
		IdempotencyKey:    "order_123",
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	if session.Reference != "PAB12CD3420260813" {
		t.Errorf("Reference = %q", session.Reference)
	}
	if len(*calls) != 1 {
		t.Fatalf("made %d requests, want 1", len(*calls))
	}

	call := (*calls)[0]
	if call.Method != http.MethodPost || call.Path != "/transactions/initialize" {
		t.Errorf("sent %s %s", call.Method, call.Path)
	}
	if got := call.Header.Get("Authorization"); got != "Bearer "+testAPIKey {
		t.Errorf("Authorization = %q", got)
	}
	if got := call.Header.Get("x-idempotency-key"); got != "order_123" {
		t.Errorf("idempotency key = %q", got)
	}

	var body map[string]any
	if err := json.Unmarshal(call.Body, &body); err != nil {
		t.Fatalf("request body was not JSON: %v", err)
	}

	// Values are normalized before being sent.
	if body["amount"] != "250.00" || body["currencyCode"] != "ETB" ||
		body["customerPhone"] != "+251911234567" {
		t.Errorf("body was not normalized: %v", body)
	}
	// Unset optional fields are omitted rather than sent as null.
	if _, present := body["callbackUrl"]; present {
		t.Error("callbackUrl should be omitted when unset")
	}
}

func TestInitializeGeneratesAnIdempotencyKeyWhenOmitted(t *testing.T) {
	server, calls := newTestServer(t, reply{status: 200, body: sessionJSON})
	client := newTestClient(t, server)

	if _, err := client.Payments.Initialize(context.Background(), InitializeParams{
		Amount:        "10.00",
		CurrencyCode:  "ETB",
		CustomerPhone: "0911234567",
	}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	if key := (*calls)[0].Header.Get("x-idempotency-key"); len(key) < 12 {
		t.Errorf("generated idempotency key = %q", key)
	}
}

func TestInitializeValidatesBeforeSpendingARequest(t *testing.T) {
	server, calls := newTestServer(t)
	client := newTestClient(t, server)

	cases := map[string]InitializeParams{
		"bad amount":   {Amount: "-1", CurrencyCode: "ETB", CustomerPhone: "0911234567"},
		"bad currency": {Amount: "1.00", CurrencyCode: "ET", CustomerPhone: "0911234567"},
		"bad phone":    {Amount: "1.00", CurrencyCode: "ETB", CustomerPhone: "123"},
	}

	for name, params := range cases {
		if _, err := client.Payments.Initialize(context.Background(), params); !errors.Is(err, ErrValidation) {
			t.Errorf("%s should wrap ErrValidation, got %v", name, err)
		}
	}

	if len(*calls) != 0 {
		t.Errorf("made %d requests, want 0", len(*calls))
	}
}

// --- verify and cancel ------------------------------------------------------

func TestVerifyReturnsTheReceipt(t *testing.T) {
	body := `{"reference":"PAB1","status":"completed","amount":"250.00","serviceFee":"7.50",` +
		`"customer":{"name":"Abebe Bikila","email":null,"phone":"+251911234567"},` +
		`"paidAt":"2026-08-13T11:47:00.000Z","createdAt":"2026-08-13T11:30:00.000Z"}`

	server, calls := newTestServer(t, reply{status: 200, body: body})
	client := newTestClient(t, server)

	receipt, err := client.Payments.Verify(context.Background(), "PAB1")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if (*calls)[0].Path != "/transactions/PAB1/verify" {
		t.Errorf("path = %q", (*calls)[0].Path)
	}
	if receipt.ServiceFee == nil || *receipt.ServiceFee != "7.50" {
		t.Error("ServiceFee was not decoded")
	}
	if receipt.Customer.Email != nil {
		t.Error("a null customer email should decode to nil")
	}
}

func TestVerifyReportsAnIncompleteTransaction(t *testing.T) {
	body := `{"status":"error","data":{"message":"Transaction is not completed. Current status: pending"}}`
	server, _ := newTestServer(t, reply{status: 400, body: body})
	client := newTestClient(t, server)

	_, err := client.Payments.Verify(context.Background(), "PAB1")
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("expected ErrBadRequest, got %v", err)
	}
	if !strings.Contains(err.Error(), "Current status: pending") {
		t.Errorf("error lost the API message: %v", err)
	}
}

func TestCancelSucceedsOn204AndIsNeverRetried(t *testing.T) {
	server, calls := newTestServer(t, reply{status: 204})
	client := newTestClient(t, server)

	if err := client.Payments.Cancel(context.Background(), "PAB1"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if (*calls)[0].Path != "/transactions/PAB1/cancel" {
		t.Errorf("path = %q", (*calls)[0].Path)
	}

	// A non-idempotent POST must be attempted exactly once, even with retries on.
	failing, failingCalls := newTestServer(t,
		reply{status: 500}, reply{status: 500}, reply{status: 500}, reply{status: 500})
	retrying := newTestClient(t, failing, WithMaxRetries(3))

	if err := retrying.Payments.Cancel(context.Background(), "PAB1"); !errors.Is(err, ErrServer) {
		t.Fatalf("expected ErrServer, got %v", err)
	}
	if len(*failingCalls) != 1 {
		t.Errorf("cancel was attempted %d times, want 1", len(*failingCalls))
	}
}

func TestEmptyReferencesAreRejectedLocally(t *testing.T) {
	server, calls := newTestServer(t)
	client := newTestClient(t, server)

	if _, err := client.Transactions.Retrieve(context.Background(), ""); !errors.Is(err, ErrValidation) {
		t.Errorf("Retrieve(\"\") should wrap ErrValidation, got %v", err)
	}
	if err := client.Payments.Cancel(context.Background(), ""); !errors.Is(err, ErrValidation) {
		t.Errorf("Cancel(\"\") should wrap ErrValidation, got %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("made %d requests, want 0", len(*calls))
	}
}

// --- transactions -----------------------------------------------------------

func TestListBuildsTheDocumentedQuery(t *testing.T) {
	server, calls := newTestServer(t,
		reply{status: 200, body: `{"data":[],"nextCursor":null,"hasMore":false}`})
	client := newTestClient(t, server)

	if _, err := client.Transactions.List(context.Background(), ListParams{
		Status:   StatusCompleted,
		Currency: "etb",
		From:     "2026-08-01",
		To:       "2026-08-31",
	}); err != nil {
		t.Fatalf("List: %v", err)
	}

	query := (*calls)[0].Query
	for _, want := range []string{"status=completed", "currency=ETB", "from=2026-08-01", "to=2026-08-31"} {
		if !strings.Contains(query, want) {
			t.Errorf("query %q is missing %q", query, want)
		}
	}
}

func TestListRejectsAnInvertedRangeAndUnknownStatus(t *testing.T) {
	server, calls := newTestServer(t)
	client := newTestClient(t, server)

	if _, err := client.Transactions.List(context.Background(), ListParams{
		From: "2026-08-31", To: "2026-08-01",
	}); !errors.Is(err, ErrValidation) {
		t.Errorf("inverted range should wrap ErrValidation, got %v", err)
	}

	if _, err := client.Transactions.List(context.Background(), ListParams{
		Status: TransactionStatus("expired"),
	}); !errors.Is(err, ErrValidation) {
		t.Errorf("unknown status should wrap ErrValidation, got %v", err)
	}

	if len(*calls) != 0 {
		t.Errorf("made %d requests, want 0", len(*calls))
	}
}

func TestIterateWalksEveryPageAndKeepsFilters(t *testing.T) {
	row := func(reference string) string {
		return `{"reference":"` + reference + `","merchantReference":null,"status":"completed",` +
			`"amount":"10.00","currencyCode":"ETB","paidAt":null,"createdAt":"2026-08-13T11:30:00.000Z"}`
	}

	server, calls := newTestServer(t,
		reply{status: 200, body: `{"data":[` + row("P1") + `,` + row("P2") + `],"nextCursor":"cur_1","hasMore":true}`},
		reply{status: 200, body: `{"data":[` + row("P3") + `],"nextCursor":"cur_2","hasMore":true}`},
		reply{status: 200, body: `{"data":[` + row("P4") + `],"nextCursor":null,"hasMore":false}`},
	)
	client := newTestClient(t, server)

	it := client.Transactions.Iterate(ListParams{Status: StatusCompleted})

	var seen []string
	for it.Next(context.Background()) {
		seen = append(seen, it.Transaction().Reference)
	}
	if err := it.Err(); err != nil {
		t.Fatalf("iterator: %v", err)
	}

	if len(seen) != 4 || seen[0] != "P1" || seen[3] != "P4" {
		t.Errorf("walked %v, want P1..P4", seen)
	}
	if len(*calls) != 3 {
		t.Fatalf("made %d requests, want 3", len(*calls))
	}

	// The cursor advances while the filter persists across pages.
	if strings.Contains((*calls)[0].Query, "cursor=") {
		t.Error("the first page should carry no cursor")
	}
	for i, want := range map[int]string{1: "cursor=cur_1", 2: "cursor=cur_2"} {
		if !strings.Contains((*calls)[i].Query, want) {
			t.Errorf("request %d query %q is missing %q", i, (*calls)[i].Query, want)
		}
		if !strings.Contains((*calls)[i].Query, "status=completed") {
			t.Errorf("request %d dropped the status filter", i)
		}
	}
}

func TestCollectStopsAtTheLimit(t *testing.T) {
	row := `{"reference":"P","status":"pending","amount":"1.00","currencyCode":"ETB","createdAt":"x"}`

	server, calls := newTestServer(t,
		reply{status: 200, body: `{"data":[` + row + `,` + row + `],"nextCursor":"cur_1","hasMore":true}`},
		reply{status: 200, body: `{"data":[` + row + `],"nextCursor":"cur_2","hasMore":true}`},
	)
	client := newTestClient(t, server)

	collected, err := client.Transactions.Iterate(ListParams{}).Collect(context.Background(), 3)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(collected) != 3 {
		t.Errorf("collected %d, want 3", len(collected))
	}
	if len(*calls) != 2 {
		t.Errorf("made %d requests, want 2", len(*calls))
	}
}

func TestTimelineDecodesErrorDetails(t *testing.T) {
	body := `{"reference":"PFF98AB1220260812","events":[` +
		`{"eventType":"session_created","occurredAt":"a","errorCode":null,"errorMessage":null},` +
		`{"eventType":"payment_failed","occurredAt":"b","errorCode":"INSUFFICIENT_FUNDS","errorMessage":"no balance"}]}`

	server, _ := newTestServer(t, reply{status: 200, body: body})
	client := newTestClient(t, server)

	timeline, err := client.Transactions.Timeline(context.Background(), "PFF98AB1220260812")
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	if len(timeline.Events) != 2 {
		t.Fatalf("decoded %d events, want 2", len(timeline.Events))
	}
	if timeline.Events[0].ErrorCode != nil {
		t.Error("a null errorCode should decode to nil")
	}
	if timeline.Events[1].ErrorCode == nil || *timeline.Events[1].ErrorCode != "INSUFFICIENT_FUNDS" {
		t.Error("errorCode was not decoded")
	}
}

// --- providers --------------------------------------------------------------

func TestProvidersAreUnwrappedFromTheEnvelope(t *testing.T) {
	server, calls := newTestServer(t, reply{
		status: 200,
		body:   `{"data":[{"providerCode":"telebirr","providerName":"Telebirr","flowCode":"ussd"}]}`,
	})
	client := newTestClient(t, server)

	providers, err := client.Providers.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if (*calls)[0].Path != "/payment-providers/list" {
		t.Errorf("path = %q", (*calls)[0].Path)
	}
	if len(providers) != 1 || providers[0].ProviderCode != "telebirr" {
		t.Errorf("decoded %v", providers)
	}
}

func TestListEnabledFiltersDisabledProviders(t *testing.T) {
	server, _ := newTestServer(t, reply{
		status: 200,
		body: `{"data":[{"providerCode":"cbe_birr","flowCode":"mobile_banking","isEnabled":true},` +
			`{"providerCode":"awash_bank","flowCode":"mobile_banking","isEnabled":false}]}`,
	})
	client := newTestClient(t, server)

	enabled, err := client.Providers.ListEnabled(context.Background())
	if err != nil {
		t.Fatalf("ListEnabled: %v", err)
	}
	if len(enabled) != 1 || enabled[0].ProviderCode != "cbe_birr" {
		t.Errorf("enabled = %v", enabled)
	}
}

func TestAMissingPermissionIsPermissionDenied(t *testing.T) {
	body := `{"statusCode":403,"message":"Missing get_platform_payment_provider permission","error":"Forbidden"}`
	server, _ := newTestServer(t, reply{status: 403, body: body})
	client := newTestClient(t, server)

	_, err := client.Providers.GetAll(context.Background())
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied, got %v", err)
	}
	if !strings.Contains(err.Error(), "get_platform_payment_provider") {
		t.Errorf("error lost the API message: %v", err)
	}
}

// --- transport --------------------------------------------------------------

func TestA429IsRetriedAndReusesTheIdempotencyKey(t *testing.T) {
	server, calls := newTestServer(t,
		reply{status: 429, body: `{"status":"error","data":{"message":"Rate limit exceeded"}}`,
			header: map[string]string{"Retry-After": "0"}},
		reply{status: 200, body: sessionJSON},
	)
	client := newTestClient(t, server, WithMaxRetries(2))

	if _, err := client.Payments.Initialize(context.Background(), InitializeParams{
		Amount:         "10.00",
		CurrencyCode:   "ETB",
		CustomerPhone:  "0911234567",
		IdempotencyKey: "order_9",
	}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	if len(*calls) != 2 {
		t.Fatalf("made %d requests, want 2", len(*calls))
	}
	for i := range *calls {
		if got := (*calls)[i].Header.Get("x-idempotency-key"); got != "order_9" {
			t.Errorf("attempt %d used key %q; a retry must reuse it or it would double-charge", i, got)
		}
	}
}

func TestA5xxOnAGetIsRetriedAndA4xxIsNot(t *testing.T) {
	server, calls := newTestServer(t,
		reply{status: 503},
		reply{status: 200, body: `{"reference":"P1"}`},
	)
	client := newTestClient(t, server, WithMaxRetries(1))

	if _, err := client.Transactions.Retrieve(context.Background(), "P1"); err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(*calls) != 2 {
		t.Errorf("made %d requests, want 2", len(*calls))
	}

	noRetry, noRetryCalls := newTestServer(t,
		reply{status: 401, body: `{"status":"error","data":{"message":"Invalid API key"}}`})
	client2 := newTestClient(t, noRetry, WithMaxRetries(3))

	if _, err := client2.Transactions.Retrieve(context.Background(), "P1"); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("expected ErrAuthentication, got %v", err)
	}
	if len(*noRetryCalls) != 1 {
		t.Errorf("a 4xx was attempted %d times, want 1", len(*noRetryCalls))
	}
}

func TestTheRateLimitErrorSurfacesAfterTheBudget(t *testing.T) {
	server, calls := newTestServer(t,
		reply{status: 429, header: map[string]string{"Retry-After": "0"}},
		reply{status: 429, header: map[string]string{"Retry-After": "30"}},
	)
	client := newTestClient(t, server, WithMaxRetries(1))

	_, err := client.Transactions.Retrieve(context.Background(), "P1")
	if !errors.Is(err, ErrRateLimit) {
		t.Fatalf("expected ErrRateLimit, got %v", err)
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatal("expected an *APIError")
	}
	if apiErr.RetryAfter() != 30*time.Second {
		t.Errorf("RetryAfter = %s, want 30s", apiErr.RetryAfter())
	}
	if len(*calls) != 2 {
		t.Errorf("made %d requests, want 2", len(*calls))
	}
}

func TestATimeoutIsReportedAsErrTimeout(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(slow.Close)

	client := newTestClient(t, slow, WithTimeout(20*time.Millisecond))

	if _, err := client.Transactions.Retrieve(context.Background(), "P1"); !errors.Is(err, ErrTimeout) {
		t.Fatalf("expected ErrTimeout, got %v", err)
	}
}

func TestACancelledContextIsReturnedToTheCaller(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	t.Cleanup(slow.Close)

	client := newTestClient(t, slow)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, err := client.Transactions.Retrieve(ctx, "P1")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestDefaultHeadersAreSentOnEveryRequest(t *testing.T) {
	server, calls := newTestServer(t, reply{status: 200, body: `{}`})
	client := newTestClient(t, server, WithHeader("X-Trace-Id", "trace_1"))

	if _, err := client.Transactions.Retrieve(context.Background(), "P1"); err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if got := (*calls)[0].Header.Get("X-Trace-Id"); got != "trace_1" {
		t.Errorf("X-Trace-Id = %q", got)
	}
}

func TestDoReachesUnmodelledEndpoints(t *testing.T) {
	server, calls := newTestServer(t, reply{status: 200, body: `{"ok":true}`})
	client := newTestClient(t, server)

	var out struct {
		OK bool `json:"ok"`
	}
	if err := client.Do(context.Background(), http.MethodGet, "/some/future/endpoint", nil, nil, &out); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if !out.OK {
		t.Error("response was not decoded")
	}
	if (*calls)[0].Path != "/some/future/endpoint" {
		t.Errorf("path = %q", (*calls)[0].Path)
	}
}

// --- webhooks ---------------------------------------------------------------

const webhookSecret = "whsec_test_secret"

const webhookPayload = `{"event":"payment.success","mode":"live","reference":"PAB12CD3420260813",` +
	`"merchantReference":"order_123","amount":"250.00","serviceFee":"7.50","currency":"ETB",` +
	`"status":"completed","paymentMethod":"telebirr",` +
	`"customer":{"name":"Abebe Bikila","email":"abebe@example.com","phone":"+251911234567"},` +
	`"paidAt":"2026-08-13T11:47:00.000Z","createdAt":"2026-08-13T11:30:00.000Z"}`

func webhookSignature(payload, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestWebhookVerifyAcceptsAGenuineDelivery(t *testing.T) {
	verifier := NewWebhookVerifier(webhookSecret)
	signature := webhookSignature(webhookPayload, webhookSecret)

	if !verifier.Verify([]byte(webhookPayload), signature) {
		t.Error("a genuine delivery should verify")
	}

	// The prefix is optional and case-insensitive.
	bare := signature[len("sha256="):]
	if !verifier.Verify([]byte(webhookPayload), bare) {
		t.Error("a bare hex digest should verify")
	}
	if !verifier.Verify([]byte(webhookPayload), "SHA256="+bare) {
		t.Error("an uppercase prefix should verify")
	}
	if !verifier.Verify([]byte(webhookPayload), "  "+signature+"  ") {
		t.Error("surrounding whitespace should be tolerated")
	}
}

func TestWebhookVerifyRejectsTamperingAndMalformedHeaders(t *testing.T) {
	verifier := NewWebhookVerifier(webhookSecret)
	signature := webhookSignature(webhookPayload, webhookSecret)

	tampered := webhookPayload[:len(webhookPayload)-1] + " "
	if verifier.Verify([]byte(tampered), signature) {
		t.Error("a tampered body must not verify")
	}
	if verifier.Verify([]byte(webhookPayload), webhookSignature(webhookPayload, "wrong")) {
		t.Error("a wrong secret must not verify")
	}

	for _, header := range []string{"", "sha256=", "not-hex!!", "sha256=zzzz", "sha256=abc"} {
		if verifier.Verify([]byte(webhookPayload), header) {
			t.Errorf("header %q must not verify", header)
		}
	}
}

func TestConstructEventFailsClosed(t *testing.T) {
	verifier := NewWebhookVerifier(webhookSecret)

	event, err := verifier.ConstructEvent(
		[]byte(webhookPayload), webhookSignature(webhookPayload, webhookSecret))
	if err != nil {
		t.Fatalf("ConstructEvent: %v", err)
	}
	if event.Event != EventPaymentSuccess || event.Reference != "PAB12CD3420260813" {
		t.Errorf("decoded %+v", event)
	}
	if !event.Event.IsKnown() {
		t.Error("payment.success should be a known event")
	}
	if event.Customer.Phone == nil || *event.Customer.Phone != "+251911234567" {
		t.Error("customer phone was not decoded")
	}

	for _, header := range []string{"", "sha256=00"} {
		if _, err := verifier.ConstructEvent([]byte(webhookPayload), header); !errors.Is(err, ErrWebhookSignature) {
			t.Errorf("header %q should wrap ErrWebhookSignature, got %v", header, err)
		}
	}

	unconfigured := NewWebhookVerifier("")
	if _, err := verifier.ConstructEvent([]byte("not json"),
		webhookSignature("not json", webhookSecret)); !errors.Is(err, ErrValidation) {
		t.Errorf("a non-JSON body should wrap ErrValidation, got %v", err)
	}
	if _, err := unconfigured.ConstructEvent([]byte(webhookPayload), "sha256=00"); !errors.Is(err, ErrConfig) {
		t.Errorf("an unconfigured verifier should wrap ErrConfig, got %v", err)
	}
}

func TestWebhookHandlerRejectsBadSignaturesWith401(t *testing.T) {
	verifier := NewWebhookVerifier(webhookSecret)

	var received *WebhookEvent
	handler := verifier.Handler(func(event *WebhookEvent) error {
		received = event
		return nil
	})

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	post := func(payload, signature string) int {
		req, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(payload))
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set(SignatureHeader, signature)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer resp.Body.Close()

		return resp.StatusCode
	}

	if status := post(webhookPayload, webhookSignature(webhookPayload, webhookSecret)); status != http.StatusOK {
		t.Errorf("a genuine delivery returned %d, want 200", status)
	}
	if received == nil || received.Reference != "PAB12CD3420260813" {
		t.Error("the handler did not receive the event")
	}

	received = nil
	if status := post(webhookPayload, "sha256=00"); status != http.StatusUnauthorized {
		t.Errorf("a bad signature returned %d, want 401", status)
	}
	if received != nil {
		t.Error("the handler ran despite an invalid signature")
	}
}
