package email

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
)

// A faktura goes out as multipart with a quoted-printable HTML part. The HTML
// MUST survive the QP round-trip intact — a prior bug declared QP but wrote the
// body raw, so every `attr="value"` arrived as `attr"value"` (dropped '='),
// breaking the "Betal med Vipps" button and all styling.
func TestBuildMessageQuotedPrintableRoundTrip(t *testing.T) {
	c := NewSMTPClient("localhost", 587, "", "", "Klokkarvik Båtlag <relay@klokkarvikbaatlag.no>", "")
	html := `<a href="https://api.vipps.no/pay?v2&token=abc123" style="background:#ff5b24;color:#ffffff">Betal med Vipps</a> — beløp å betale`

	msg, err := c.buildMessage("to@example.com", "Faktura 285", html, "faktura.pdf", []byte("%PDF-1.4 stub"), nil)
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}

	// The raw body must be quoted-printable encoded — the bug wrote it raw, so
	// literal '=' never became '=3D'.
	if !bytes.Contains(msg, []byte("=3D")) {
		t.Fatalf("HTML body is not quoted-printable encoded (no =3D in message)")
	}

	m, err := mail.ReadMessage(bytes.NewReader(msg))
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	mediaType, params, err := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		t.Fatalf("top-level Content-Type = %q (%v)", mediaType, err)
	}

	var decoded string
	mr := multipart.NewReader(m.Body, params["boundary"])
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextPart: %v", err)
		}
		if strings.HasPrefix(p.Header.Get("Content-Type"), "text/html") {
			// multipart.Part transparently decodes a quoted-printable part.
			b, err := io.ReadAll(p)
			if err != nil {
				t.Fatalf("read html part: %v", err)
			}
			decoded = string(b)
		}
	}

	if !strings.Contains(decoded, `href="https://api.vipps.no/pay?v2&token=abc123"`) {
		t.Errorf("href mangled by QP round-trip: %q", decoded)
	}
	if !strings.Contains(decoded, `style="background:#ff5b24;color:#ffffff"`) {
		t.Errorf("style attribute mangled: %q", decoded)
	}
	if !strings.Contains(decoded, "beløp å betale") {
		t.Errorf("utf-8 mangled: %q", decoded)
	}
}
