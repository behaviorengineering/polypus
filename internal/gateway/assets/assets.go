// Package assets embeds the static files the gateway landing page serves.
package assets

import (
	_ "embed"
	"slices"
)

// bannerWebP is the Polypus banner (internal/gateway/assets/banner.webp), which the
// README and the docs site also show.
//
//go:embed banner.webp
var bannerWebP []byte

// BannerWebP returns a copy of the landing banner, so no caller can change the
// embedded bytes.
func BannerWebP() []byte { return slices.Clone(bannerWebP) }
