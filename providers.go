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

// GetAll returns every provider available to the account, each with the
// payment flows it supports.
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

// ListForDirectPayment returns the providers that can take a direct payment,
// keeping only their direct-payment flows. Providers with no such flow are left
// out.
//
// Pass a provider's ProviderCode as [DirectPaymentParams.PSPCode], and
// pre-select the flow from [Provider.DefaultFlow].
func (s *ProvidersService) ListForDirectPayment(ctx context.Context) ([]Provider, error) {
	providers, err := s.GetAll(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]Provider, 0, len(providers))
	for _, provider := range providers {
		var flows []ProviderFlow
		for _, flow := range provider.Flows {
			if flow.SupportsDirectPayment {
				flows = append(flows, flow)
			}
		}
		if len(flows) > 0 {
			provider.Flows = flows
			result = append(result, provider)
		}
	}

	return result, nil
}

// ListEnabled returns the same list as [ProvidersService.GetAll].
//
// Deprecated: The API no longer reports a per-provider enabled state; every
// provider it returns is available to the account. Use
// [ProvidersService.GetAll] or [ProvidersService.ListForDirectPayment].
func (s *ProvidersService) ListEnabled(ctx context.Context) ([]Provider, error) {
	return s.GetAll(ctx)
}
