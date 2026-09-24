package epay

import (
	"context"
	"net/http"
)

// ProvidersService covers the account's payment providers.
//
// Both endpoints are mode-aware: a sk_test_… key returns sandbox providers and
// a sk_live_… key returns live ones.
type ProvidersService struct {
	client *Client
}

// List returns a compact provider list suited to dropdowns and selection UIs.
//
// It requires the list_platform_payment_provider permission, and otherwise
// returns an error wrapping [ErrPermissionDenied].
func (s *ProvidersService) List(ctx context.Context) ([]ProviderItem, error) {
	var envelope providerEnvelope[ProviderItem]
	if err := s.client.do(ctx, request{
		method: http.MethodGet,
		path:   "/payment-providers/list",
	}, &envelope); err != nil {
		return nil, err
	}

	return envelope.Data, nil
}

// GetAll returns every provider on the account with its category and enabled
// state.
//
// Disabled providers are included so your UI can show them as unavailable
// rather than having them silently disappear.
//
// It requires the get_platform_payment_provider permission, and otherwise
// returns an error wrapping [ErrPermissionDenied].
func (s *ProvidersService) GetAll(ctx context.Context) ([]Provider, error) {
	var envelope providerEnvelope[Provider]
	if err := s.client.do(ctx, request{
		method: http.MethodGet,
		path:   "/payment-providers",
	}, &envelope); err != nil {
		return nil, err
	}

	return envelope.Data, nil
}

// ListEnabled returns only the providers the account can currently route
// payments through.
func (s *ProvidersService) ListEnabled(ctx context.Context) ([]Provider, error) {
	providers, err := s.GetAll(ctx)
	if err != nil {
		return nil, err
	}

	enabled := make([]Provider, 0, len(providers))
	for _, provider := range providers {
		if provider.IsEnabled {
			enabled = append(enabled, provider)
		}
	}

	return enabled, nil
}
