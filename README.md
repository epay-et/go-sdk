# go-sdk

Official ePay Business API client for Go.

Accept payments from every major Ethiopian mobile wallet and bank with one integration.

Full API documentation: <https://docs.epayethiopia.com/>

- **Zero dependencies** — standard library only
- **Context-aware** on every call, with per-attempt timeouts
- **Automatic retries** with exponential backoff and jitter on `429`/`5xx`/network errors
- **Safe retries on initialize** — every attempt reuses one idempotency key
- **Idiomatic errors** via `errors.Is` sentinels and a single `*APIError`
- **Exact amounts** as strings and integer minor units, never `float64`
- **Constant-time webhook verification**, plus a ready-made `http.Handler`

## Install

```sh
go get github.com/epay-et/go-sdk
```

Requires Go 1.22 or newer.

The import path ends in `go-sdk` but the package is named `epay`, so the
examples below alias it for clarity. Plain `import "github.com/epay-et/go-sdk"`
works identically — Go takes the name from the package clause, not the path.

## Quick start

```go
package main

import (
	"context"
	"log"

	epay "github.com/epay-et/go-sdk"
)

func main() {
	client, err := epay.NewClient() // reads EPAY_SECRET_KEY
	if err != nil {
		log.Fatal(err)
	}

	session, err := client.Payments.Initialize(context.Background(), epay.InitializeParams{
		Amount:            "250.00",
		CurrencyCode:      "ETB",
		CustomerPhone:     "+251911234567",
		MerchantReference: "order_123",
		ReturnURL:         "https://yourstore.com/orders/123/complete",
		IdempotencyKey:    "order_123",
	})
	if err != nil {
		log.Fatal(err)
	}

	// Save session.Reference, then send the customer to session.CheckoutURL.
	log.Println(session.CheckoutURL)
}
```

The key prefix picks the environment: `sk_test_…` runs against the sandbox and
`sk_live_…` moves real money. There is no separate mode to configure.

## Configuration

Options fall back to environment variables, so `epay.NewClient()` is often enough.

| Option | Environment variable | Default |
| --- | --- | --- |
| `WithAPIKey` | `EPAY_SECRET_KEY` | *required* |
| `WithWebhookSecret` | `EPAY_WEBHOOK_SECRET` | — |
| `WithBaseURL` | `EPAY_BASE_URL` | `https://api.epayethiopia.com/v1` |
| `WithTimeout` | — | `30s` per attempt |
| `WithMaxRetries` | — | `2` |
| `WithHTTPClient` | — | `&http.Client{}` |
| `WithHeader` | — | — |

```go
client, err := epay.NewClient(
	epay.WithAPIKey(os.Getenv("EPAY_SECRET_KEY")),
	epay.WithTimeout(10*time.Second),
	epay.WithMaxRetries(3),
)

client.Mode()       // epay.ModeLive | epay.ModeSandbox | ""
client.IsSandbox()  // bool
```

`client.String()` masks the key, so a client is safe to log.

`WithTimeout` applies per attempt. Setting `Timeout` on a custom `*http.Client`
instead bounds the whole call including retries, which is usually not what you
want — prefer `WithTimeout` and leave the shared client's `Timeout` at zero.

## Payments

### Initialize

`Amount`, `CurrencyCode`, and `CustomerPhone` are normalized before the request
is sent: amounts are padded to two decimal places, currencies uppercased, and
phone numbers rewritten to `+251XXXXXXXXX`. A malformed value returns an error
wrapping `epay.ErrValidation` without spending a request.

**On amounts.** Go has no decimal type in the standard library and `float64`
cannot represent every decimal amount exactly, so amounts are strings. Helpers
keep that exact:

```go
epay.FormatAmount("250.5")          // "250.50", error
epay.AmountFromMinorUnits(25000)    // "250.00", error
epay.MinorUnits("250.00")           // int64(25000), error
```

**On idempotency keys.** If you leave `IdempotencyKey` empty, the client
generates a fresh one per call. That makes its internal retries safe — a retried
`Initialize` cannot double-charge — but it does *not* deduplicate across separate
calls. Set your own order id to get that guarantee.

### Verify before fulfilling

