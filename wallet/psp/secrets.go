package psp

import (
	"context"
)

type MapSecretResolver struct {
	Root map[string]interface{}
}

func NewMapSecretResolver(root map[string]interface{}) *MapSecretResolver {
	if root == nil {
		return nil
	}
	return &MapSecretResolver{Root: root}
}

func (r *MapSecretResolver) Resolve(ctx context.Context, tenantID, providerCode string) (SecretBundle, error) {
	_ = ctx
	if r == nil || r.Root == nil {
		return SecretBundle{}, ErrPSPSecretMissing
	}
	noebsMap, ok := getMap(r.Root, "noebs")
	if !ok {
		return SecretBundle{}, ErrPSPSecretMissing
	}
	pspMap, ok := getMap(noebsMap, "psp")
	if !ok {
		return SecretBundle{}, ErrPSPSecretMissing
	}
	tenantMap, ok := getMap(pspMap, tenantID)
	if !ok {
		return SecretBundle{}, ErrPSPSecretMissing
	}
	providerMap, ok := getMap(tenantMap, providerCode)
	if !ok {
		return SecretBundle{}, ErrPSPSecretMissing
	}
	bundle := SecretBundle{
		APIKey:           getString(providerMap, "api_key"),
		APISecret:        getString(providerMap, "api_secret"),
		WebhookSecret:    getString(providerMap, "webhook_secret"),
		WebhookPublicKey: getString(providerMap, "webhook_public_key"),
	}
	if bundle.APIKey == "" && bundle.APISecret == "" && bundle.WebhookSecret == "" && bundle.WebhookPublicKey == "" {
		return SecretBundle{}, ErrPSPSecretMissing
	}
	return bundle, nil
}

func getMap(root map[string]interface{}, key string) (map[string]interface{}, bool) {
	value, ok := root[key]
	if !ok || value == nil {
		return nil, false
	}
	child, ok := value.(map[string]interface{})
	return child, ok
}

func getString(root map[string]interface{}, key string) string {
	value, ok := root[key]
	if !ok || value == nil {
		return ""
	}
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}
