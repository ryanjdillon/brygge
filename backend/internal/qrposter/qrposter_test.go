package qrposter

import (
	"bytes"
	"testing"
)

func TestQR(t *testing.T) {
	png, err := QR("https://klokkarvikbaatlag.no/qr/abc123", 512)
	if err != nil {
		t.Fatalf("QR: %v", err)
	}
	if len(png) == 0 {
		t.Fatal("QR: empty output")
	}
	// PNG magic number.
	if !bytes.HasPrefix(png, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		t.Fatal("QR: output is not a PNG")
	}
}

func TestQREmptyURL(t *testing.T) {
	if _, err := QR("", 512); err == nil {
		t.Fatal("QR: expected error for empty url")
	}
}

func TestPoster(t *testing.T) {
	pdf, err := Poster(PosterData{
		ClubName:     "Klokkarvik Båtlag",
		Label:        "Gjestehavn – Brygge A",
		Location:     "Ytre molo",
		PriceLine:    "200 kr / natt",
		Instructions: []string{"Skann QR-koden med mobilen.", "Betal med Vipps."},
		URL:          "https://klokkarvikbaatlag.no/qr/abc123",
	})
	if err != nil {
		t.Fatalf("Poster: %v", err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF")) {
		t.Fatal("Poster: output is not a PDF")
	}
}
