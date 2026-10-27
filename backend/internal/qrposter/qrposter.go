// Package qrposter renders the scannable artifacts for harbor QR payment
// targets (BRY-6): a QR PNG encoding the public /qr/{token} checkout URL, and a
// print-ready A4 poster PDF wrapping it with the club name, a "pay here"
// headline, label, price, and scan instructions.
package qrposter

import (
	"bytes"
	"fmt"
	"image/png"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/qr"
	"github.com/go-pdf/fpdf"
)

// QR returns a PNG of a QR code encoding url, sized to px × px.
func QR(url string, px int) ([]byte, error) {
	if url == "" {
		return nil, fmt.Errorf("qrposter: empty url")
	}
	if px < 64 {
		px = 512
	}
	code, err := qr.Encode(url, qr.M, qr.Auto)
	if err != nil {
		return nil, fmt.Errorf("qrposter: encode: %w", err)
	}
	code, err = barcode.Scale(code, px, px)
	if err != nil {
		return nil, fmt.Errorf("qrposter: scale: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, code); err != nil {
		return nil, fmt.Errorf("qrposter: png: %w", err)
	}
	return buf.Bytes(), nil
}

// PosterData is the copy printed around the QR on an A4 poster.
type PosterData struct {
	ClubName     string
	Headline     string   // e.g. "Betal her"; the largest line on the poster
	Label        string   // e.g. "Gjestehavn – Brygge A"
	Location     string   // optional line under the label
	PriceLine    string   // e.g. "200 kr / natt" or "200 kr"
	Instructions []string // scan steps, in the club's language
	URL          string   // the public /qr/{token} URL encoded in the QR
	ShortURL     string   // optional typed fallback, printed without scheme
}

// Poster renders an A4 portrait PDF: club name, headline, label, location, a
// large centred QR, the short typed address, the price, and instructions.
// Fonts are the fpdf core Helvetica set so no font files are needed.
func Poster(d PosterData) ([]byte, error) {
	qrPNG, err := QR(d.URL, 1024)
	if err != nil {
		return nil, err
	}

	pdf := fpdf.New("P", "mm", "A4", "")
	// Core fonts are cp1252-encoded; translate UTF-8 (e.g. "Båtlag") so it
	// doesn't render as mojibake ("BÃ¥tlag").
	tr := pdf.UnicodeTranslatorFromDescriptor("")
	pdf.SetMargins(20, 24, 20)
	pdf.AddPage()
	const pageW = 210.0
	center := func(txt string) { pdf.CellFormat(0, 0, tr(txt), "", 0, "C", false, 0, "") }

	if d.ClubName != "" {
		pdf.SetFont("Helvetica", "", 12)
		pdf.SetTextColor(110, 110, 110)
		pdf.SetY(20)
		center(d.ClubName)
	}

	// The headline is what a passer-by reads first, so it is set larger than
	// the label.
	if d.Headline != "" {
		pdf.SetTextColor(0, 0, 0)
		pdf.SetFont("Helvetica", "B", 44)
		pdf.SetY(36)
		center(d.Headline)
	}

	pdf.SetTextColor(20, 20, 20)
	pdf.SetFont("Helvetica", "B", 28)
	pdf.SetY(54)
	center(d.Label)

	if d.Location != "" {
		pdf.SetFont("Helvetica", "", 14)
		pdf.SetTextColor(90, 90, 90)
		pdf.SetY(66)
		center(d.Location)
	}

	// Centred QR block.
	const qrSize = 110.0
	qrX := (pageW - qrSize) / 2
	qrY := 76.0
	pdf.RegisterImageOptionsReader("qr", fpdf.ImageOptions{ImageType: "PNG"}, bytes.NewReader(qrPNG))
	if pdf.Err() {
		return nil, fmt.Errorf("qrposter: register image: %w", pdf.Error())
	}
	pdf.ImageOptions("qr", qrX, qrY, qrSize, qrSize, false, fpdf.ImageOptions{ImageType: "PNG"}, 0, "")

	// Space below the QR is reserved for the short address whether or not it
	// is set, so the rest of the layout does not shift between posters.
	if d.ShortURL != "" {
		pdf.SetTextColor(40, 40, 40)
		pdf.SetFont("Courier", "", 14)
		pdf.SetY(qrY + qrSize + 7)
		center(d.ShortURL)
	}

	if d.PriceLine != "" {
		pdf.SetTextColor(20, 20, 20)
		pdf.SetFont("Helvetica", "B", 22)
		pdf.SetY(qrY + qrSize + 20)
		center(d.PriceLine)
	}

	pdf.SetFont("Helvetica", "", 13)
	pdf.SetTextColor(70, 70, 70)
	y := qrY + qrSize + 36
	for _, line := range d.Instructions {
		pdf.SetY(y)
		center(line)
		y += 8
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("qrposter: output: %w", err)
	}
	return buf.Bytes(), nil
}
