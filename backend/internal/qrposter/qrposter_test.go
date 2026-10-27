package qrposter

import (
	"bytes"
	"compress/zlib"
	"io"
	"regexp"
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
		ShortURL:     "klokkarvikbaatlag.no/q/gjest",
	})
	if err != nil {
		t.Fatalf("Poster: %v", err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF")) {
		t.Fatal("Poster: output is not a PDF")
	}
}

// pdfText inflates the PDF's content streams so a test can look for the text
// drawn on the page.
func pdfText(t *testing.T, pdf []byte) string {
	t.Helper()
	var out bytes.Buffer
	re := regexp.MustCompile(`(?s)stream\r?\n(.*?)\r?\nendstream`)
	for _, m := range re.FindAllSubmatch(pdf, -1) {
		zr, err := zlib.NewReader(bytes.NewReader(m[1]))
		if err != nil {
			out.Write(m[1])
			continue
		}
		b, _ := io.ReadAll(zr)
		out.Write(b)
	}
	return out.String()
}

func TestPosterPrintsShortURL(t *testing.T) {
	data := PosterData{
		Label:     "Synthetic dock",
		PriceLine: "100 kr",
		URL:       "https://example.test/qr/abc123",
		ShortURL:  "example.test/q/dock-a",
	}
	pdf, err := Poster(data)
	if err != nil {
		t.Fatalf("Poster: %v", err)
	}
	if !bytes.Contains([]byte(pdfText(t, pdf)), []byte("(example.test/q/dock-a)")) {
		t.Fatal("Poster: short URL not drawn")
	}

	data.ShortURL = ""
	pdf, err = Poster(data)
	if err != nil {
		t.Fatalf("Poster without short URL: %v", err)
	}
	if bytes.Contains([]byte(pdfText(t, pdf)), []byte("/q/")) {
		t.Fatal("Poster: drew a short URL that was not given")
	}
}
