package db

import "strings"

// CompleteDiscoveryURLSQL updates the donated catalog fallback to match the
// full standard catalog address. Explicit and historical pins remain first.
var CompleteDiscoveryURLSQL = func() string {
	_, view, _ := strings.Cut(ServiceKeyScopeSQL, "DROP VIEW charity_model_catalog;")
	return "DROP VIEW charity_model_catalog;" + strings.ReplaceAll(view,
		"sr.canonical_base_url=a.canonical_base_url",
		`sr.canonical_base_url=CASE
 WHEN substr(rtrim(a.canonical_base_url,'/'),-7)=CASE WHEN a.configured_type IN ('ai-sdk-gateway-v3','ai-sdk-gateway-v4') THEN '/config' ELSE '/models' END THEN a.canonical_base_url
 ELSE rtrim(a.canonical_base_url,'/')||CASE WHEN a.configured_type IN ('ai-sdk-gateway-v3','ai-sdk-gateway-v4') THEN '/config' ELSE '/models' END END`)
}()
