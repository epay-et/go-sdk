package epay

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// SignatureHeader is the header ePay signs every delivery with.
const SignatureHeader = "X-Epay-Signature"

const signaturePrefix = "sha256="

// WebhookVerifier verifies webhook signatures and parses events.
//
// Reach it through [Client.Webhooks], or build a standalone one with
// [NewWebhookVerifier] when your webhook receiver does not need an API client.
type WebhookVerifier struct {
	secret string
}

// NewWebhookVerifier builds a verifier for a signing secret.
func NewWebhookVerifier(secret string) *WebhookVerifier {
	return &WebhookVerifier{secret: secret}
}

// IsConfigured reports whether a signing secret is set.
func (w *WebhookVerifier) IsConfigured() bool { return w.secret != "" }

// Sign computes the expected signature for a payload.
//
// It is useful for generating fixtures in your own tests; ePay does the same
// computation over the raw bytes it sends.
func (w *WebhookVerifier) Sign(payload []byte) (string, error) {
	if w.secret == "" {
		return "", fmt.Errorf("%w: no webhook secret configured", ErrConfig)
	}

	mac := hmac.New(sha256.New, []byte(w.secret))
	mac.Write(payload)

	return hex.EncodeToString(mac.Sum(nil)), nil
}

// Verify reports whether an X-Epay-Signature header matches the raw request
// body.
//
// Pass the raw body, never a re-marshalled struct: re-encoding may reorder keys
// or change whitespace, which changes the digest and rejects valid deliveries.
// The comparison is constant-time.
func (w *WebhookVerifier) Verify(payload []byte, signatureHeader string) bool {
	received, ok := normalizeSignature(signatureHeader)
	if !ok {
		return false
	}

	expected, err := w.Sign(payload)
	if err != nil {
		return false
	}

	return hmac.Equal([]byte(received), []byte(expected))
}

// ConstructEvent verifies a delivery and returns the parsed event.
//
// This is the entry point to use in a webhook handler: it fails closed, so any
// event it returns had a valid signature. The returned error wraps
// [ErrWebhookSignature] on a bad or missing signature, and [ErrValidation] when
// the verified body is not a JSON object.
func (w *WebhookVerifier) ConstructEvent(payload []byte, signatureHeader string) (*WebhookEvent, error) {
	if w.secret == "" {
		return nil, fmt.Errorf(
			"%w: no webhook secret configured, pass epay.WithWebhookSecret or set "+
				"EPAY_WEBHOOK_SECRET", ErrConfig)
	}
	if _, ok := normalizeSignature(signatureHeader); !ok {
		return nil, fmt.Errorf(
			"%w: missing or malformed %s header, expected %q followed by hex",
			ErrWebhookSignature, SignatureHeader, signaturePrefix)
	}
	if !w.Verify(payload, signatureHeader) {
		return nil, fmt.Errorf(
			"%w: %s does not match the request body; verify you are using the raw request "+
				"body and the webhook secret from Developers -> Webhooks",
			ErrWebhookSignature, SignatureHeader)
	}

	var event WebhookEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, fmt.Errorf(
			"%w: webhook body passed verification but is not a JSON object: %v", ErrValidation, err)
	}

	return &event, nil
}

// ConstructEventFromRequest reads and verifies an incoming HTTP request.
//
// It consumes r.Body, so call it before any other reader. maxBytes caps the
// body size; pass 0 for the 1 MiB default, which is far above any real payload.
//
//	func handler(w http.ResponseWriter, r *http.Request) {
//		event, err := verifier.ConstructEventFromRequest(r, 0)
//		if err != nil {
//			http.Error(w, "invalid signature", http.StatusUnauthorized)
//			return
//		}
//
//		queue.Enqueue(event)          // acknowledge fast, process later
//		w.WriteHeader(http.StatusOK)
//	}
func (w *WebhookVerifier) ConstructEventFromRequest(r *http.Request, maxBytes int64) (*WebhookEvent, error) {
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}

	payload, err := io.ReadAll(io.LimitReader(r.Body, maxBytes))
	if err != nil {
		return nil, fmt.Errorf("%w: could not read webhook body: %v", ErrValidation, err)
	}

	return w.ConstructEvent(payload, r.Header.Get(SignatureHeader))
}

// Handler wraps an event handler with signature verification, returning 401 on
// a bad or missing signature and 400 on an unreadable body.
//
// onEvent should return quickly: ePay treats a response slower than 10 seconds
// as a failure and retries, up to 5 attempts. Enqueue the work rather than
// doing it inline, and deduplicate on Reference, since a retry can deliver the
// same event twice.
//
//	mux.Handle("/webhooks/epay", verifier.Handler(func(event *epay.WebhookEvent) error {
//		return queue.Enqueue(event)
//	}))
func (w *WebhookVerifier) Handler(onEvent func(*WebhookEvent) error) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		event, err := w.ConstructEventFromRequest(r, 0)
		if err != nil {
			status := http.StatusBadRequest
			if isWebhookSignatureError(err) {
				status = http.StatusUnauthorized
			}
			http.Error(rw, err.Error(), status)
			return
		}

		if err := onEvent(event); err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}

		rw.WriteHeader(http.StatusOK)
	})
}

func isWebhookSignatureError(err error) bool {
	return errors.Is(err, ErrWebhookSignature)
}

// normalizeSignature reduces a header value to a bare lowercase hex digest.
func normalizeSignature(header string) (string, bool) {
	trimmed := strings.TrimSpace(header)

	if len(trimmed) >= len(signaturePrefix) &&
		strings.EqualFold(trimmed[:len(signaturePrefix)], signaturePrefix) {
		trimmed = trimmed[len(signaturePrefix):]
	}

	if trimmed == "" {
		return "", false
	}

	// hex.DecodeString rejects odd lengths and non-hex characters, which is
	// exactly the validation needed here.
	if _, err := hex.DecodeString(trimmed); err != nil {
		return "", false
	}

	return strings.ToLower(trimmed), true
}