```go
receipt, err := client.Payments.Verify(ctx, reference)
// receipt.Status == epay.StatusCompleted, plus ServiceFee, PaymentMethod, Customer
```

The API rejects a transaction that is not yet `completed` with
`epay.ErrBadRequest`, so call `Transactions.Retrieve` first if you would rather
branch on status than handle an error.

### Cancel

```go
err := client.Payments.Cancel(ctx, reference)
```

Only `pending` and `processing` transactions can be cancelled, and cancellation
is irreversible. This call is never retried automatically, because the endpoint
takes no idempotency key.

## Transactions

```go
transaction, err := client.Transactions.Retrieve(ctx, reference)
timeline, err := client.Transactions.Timeline(ctx, reference)

for _, event := range timeline.Events {
	log.Println(event.EventType, event.OccurredAt)
}
```

Nullable API fields are pointers, so "absent" is distinguishable from "empty":

```go
if transaction.PaidAt != nil {
	log.Println("paid at", *transaction.PaidAt)
}
```

### Listing and pagination

The endpoint is cursor-paginated at a fixed 10 per page. `Iterate` walks every
page for you, fetching lazily and carrying your filters along:

```go
it := client.Transactions.Iterate(epay.ListParams{
	Status:   epay.StatusCompleted,
	Currency: "ETB",
	From:     "2026-08-01",
	To:       "2026-08-31",
})

for it.Next(ctx) {
	transaction := it.Transaction()
	log.Println(transaction.Reference, transaction.Amount)
}
if err := it.Err(); err != nil {
	return err
}
```

The API caps the range at 90 days.

Cap the walk, or drive the cursor yourself:

```go
recent, err := client.Transactions.Iterate(epay.ListParams{}).Collect(ctx, 50)

page, err := client.Transactions.List(ctx, epay.ListParams{})
page.Transactions  // exactly this page
page.HasMore       // bool
next, err := page.Next(ctx) // nil when this was the last page
```

## Payment providers

```go
items, err := client.Providers.List(ctx)        // compact, for dropdowns
all, err := client.Providers.GetAll(ctx)        // with category and IsEnabled
enabled, err := client.Providers.ListEnabled(ctx) // only what you can route to
```

Both endpoints are mode-aware and permission-gated: `List` needs
`list_platform_payment_provider`, `GetAll` needs `get_platform_payment_provider`.

## Webhooks

Verify the `X-Epay-Signature` header against the **raw** request body before you
trust a payload. Re-marshalling a decoded struct can reorder keys and change the
digest, which rejects valid deliveries.

The simplest form is a ready-made handler:

```go
verifier := epay.NewWebhookVerifier(os.Getenv("EPAY_WEBHOOK_SECRET"))

mux.Handle("/webhooks/epay", verifier.Handler(func(event *epay.WebhookEvent) error {
	return queue.Enqueue(event) // acknowledge fast, process later
}))
```

It replies `401` on a bad or missing signature, `400` on an unreadable body, and
`500` if your callback returns an error.

For more control, verify inside your own handler:

```go
func handleWebhook(w http.ResponseWriter, r *http.Request) {
	event, err := verifier.ConstructEventFromRequest(r, 0) // 0 = 1 MiB cap
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, epay.ErrWebhookSignature) {
			status = http.StatusUnauthorized
		}
		http.Error(w, "rejected", status)
		return
	}

	switch event.Event {
	case epay.EventPaymentSuccess:
		fulfil(event.Reference)
	case epay.EventPaymentFailed, epay.EventPaymentCancelled:
		release(event.Reference)
	case epay.EventPaymentRefunding, epay.EventPaymentRefunded, epay.EventPaymentReversed:
		reconcile(event)
	default:
		// An event this SDK version does not know yet.
	}

	w.WriteHeader(http.StatusOK)
}
```

`ConstructEvent` and `ConstructEventFromRequest` fail closed: any event they
return had a valid signature.

ePay retries anything that is not a `2xx` within 10 seconds, up to 5 attempts,
so keep the handler fast and make it idempotent — deduplicate on
`event.Reference`.

## Error handling

Failures return an `*APIError` that reports itself as the sentinel matching its
status code, so `errors.Is` does the branching:

