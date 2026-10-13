package epay

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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

	key, err := resolveIdempotencyKey(params.IdempotencyKey)
	if err != nil {
		return nil, err
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

// directPaymentBody is the wire form of [DirectPaymentParams].
type directPaymentBody struct {
	Amount            string `json:"amount"`
	CurrencyCode      string `json:"currencyCode"`
	CustomerPhone     string `json:"customerPhone"`
	PSPCode           string `json:"pspCode"`
	FlowCode          string `json:"flowCode,omitempty"`
	MerchantReference string `json:"merchantReference,omitempty"`
	CallbackURL       string `json:"callbackUrl,omitempty"`
}

// DirectPayment charges a customer's wallet or bank account directly, with no
// checkout page.
//
// ePay pushes an authorization prompt (USSD menu, SMS OTP, or in-app
// notification) to the customer's phone. The call returns as soon as the prompt
// is sent, with Status pending; the outcome arrives as a payment.success or
// payment.failed webhook once the customer acts.
//
// Choose PSPCode and FlowCode from [ProvidersService.ListForDirectPayment].
//
// The call is retried on transient failures; every attempt carries the same
// idempotency key, so a retry cannot double-charge.
func (s *PaymentsService) DirectPayment(
	ctx context.Context,
	params DirectPaymentParams,
) (*DirectPayment, error) {
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
	pspCode := strings.TrimSpace(params.PSPCode)
	if pspCode == "" {
		return nil, fmt.Errorf("%w: PSPCode must not be empty", ErrValidation)
	}
	key, err := resolveIdempotencyKey(params.IdempotencyKey)
	if err != nil {
		return nil, err
	}

	body := directPaymentBody{
		Amount:            amount,
		CurrencyCode:      currency,
		CustomerPhone:     phone,
		PSPCode:           pspCode,
		FlowCode:          strings.TrimSpace(params.FlowCode),
		MerchantReference: params.MerchantReference,
		CallbackURL:       params.CallbackURL,
	}

	var charge DirectPayment
	err = s.client.do(ctx, request{
		method:    http.MethodPost,
		path:      "/transactions/direct",
		body:      body,
		header:    map[string]string{"x-idempotency-key": key},
		retryable: boolPtr(true),
	}, &charge)
	if err != nil {
		return nil, err
	}

	return &charge, nil
}

// smsLinkBody is the wire form of [SMSLinkParams].
type smsLinkBody struct {
	CustomerPhone string `json:"customerPhone"`
	CurrencyCode  string `json:"currencyCode"`
	Amount        string `json:"amount,omitempty"`
}

// SendSMSLink texts the customer a signed link to the hosted checkout page.
//
// With an Amount, the amount is signed into the link and cannot be changed;
// without one, the customer enters it. The link is valid for 1 hour.
//
// The call is never retried automatically: the endpoint takes no idempotency
// key, so a retry after a lost response would text the customer a second time.
func (s *PaymentsService) SendSMSLink(ctx context.Context, params SMSLinkParams) (*SMSLink, error) {
	phone, err := NormalizePhone(params.CustomerPhone)
	if err != nil {
		return nil, err
	}
	currency, err := NormalizeCurrency(params.CurrencyCode)
	if err != nil {
		return nil, err
	}

	body := smsLinkBody{CustomerPhone: phone, CurrencyCode: currency}
	if params.Amount != "" {
		if body.Amount, err = FormatAmount(params.Amount); err != nil {
			return nil, err
		}
	}

	var link SMSLink
	err = s.client.do(ctx, request{
		method:    http.MethodPost,
		path:      "/transactions/sms-link",
		body:      body,
		retryable: boolPtr(false),
	}, &link)
	if err != nil {
		return nil, err
	}

	return &link, nil
}

// Receipt returns the receipt for a transaction in any terminal state:
// completed, failed, or cancelled.
//
// Use it for records, audits, and customer-facing receipts. To confirm a
// payment before fulfilling an order, use [PaymentsService.Verify], which
// accepts only completed transactions. A transaction that is still pending or
// processing returns an error wrapping [ErrBadRequest].
func (s *PaymentsService) Receipt(ctx context.Context, reference string) (*Receipt, error) {
	if err := validateReference(reference); err != nil {
		return nil, err
	}

	var receipt Receipt
	err := s.client.do(ctx, request{
		method: http.MethodGet,
		path:   "/transactions/" + url.PathEscape(reference) + "/receipt",
	}, &receipt)
	if err != nil {
		return nil, err
	}

	return &receipt, nil
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

// resolveIdempotencyKey uses the caller's key, or generates one for this call.
func resolveIdempotencyKey(key string) (string, error) {
	if key == "" {
		key = newIdempotencyKey()
	}
	if len(key) > 255 {
		return "", fmt.Errorf(
			"%w: IdempotencyKey must be at most 255 characters, got %d", ErrValidation, len(key))
	}
	return key, nil
}

func validateReference(reference string) error {
	if reference == "" {
		return fmt.Errorf("%w: reference must not be empty", ErrValidation)
	}
	return nil
}
