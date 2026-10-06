package media

import (
	"testing"
)

func TestBannerWebPEmbedded(t *testing.T) {
	if len(BannerWebP) == 0 {
		t.Fatal("empty banner embed")
	}
	if string(BannerWebP[0:4]) != "RIFF" {
		t.Fatalf("RIFF magic: %q", BannerWebP[0:4])
	}
	if len(BannerWebP) < 12 || string(BannerWebP[8:12]) != "WEBP" {
		t.Fatalf("WEBP marker: %q", BannerWebP[8:12])
	}
}
