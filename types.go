package epay

// Mode reports whether a key or event belongs to live traffic or the sandbox.
type Mode string

const (
	// ModeLive means real payments.
	ModeLive Mode = "live"
	// ModeSandbox means test payments that move no money.
	ModeSandbox Mode = "sandbox"
)

// TransactionStatus is a lifecycle state a transaction can be in.
type TransactionStatus string

const (
	StatusPending    TransactionStatus = "pending"
	StatusProcessing TransactionStatus = "processing"
	StatusCompleted  TransactionStatus = "completed"
	StatusFailed     TransactionStatus = "failed"
	StatusCancelled  TransactionStatus = "cancelled"
	StatusReversed   TransactionStatus = "reversed"
	StatusRefunding  TransactionStatus = "refunding"
	StatusRefunded   TransactionStatus = "refunded"
)

// AllStatuses lists every status the API recognises, for validation.
var AllStatuses = []TransactionStatus{
	StatusPending,
	StatusProcessing,
	StatusCompleted,
	StatusFailed,
	StatusCancelled,
	StatusReversed,
	StatusRefunding,
	StatusRefunded,
}

// CancellableStatuses lists the statuses that can still be cancelled.
var CancellableStatuses = []TransactionStatus{StatusPending, StatusProcessing}

// IsValid reports whether the status is one the API recognises.
func (s TransactionStatus) IsValid() bool {
	for _, candidate := range AllStatuses {
		if candidate == s {
			return true
		}
	}
	return false
}

// EventType is a webhook event name.
type EventType string

const (
	EventPaymentSuccess   EventType = "payment.success"
	EventPaymentFailed    EventType = "payment.failed"
	EventPaymentCancelled EventType = "payment.cancelled"
	EventPaymentRefunding EventType = "payment.refunding"
	EventPaymentRefunded  EventType = "payment.refunded"
	EventPaymentReversed  EventType = "payment.reversed"
)

// AllEventTypes lists every webhook event name this SDK version documents.
var AllEventTypes = []EventType{
	EventPaymentSuccess,
	EventPaymentFailed,
	EventPaymentCancelled,
	EventPaymentRefunding,
	EventPaymentRefunded,
	EventPaymentReversed,
}

// IsKnown reports whether the event name is one this SDK version documents.
func (e EventType) IsKnown() bool {
	for _, candidate := range AllEventTypes {
		if candidate == e {
			return true
		}
	}
	return false
}

// InitializeParams is the body of POST /v1/transactions/initialize.
//
// Amount must be a positive numeric string with at most 9 integer digits and 2
// decimal places; see [FormatAmount] and [AmountFromMinorUnits]. CurrencyCode
// and CustomerPhone are normalized for you.
type InitializeParams struct {
	// Amount as a numeric string, for example "250.00". Required.
	Amount string
	// CurrencyCode is an ISO 4217 code such as "ETB". Required.
	CurrencyCode string
	// CustomerPhone is an Ethiopian mobile number, such as "+251911234567" or
	// "0911234567". Required.
	CustomerPhone string
	// MerchantReference is your own unique reference, such as an order id.
	MerchantReference string
	// Email is the customer's email address.
	Email string
	// FirstName is the customer's first name.
	FirstName string
	// LastName is the customer's last name.
	LastName string
	// ReturnURL is where to send the customer after payment, success or failure.
	ReturnURL string
	// CallbackURL is a per-transaction webhook URL, overriding the dashboard one.
	CallbackURL string
	// IdempotencyKey, up to 255 characters. Resending the same key within the
	// session window returns the original response instead of creating a second
	// transaction.
	//
	// When empty the client generates a fresh key per call, which makes its
	// internal retries safe but does not deduplicate across separate calls.
	// Set your order id to get that guarantee.
	IdempotencyKey string
}

// InitializedPayment is the response of POST /v1/transactions/initialize.
type InitializedPayment struct {
	// Reference is the ePay reference, an 8-character code such as A3BX72KP.
	Reference string `json:"reference"`
	// CheckoutURL is where to redirect the customer to pay.
	CheckoutURL string `json:"checkoutUrl"`
	// Status is always "success" on a successful initialization.
	Status string `json:"status"`
	// ExpiresAt is when the checkout session expires, ISO 8601.
	ExpiresAt string `json:"expiresAt"`
}

