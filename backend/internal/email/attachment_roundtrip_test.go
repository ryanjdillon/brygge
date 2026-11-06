package email_test

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/finance"
)

// A faktura PDF must reach the member byte for byte. The attachment was once
// written as one unbroken base64 line, far past the 998-byte line limit of
// RFC 5322, which relays may fold or truncate; this test uses a real PDF so
// the attachment is large enough to show it.
func TestFakturaAttachmentRoundTrip(t *testing.T) {
	pdf, err := finance.GeneratePDF(finance.Invoice{
		ClubName:      "Eksempel Båtlag",
		OrgNumber:     "999 999 999",
		ClubAddress:   "Bryggeveien 1, 0001 Eksempelby",
		MemberName:    "Kari Testesen",
		MemberAddress: "Testgata 2, 0002 Eksempelby",
		InvoiceNumber: 4242,
		IssueDate:     time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		DueDate:       time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
		KID:           "000042420017",
		BankAccount:   "1234.56.78903",
		Lines: []finance.InvoiceLine{
			{Description: "Båtplassleie 2026", SubDescription: "Plass A1 · 3,0 m bredde", Quantity: 1, UnitPrice: 2500},
			{Description: "Medlemskontingent", Quantity: 1, UnitPrice: 650},
			{Description: "Fritak styreverv", Quantity: 1, UnitPrice: -650},
		},
	})
	if err != nil {
		t.Fatalf("GeneratePDF: %v", err)
	}
	if len(pdf) < 1024 {
		t.Fatalf("PDF is only %d bytes; too small to exercise line wrapping", len(pdf))
	}

	c := email.NewSMTPClient("localhost", 587, "", "", "Eksempel Båtlag <relay@club.example>", "")
	const filename = "faktura-4242.pdf"
	msg, err := c.BuildMessage("kari@example.com", "Faktura 4242", "<p>Hei Kari</p>", filename, pdf)
	if err != nil {
		t.Fatalf("BuildMessage: %v", err)
	}

	longest := 0
	for i, line := range strings.Split(string(msg), "\r\n") {
		longest = max(longest, len(line))
		if len(line) > 998 {
			t.Errorf("message line %d is %d bytes, over the RFC 5322 limit of 998", i, len(line))
		}
	}
	t.Logf("longest message line: %d bytes (PDF %d bytes)", longest, len(pdf))

	m, err := mail.ReadMessage(bytes.NewReader(msg))
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	_, params, err := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("top-level Content-Type: %v", err)
	}
	mr := multipart.NewReader(m.Body, params["boundary"])
	var got []byte
	found := false
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextPart: %v", err)
		}
		disp, dparams, err := mime.ParseMediaType(p.Header.Get("Content-Disposition"))
		if err != nil || disp != "attachment" {
			continue
		}
		found = true
		if dparams["filename"] != filename {
			t.Errorf("Content-Disposition filename = %q, want %q", dparams["filename"], filename)
		}
		ctype, cparams, err := mime.ParseMediaType(p.Header.Get("Content-Type"))
		if err != nil || ctype != "application/pdf" || cparams["name"] != filename {
			t.Errorf("attachment Content-Type = %q %v (%v), want application/pdf name=%s", ctype, cparams, err, filename)
		}
		raw, err := io.ReadAll(p)
		if err != nil {
			t.Fatalf("read attachment: %v", err)
		}
		for i, line := range strings.Split(strings.TrimRight(string(raw), "\r\n"), "\r\n") {
			if len(line) > 76 {
				t.Errorf("base64 line %d is %d bytes, over the MIME limit of 76", i, len(line))
				break
			}
		}
		got, err = io.ReadAll(base64Decoder(raw))
		if err != nil {
			t.Fatalf("decode attachment: %v", err)
		}
	}
	if !found {
		t.Fatal("no attachment part in the message")
	}
	if !bytes.Equal(got, pdf) {
		t.Fatalf("decoded attachment (%d bytes) differs from the PDF (%d bytes)", len(got), len(pdf))
	}
	if !bytes.HasPrefix(got, []byte("%PDF")) {
		t.Errorf("decoded attachment does not start with %s", "%PDF")
	}
	if !bytes.HasSuffix(bytes.TrimRight(got, "\r\n"), []byte("%%EOF")) {
		t.Errorf("decoded attachment does not end with %s", "%%EOF")
	}
}

// base64Decoder decodes a base64 MIME body; the standard decoder skips the
// CRLF line breaks.
func base64Decoder(raw []byte) io.Reader {
	return base64.NewDecoder(base64.StdEncoding, bytes.NewReader(raw))
}
