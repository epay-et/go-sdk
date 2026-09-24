package epay

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// PaymentsService covers the payment lifecycle.
type PaymentsService struct {
	client *Client
}

// initializeBody is the wire form of [InitializeParams]. Optional fields are
// omitted rather than sent as null, so the API applies its own defaults.
type initializeBody struct {
	Amount            string `json:"amount"`
	CurrencyCode      string `json:"currencyCode"`
	CustomerPhone     string `json:"customerPhone"`
	MerchantReference string `json:"merchantReference,omitempty"`
	Email             string `json:"email,omitempty"`
	FirstName         string `json:"firstName,omitempty"`
	LastName          string `json:"lastName,omitempty"`
	ReturnURL         string `json:"returnUrl,omitempty"`
	CallbackURL       string `json:"callbackUrl,omitempty"`
}

// Initialize creates a payment session and returns a hosted checkout URL.
//
// Amount, CurrencyCode, and CustomerPhone are normalized before the request is
// sent: amounts are padded to two decimal places, currencies uppercased, and
// phone numbers rewritten to +251XXXXXXXXX. A malformed value returns an error
// wrapping [ErrValidation] without spending a request.
//
// Redirect the customer to CheckoutURL and store Reference to reconcile the
// payment later.
//
// The call is retried on transient failures; every attempt carries the same
// idempotency key, so a retry cannot double-charge.
func (s *PaymentsService) Initialize(
	ctx context.Context,
	params InitializeParams,
) (*InitializedPayment, error) {
	amount, err := FormatAmount(params.Amount)
	if err != nil {
		return nil, err
	}
	currency, err := NormalizeCurrency(params.CurrencyCode)
	if err != nil {
		return nil, err
	}
	phone, err := NormalizePhone(params.CustomerPhone)
	if err != nil {
		return nil, err
	}

	key := params.IdempotencyKey
	if key == "" {
		key = newIdempotencyKey()
	}
	if len(key) > 255 {
		return nil, fmt.Errorf(
			"%w: IdempotencyKey must be at most 255 characters, got %d", ErrValidation, len(key))
	}

	body := initializeBody{
		Amount:            amount,
		CurrencyCode:      currency,
		CustomerPhone:     phone,
		MerchantReference: params.MerchantReference,
		Email:             params.Email,
		FirstName:         params.FirstName,
		LastName:          params.LastName,
		ReturnURL:         params.ReturnURL,
		CallbackURL:       params.CallbackURL,
	}

	var session InitializedPayment
	err = s.client.do(ctx, request{
		method:    http.MethodPost,
		path:      "/transactions/initialize",
		body:      body,
		header:    map[string]string{"x-idempotency-key": key},
		retryable: boolPtr(true),
	}, &session)
	if err != nil {
		return nil, err
	}

	return &session, nil
}

// Verify returns the full receipt for a completed transaction.
//
// Call it before fulfilling an order. The API rejects any transaction that is
// not yet completed with [ErrBadRequest], so use
// [TransactionsService.Retrieve] first if you would rather branch on status.
func (s *PaymentsService) Verify(ctx context.Context, reference string) (*VerifiedPayment, error) {
	if err := validateReference(reference); err != nil {
		return nil, err
	}

	var receipt VerifiedPayment
	err := s.client.do(ctx, request{
		method: http.MethodGet,
		path:   "/transactions/" + url.PathEscape(reference) + "/verify",
	}, &receipt)
	if err != nil {
		return nil, err
	}

	return &receipt, nil
}

// Cancel cancels a pending or processing transaction and fires a
// payment.cancelled webhook.
//
// Cancellation is irreversible: the checkout session is invalidated and the
// customer can no longer pay. The call is never retried automatically, because
// the endpoint takes no idempotency key.
func (s *PaymentsService) Cancel(ctx context.Context, reference string) error {
	if err := validateReference(reference); err != nil {
		return err
	}

	return s.client.do(ctx, request{
		method:    http.MethodPost,
		path:      "/transactions/" + url.PathEscape(reference) + "/cancel",
		retryable: boolPtr(false),
	}, nil)
}

func validateReference(reference string) error {
	if reference == "" {
		return fmt.Errorf("%w: reference must not be empty", ErrValidation)
	}
	return nil
}
