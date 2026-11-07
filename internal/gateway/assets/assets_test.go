package assets

import (
	"bytes"
	"testing"
)

func TestBannerWebPEmbedded(t *testing.T) {
	b := BannerWebP()
	if len(b) < 12 {
		t.Fatalf("banner is %d bytes, want a WebP file", len(b))
	}
	if string(b[0:4]) != "RIFF" {
		t.Fatalf("RIFF magic: %q", b[0:4])
	}
	if string(b[8:12]) != "WEBP" {
		t.Fatalf("WEBP marker: %q", b[8:12])
	}
}

func TestBannerWebPReturnsACopy(t *testing.T) {
	first := BannerWebP()
	want := bytes.Clone(first)
	first[0] = 'X'
	if got := BannerWebP(); !bytes.Equal(got, want) {
		t.Fatal("a caller's write changed the embedded banner")
	}
}