// DirectPaymentParams is the body of POST /v1/transactions/direct.
//
// Amount, CurrencyCode, and CustomerPhone follow the same rules as
// [InitializeParams].
type DirectPaymentParams struct {
	// Amount as a numeric string, for example "250.00". Required.
	Amount string
	// CurrencyCode is an ISO 4217 code such as "ETB". Required.
	CurrencyCode string
	// CustomerPhone is the Ethiopian mobile number registered with the
	// provider. Required.
	CustomerPhone string
	// PSPCode is the provider to charge through, such as "TELEBIRR" or "AWASH".
	// Required. See [ProvidersService.ListForDirectPayment].
	PSPCode string
	// FlowCode overrides the provider's default flow, such as "USSD" or
	// "SMS_OTP".
	FlowCode string
	// MerchantReference is your own unique reference, such as an order id.
	MerchantReference string
	// CallbackURL is a per-transaction webhook URL, overriding the dashboard one.
	CallbackURL string
	// IdempotencyKey, up to 255 characters. When empty the client generates a
	// fresh key per call, which makes its internal retries safe but does not
	// deduplicate across separate calls. Set your order id so a double-submitted
	// form cannot charge twice.
	IdempotencyKey string
}

// DirectPayment is the response of POST /v1/transactions/direct.
type DirectPayment struct {
	// Reference is the ePay reference, an 8-character code such as A3BX72KP.
	Reference string `json:"reference"`
	// Status is always pending: the outcome arrives by webhook once the
	// customer authorizes on their phone.
	Status TransactionStatus `json:"status"`
	// ChargeAmount is the total charged to the customer, fees included.
	ChargeAmount string `json:"chargeAmount"`
	// FeeAmount is the fee deducted from the transaction.
	FeeAmount string `json:"feeAmount"`
	// Attempt is provider-specific session data whose shape varies by
	// provider. Inspect it only when building a custom authorization UI.
	Attempt map[string]any `json:"attempt"`
}

// SMSLinkParams is the body of POST /v1/transactions/sms-link.
type SMSLinkParams struct {
	// CustomerPhone is the Ethiopian mobile number to text the link to.
	// Required.
	CustomerPhone string
	// CurrencyCode is an ISO 4217 code such as "ETB". Required.
	CurrencyCode string
	// Amount is a fixed amount to pre-fill and sign into the link. Leave it
	// empty to let the customer enter the amount.
	Amount string
}

// SMSLink is the response of POST /v1/transactions/sms-link.
type SMSLink struct {
	// Message confirms the SMS was dispatched.
	Message string `json:"message"`
	// CheckoutURL is the signed link sent to the customer, valid for 1 hour.
	CheckoutURL string `json:"checkoutUrl"`
	// CustomerPhone is the number the SMS was sent to.
	CustomerPhone string `json:"customerPhone"`
	// Amount is the pre-filled amount, or nil when the customer chooses it.
	Amount       *string `json:"amount"`
	CurrencyCode string  `json:"currencyCode"`
}

// Transaction is a transaction as returned by retrieve and list.
type Transaction struct {
	Reference         string            `json:"reference"`
	MerchantReference *string           `json:"merchantReference"`
	Status            TransactionStatus `json:"status"`
	Amount            string            `json:"amount"`
	CurrencyCode      string            `json:"currencyCode"`
	PaidAt            *string           `json:"paidAt"`
	CreatedAt         string            `json:"createdAt"`
}

// Customer holds customer details on a verified receipt or webhook event.
type Customer struct {
	Name  *string `json:"name"`
	Email *string `json:"email"`
	Phone *string `json:"phone"`
}

// VerifiedPayment is the response of GET /v1/transactions/:reference/verify.
type VerifiedPayment struct {
	Reference         string            `json:"reference"`
	MerchantReference *string           `json:"merchantReference"`
	Status            TransactionStatus `json:"status"`
	Amount            string            `json:"amount"`
	ServiceFee        *string           `json:"serviceFee"`
	CurrencyCode      *string           `json:"currencyCode"`
	PaymentMethod     *string           `json:"paymentMethod"`
	Customer          Customer          `json:"customer"`
	PaidAt            string            `json:"paidAt"`
	CreatedAt         string            `json:"createdAt"`
}

// Receipt is the response of GET /v1/transactions/:reference/receipt.
type Receipt struct {
	Reference         string  `json:"reference"`
	MerchantReference *string `json:"merchantReference"`
	// Status is terminal: completed, failed, or cancelled.
	Status TransactionStatus `json:"status"`
	Amount string            `json:"amount"`
	// ChargeAmount is the total charged to the customer, fees included.
	ChargeAmount  string   `json:"chargeAmount"`
	FeeAmount     *string  `json:"feeAmount"`
	CurrencyCode  *string  `json:"currencyCode"`
	PaymentMethod *string  `json:"paymentMethod"`
	Customer      Customer `json:"customer"`
	// PaidAt is nil for failed and cancelled transactions.
	PaidAt    *string `json:"paidAt"`
	CreatedAt string  `json:"createdAt"`
}

