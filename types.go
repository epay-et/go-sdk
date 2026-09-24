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
	// Reference is the ePay reference, formatted P{8-hex}{yyyymmdd}.
	Reference string `json:"reference"`
	// CheckoutURL is where to redirect the customer to pay.
	CheckoutURL string `json:"checkoutUrl"`
	// Status is always "success" on a successful initialization.
	Status string `json:"status"`
	// ExpiresAt is when the checkout session expires, ISO 8601.
	ExpiresAt string `json:"expiresAt"`
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

// Provider is a full entry from GET /v1/payment-providers.
type Provider struct {
	ProviderCode string  `json:"providerCode"`
	ProviderName *string `json:"providerName"`
	// ProviderCategory is commonly "bank" or "mobile_money".
	ProviderCategory *string `json:"providerCategory"`
	FlowCode         string  `json:"flowCode"`
	// IsEnabled reports whether your account has this provider enabled.
	// Disabled providers are still returned so your UI can show them as
	// unavailable rather than having them silently disappear.
	IsEnabled bool `json:"isEnabled"`
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
}
