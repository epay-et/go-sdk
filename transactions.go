package epay

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// PageSize is the fixed page size of GET /v1/transactions.
const PageSize = 10

// TransactionsService covers transaction history.
type TransactionsService struct {
	client *Client
}

// TransactionPage is one page of transactions, plus the means to walk the rest.
type TransactionPage struct {
	// Transactions holds up to [PageSize] transactions, newest first.
	Transactions []Transaction
	// NextCursor is the cursor for the next page, empty on the last page.
	NextCursor string
	// HasMore reports whether more transactions exist beyond this page.
	HasMore bool

	service *TransactionsService
	params  ListParams
}

// Next fetches the following page, or returns nil when this is the last one.
func (p *TransactionPage) Next(ctx context.Context) (*TransactionPage, error) {
	if !p.HasMore || p.NextCursor == "" {
		return nil, nil
	}

	params := p.params
	params.Cursor = p.NextCursor

	return p.service.List(ctx, params)
}

// List returns a page of transactions, newest first, [PageSize] per page.
//
// Filters persist across pages. When both From and To are set the API rejects
// ranges longer than [MaxListRangeDays] days.
//
// Use [TransactionsService.Iterate] to walk every page without handling cursors.
func (s *TransactionsService) List(ctx context.Context, params ListParams) (*TransactionPage, error) {
	query := url.Values{}

	if params.From != "" {
		query.Set("from", params.From)
	}
	if params.To != "" {
		query.Set("to", params.To)
	}
	if params.From != "" && params.To != "" && params.From > params.To {
		return nil, fmt.Errorf(
			"%w: From (%s) must not be later than To (%s)", ErrValidation, params.From, params.To)
	}

	if params.Currency != "" {
		currency, err := NormalizeCurrency(params.Currency)
		if err != nil {
			return nil, err
		}
		query.Set("currency", currency)
	}

	if params.Status != "" {
		if !params.Status.IsValid() {
			return nil, fmt.Errorf(
				"%w: Status %q is not a recognised transaction status", ErrValidation, params.Status)
		}
		query.Set("status", string(params.Status))
	}

	if params.Cursor != "" {
		query.Set("cursor", params.Cursor)
	}

	var response transactionListResponse
	if err := s.client.do(ctx, request{
		method: http.MethodGet,
		path:   "/transactions",
		query:  query,
	}, &response); err != nil {
		return nil, err
	}

	page := &TransactionPage{
		Transactions: response.Data,
		HasMore:      response.HasMore,
		service:      s,
		params:       params,
	}
	if response.NextCursor != nil {
		page.NextCursor = *response.NextCursor
	}

	return page, nil
}

// TransactionIterator walks every transaction matching a filter, fetching pages
// on demand.
//
//	it := client.Transactions.Iterate(epay.ListParams{Status: epay.StatusCompleted})
//	for it.Next(ctx) {
//		fmt.Println(it.Transaction().Reference)
//	}
//	if err := it.Err(); err != nil {
//		return err
//	}
type TransactionIterator struct {
	service *TransactionsService
	params  ListParams

	page    *TransactionPage
	index   int
	current Transaction
	started bool
	done    bool
	err     error
}

// Iterate returns an iterator over every transaction matching params.
func (s *TransactionsService) Iterate(params ListParams) *TransactionIterator {
	return &TransactionIterator{service: s, params: params}
}

// Next advances to the next transaction, fetching another page when needed. It
// returns false when the results are exhausted or a request failed; check
// [TransactionIterator.Err] to tell those apart.
func (it *TransactionIterator) Next(ctx context.Context) bool {
	if it.done || it.err != nil {
		return false
	}

	for {
		if it.page != nil && it.index < len(it.page.Transactions) {
			it.current = it.page.Transactions[it.index]
			it.index++
			return true
		}

		var (
			next *TransactionPage
			err  error
		)

		switch {
		case !it.started:
			it.started = true
			next, err = it.service.List(ctx, it.params)
		default:
			next, err = it.page.Next(ctx)
		}

		if err != nil {
			it.err = err
			return false
		}
		if next == nil {
			it.done = true
			return false
		}

		it.page = next
		it.index = 0

		// An empty page with HasMore set would otherwise spin; the loop
		// re-enters and asks for the next one.
		if len(next.Transactions) == 0 && !next.HasMore {
			it.done = true
			return false
		}
	}
}

// Transaction returns the transaction the last call to Next landed on.
func (it *TransactionIterator) Transaction() Transaction { return it.current }

// Err returns the first error the iterator hit, if any.
func (it *TransactionIterator) Err() error { return it.err }

// Collect walks the iterator and returns up to limit transactions. A limit of 0
// or less collects everything, which for a busy account may be many requests.
func (it *TransactionIterator) Collect(ctx context.Context, limit int) ([]Transaction, error) {
	var collected []Transaction

	for it.Next(ctx) {
		collected = append(collected, it.Transaction())
		if limit > 0 && len(collected) >= limit {
			break
		}
	}

	return collected, it.Err()
}

// Retrieve returns the current state of a single transaction.
func (s *TransactionsService) Retrieve(ctx context.Context, reference string) (*Transaction, error) {
	if err := validateReference(reference); err != nil {
		return nil, err
	}

	var transaction Transaction
	if err := s.client.do(ctx, request{
		method: http.MethodGet,
		path:   "/transactions/" + url.PathEscape(reference),
	}, &transaction); err != nil {
		return nil, err
	}

	return &transaction, nil
}

// Timeline returns every event recorded against a transaction, earliest first.
//
// Events is empty until the first provider event arrives.
func (s *TransactionsService) Timeline(ctx context.Context, reference string) (*Timeline, error) {
	if err := validateReference(reference); err != nil {
		return nil, err
	}

	var timeline Timeline
	if err := s.client.do(ctx, request{
		method: http.MethodGet,
		path:   "/transactions/" + url.PathEscape(reference) + "/timeline",
	}, &timeline); err != nil {
		return nil, err
	}

	return &timeline, nil
}
