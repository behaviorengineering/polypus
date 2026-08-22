package cloudflare

import "errors"

var (
	errBaseURLRequired   = errors.New("cloudflare: base_url required")
	errAccountIDRequired = errors.New("cloudflare: base_url must include /accounts/{id}/")
	errAPIKeyRequired    = errors.New("cloudflare: api_key required")
)