```go
receipt, err := client.Payments.Verify(ctx, reference)

switch {
case errors.Is(err, epay.ErrNotFound):
	return nil, nil
case errors.Is(err, epay.ErrBadRequest):
	return nil, errNotSettledYet
case errors.Is(err, epay.ErrRateLimit):
	var apiErr *epay.APIError
	errors.As(err, &apiErr)
	time.Sleep(apiErr.RetryAfter())
	return nil, err
case err != nil:
	var apiErr *epay.APIError
	if errors.As(err, &apiErr) {
		log.Printf("ePay %d on %s: %s", apiErr.Status, apiErr.Request, apiErr.Body)
	}
	return nil, err
}
```

| Sentinel | Returned when |
| --- | --- |
| `ErrValidation` | A value failed local validation; no request was sent |
| `ErrConfig` | The client was constructed with unusable options |
| `ErrBadRequest` | `400` |
| `ErrAuthentication` | `401` — key missing, invalid, or revoked |
| `ErrPermissionDenied` | `403` — IP not whitelisted, or key lacks a permission |
| `ErrNotFound` | `404` |
| `ErrConflict` | `409` |
| `ErrUnprocessableEntity` | `422` |
| `ErrRateLimit` | `429` — use `(*APIError).RetryAfter()` |
| `ErrServer` | `5xx` |
| `ErrTimeout` | The attempt exceeded the configured timeout |
| `ErrConnection` | No response was received at all |
| `ErrWebhookSignature` | A webhook signature was missing or wrong |

A cancelled or expired caller context is returned as `context.Canceled` or
`context.DeadlineExceeded`, unwrapped, so it is never mistaken for an SDK
timeout.

`429`, `5xx`, and network errors are retried automatically before surfacing.

## Sandbox testing

Use a `sk_test_…` key with the documented magic phone numbers:

| Phone | OTP | Outcome |
| --- | --- | --- |
| `251900000000` | `000111` | Generic sandbox account |
| `251900000001` | `123456` | Completes, fires `payment.success` |
| `251900000002` | `654321` | `INVALID_OTP` |
| `251900000003` | `111111` | `OTP_EXPIRED` |
| `251900000004` | — | Declined, fires `payment.failed` |

Generate a fresh idempotency key per test run: reusing one returns the cached
response instead of triggering the scenario again.

### Testing your own code

Point the client at an `httptest.Server` and no request leaves the process:

```go
server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"reference":"PAB1","checkoutUrl":"https://…"}`))
}))
defer server.Close()

client, _ := epay.NewClient(
	epay.WithAPIKey("sk_test_fake"),
	epay.WithBaseURL(server.URL),
	epay.WithMaxRetries(0),
)
```

See `client_test.go` for a scripted multi-response server.

## Unmodelled endpoints

`client.Do` reaches anything this version does not wrap yet, with the same auth,
timeout, retry, and error handling:

```go
var out struct {
	OK bool `json:"ok"`
}
err := client.Do(ctx, http.MethodGet, "/some/new/endpoint", url.Values{"limit": {"10"}}, nil, &out)
```

## Development

```sh
go test ./...
go vet ./...
gofmt -l .
```

## Releasing

CI runs `go test -race` on Go 1.22, 1.23 and stable, plus `go vet`, a `gofmt`
check, a `go mod tidy` no-op check, and a coverage summary.

There is **no registry account and nothing to upload**. A Go module version is
simply a git tag:

```sh
git tag v0.2.0 && git push origin v0.2.0
```

`release.yml` then verifies the whole suite, checks that the module path in
`go.mod` matches the repository it is running in, asks proxy.golang.org to fetch
the new version so the first `go get` is warm, and opens a GitHub release with
generated notes.

Two constraints worth remembering:

- **The module path must equal the repository URL.** `go.mod` declares
  `github.com/epay-et/go-sdk`, so this must be the root of that repository.
- **Tags are immutable once the proxy caches them.** Retracting a version means
  publishing a new one; plan on `v0.x` until the surface settles.

Remember to bump `Version` in `client.go` alongside the tag — it is reported in
the `User-Agent`.

## License

MIT
