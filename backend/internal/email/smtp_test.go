package email

import (
	"bytes"
	"encoding/base64"
	"fmt"
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
		if disp, dp, _ := mime.ParseMediaType(p.Header.Get("Content-Disposition")); disp == "attachment" {
			if dp["filename"] != "faktura.pdf" {
				t.Errorf("attachment filename = %q", dp["filename"])
			}
			raw, err := io.ReadAll(p)
			if err != nil {
				t.Fatalf("read attachment: %v", err)
			}
			got, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(string(raw), "\r\n", ""))
			if err != nil || string(got) != "%PDF-1.4 stub" {
				t.Errorf("attachment = %q (%v), want the stub bytes", got, err)
			}
			continue
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

// writeBase64Lines must emit full 76-column lines, a shorter last line, CRLF
// after every line and nothing empty, across the lengths where a base64 group
// (57 input bytes = 76 characters) ends exactly at, just before or just past
// a line break.
func TestWriteBase64Lines(t *testing.T) {
	lengths := []int{0, 1, 56, 57, 58}
	for _, k := range []int{2, 7} {
		lengths = append(lengths, 57*k-1, 57*k, 57*k+1)
	}
	for _, n := range lengths {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			in := make([]byte, n)
			for i := range in {
				in[i] = byte(i*31 + 7)
			}
			var buf bytes.Buffer
			if err := writeBase64Lines(&buf, in); err != nil {
				t.Fatalf("writeBase64Lines: %v", err)
			}
			out := buf.String()
			if n == 0 {
				if out != "" {
					t.Fatalf("empty input wrote %q", out)
				}
				return
			}
			if !strings.HasSuffix(out, "\r\n") {
				t.Fatalf("output does not end in CRLF: %q", out[max(0, len(out)-10):])
			}
			lines := strings.Split(strings.TrimSuffix(out, "\r\n"), "\r\n")
			for i, l := range lines {
				if l == "" {
					t.Fatalf("line %d is empty", i)
				}
				if i < len(lines)-1 && len(l) != 76 {
					t.Errorf("line %d is %d characters, want 76", i, len(l))
				}
				if len(l) > 76 {
					t.Errorf("line %d is %d characters, over 76", i, len(l))
				}
			}
			got, err := base64.StdEncoding.DecodeString(strings.Join(lines, ""))
			if err != nil || !bytes.Equal(got, in) {
				t.Errorf("decoded %d bytes (%v), want the %d input bytes back", len(got), err, n)
			}
		})
	}
}