// ListParams are the query filters for GET /v1/transactions.
//
// When both From and To are set, the API rejects ranges longer than 90 days.
type ListParams struct {
	// From filters to transactions created on or after this date, "YYYY-MM-DD".
	From string
	// To filters to transactions created on or before this date, "YYYY-MM-DD".
	To string
	// Currency is a 3-letter ISO 4217 code, case-insensitive.
	Currency string
	// Status restricts the result to a single status.
	Status TransactionStatus
	// Cursor is the NextCursor from a previous page. Leave empty for page one.
	Cursor string
}

// transactionListResponse is the raw body of GET /v1/transactions.
type transactionListResponse struct {
	Data       []Transaction `json:"data"`
	NextCursor *string       `json:"nextCursor"`
	HasMore    bool          `json:"hasMore"`
}

// TimelineEvent is a single entry in a transaction timeline.
type TimelineEvent struct {
	// EventType is what happened, such as "session_created" or "payment_failed".
	EventType string `json:"eventType"`
	// OccurredAt is when the event occurred provider-side, ISO 8601.
	OccurredAt string `json:"occurredAt"`
	// ErrorCode is the provider error code, set only on failure events.
	ErrorCode *string `json:"errorCode"`
	// ErrorMessage describes the error, set only on failure events.
	ErrorMessage *string `json:"errorMessage"`
}

// Timeline is the response of GET /v1/transactions/:reference/timeline.
type Timeline struct {
	Reference string `json:"reference"`
	// Events is chronological, earliest first, and empty until the first
	// provider event arrives.
	Events []TimelineEvent `json:"events"`
}

// ProviderItem is a compact entry from GET /v1/payment-providers/list.
type ProviderItem struct {
	// ProviderCode identifies the provider, such as "cbe_birr" or "telebirr".
	ProviderCode string `json:"providerCode"`
	// ProviderName is the display name.
	ProviderName *string `json:"providerName"`
	// FlowCode is the payment flow, such as "mobile_banking", "ussd", or "qr".
	FlowCode string `json:"flowCode"`
}

// ProviderFlow is a payment flow a provider supports.
type ProviderFlow struct {
	// FlowCode identifies the flow, such as "USSD", "SMS_OTP", or "IN_APP".
	// Pass it as [DirectPaymentParams.FlowCode].
	FlowCode string `json:"flowCode"`
	// IsDefault marks the provider's recommended flow.
	IsDefault bool `json:"isDefault"`
	// SupportsDirectPayment reports whether [PaymentsService.DirectPayment]
	// accepts this flow.
	SupportsDirectPayment bool `json:"supportsDirectPayment"`
}

// Provider is an entry from GET /v1/payment-providers, with its flows grouped.
type Provider struct {
	// ProviderCode identifies the provider, such as "TELEBIRR". Pass it as
	// [DirectPaymentParams.PSPCode].
	ProviderCode string  `json:"providerCode"`
	ProviderName *string `json:"providerName"`
	// ProviderCategory is commonly "wallet" or "bank".
	ProviderCategory *string        `json:"providerCategory"`
	Flows            []ProviderFlow `json:"flows"`
}

// DefaultFlow returns the provider's recommended flow, if it marks one.
func (p Provider) DefaultFlow() (ProviderFlow, bool) {
	for _, flow := range p.Flows {
		if flow.IsDefault {
			return flow, true
		}
	}
	return ProviderFlow{}, false
}

// providerEnvelope is the {"data": [...]} wrapper both provider endpoints use.
type providerEnvelope[T any] struct {
	Data []T `json:"data"`
}

// WebhookEvent is the payload delivered to your webhook endpoint.
//
// Note the currency field is Currency here, not CurrencyCode as on the
// transaction endpoints.
type WebhookEvent struct {
	Event             EventType         `json:"event"`
	Mode              Mode              `json:"mode"`
	Reference         string            `json:"reference"`
	MerchantReference *string           `json:"merchantReference"`
	Amount            *string           `json:"amount"`
	ServiceFee        *string           `json:"serviceFee"`
	Currency          *string           `json:"currency"`
	Status            TransactionStatus `json:"status"`
	PaymentMethod     *string           `json:"paymentMethod"`
	Customer          Customer          `json:"customer"`
	// PaidAt is nil for every non-success event.
	PaidAt    *string `json:"paidAt"`
	CreatedAt string  `json:"createdAt"`
	// CustomerPhone is set on direct payment events.
	CustomerPhone *string `json:"customerPhone"`
	// PSPCode is the provider charged, set on direct payment events.
	PSPCode *string `json:"pspCode"`
	// OccurredAt is when the event occurred, ISO 8601.
	OccurredAt *string `json:"occurredAt"`
	// Reason explains a payment.failed event.
	Reason *string `json:"reason"`
}
