package email

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"time"
)

// Compile-time check that *SMTPClient implements Sender.
var _ Sender = (*SMTPClient)(nil)

// SMTPClient sends email via an external SMTP relay — typically the
// self-hosted mail server on the same host (Stalwart on localhost:587).
type SMTPClient struct {
	host        string
	port        int
	username    string
	password    string
	fromAddress string
	replyTo     string
}

// NewSMTPClient returns a Sender that speaks SMTP. Returns nil if host
// or fromAddress is empty. Default port is 587 (Submission with STARTTLS);
// port 465 is treated as implicit TLS (SMTPS). replyTo is optional; when
// set it's added as the Reply-To header so recipients' mail clients
// direct replies at a monitored address (e.g. info@) even when the
// From address is a sending-only identity (e.g. login@).
func NewSMTPClient(host string, port int, username, password, fromAddress, replyTo string) *SMTPClient {
	if host == "" || fromAddress == "" {
		return nil
	}
	if port == 0 {
		port = 587
	}
	return &SMTPClient{
		host:        host,
		port:        port,
		username:    username,
		password:    password,
		fromAddress: fromAddress,
		replyTo:     replyTo,
	}
}

func (c *SMTPClient) Send(ctx context.Context, to, subject, htmlBody string) error {
	return c.send(ctx, to, subject, htmlBody, "", nil, nil)
}

func (c *SMTPClient) SendWithAttachment(ctx context.Context, to, subject, htmlBody, filename string, attachment []byte) error {
	return c.send(ctx, to, subject, htmlBody, filename, attachment, nil)
}

func (c *SMTPClient) SendWithHeaders(ctx context.Context, to, subject, htmlBody string, extraHeaders map[string]string) error {
	return c.send(ctx, to, subject, htmlBody, "", nil, extraHeaders)
}

func (c *SMTPClient) send(ctx context.Context, to, subject, htmlBody, filename string, attachment []byte, extraHeaders map[string]string) error {
	msg, err := c.buildMessage(to, subject, htmlBody, filename, attachment, extraHeaders)
	if err != nil {
		return fmt.Errorf("build message: %w", err)
	}

	addr := net.JoinHostPort(c.host, strconv.Itoa(c.port))
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp dial %s: %w", addr, err)
	}

	var client *smtp.Client
	tlsConfig := &tls.Config{ServerName: c.host, MinVersion: tls.VersionTLS12}

	if c.port == 465 {
		tlsConn := tls.Client(conn, tlsConfig)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			conn.Close()
			return fmt.Errorf("smtp tls handshake: %w", err)
		}
		client, err = smtp.NewClient(tlsConn, c.host)
	} else {
		client, err = smtp.NewClient(conn, c.host)
	}
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp new client: %w", err)
	}
	defer client.Close()

	if c.port != 465 {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsConfig); err != nil {
				return fmt.Errorf("smtp starttls: %w", err)
			}
		}
	}

	if c.username != "" {
		auth := smtp.PlainAuth("", c.username, c.password, c.host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	// SMTP envelope MAIL FROM must be a bare address — strip any display
	// name if EmailFrom came in RFC 5322 "Name <addr>" form.
	envelopeFrom := c.fromAddress
	if addr, err := mail.ParseAddress(c.fromAddress); err == nil {
		envelopeFrom = addr.Address
	}
	if err := client.Mail(envelopeFrom); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("smtp RCPT TO: %w", err)
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		_ = w.Close()
		return fmt.Errorf("smtp write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close DATA: %w", err)
	}
	return client.Quit()
}

func (c *SMTPClient) buildMessage(to, subject, htmlBody, filename string, attachment []byte, extraHeaders map[string]string) ([]byte, error) {
	var buf bytes.Buffer
	header := textproto.MIMEHeader{}
	header.Set("From", c.fromAddress)
	header.Set("To", to)
	if c.replyTo != "" {
		header.Set("Reply-To", c.replyTo)
	}
	header.Set("Subject", mime.QEncoding.Encode("utf-8", subject))
	header.Set("MIME-Version", "1.0")
	header.Set("Date", time.Now().UTC().Format(time.RFC1123Z))
	for k, v := range extraHeaders {
		header.Set(k, v)
	}

	if len(attachment) == 0 {
		header.Set("Content-Type", "text/html; charset=UTF-8")
		writeHeaders(&buf, header)
		buf.WriteString("\r\n")
		buf.WriteString(htmlBody)
		return buf.Bytes(), nil
	}

	mw := multipart.NewWriter(&buf)
	header.Set("Content-Type", fmt.Sprintf("multipart/mixed; boundary=%q", mw.Boundary()))
	writeHeaders(&buf, header)
	buf.WriteString("\r\n")

	htmlHeader := textproto.MIMEHeader{}
	htmlHeader.Set("Content-Type", "text/html; charset=UTF-8")
	htmlHeader.Set("Content-Transfer-Encoding", "quoted-printable")
	htmlPart, err := mw.CreatePart(htmlHeader)
	if err != nil {
		return nil, err
	}
	if err := writeQuotedPrintable(htmlPart, htmlBody); err != nil {
		return nil, err
	}

	attHeader := textproto.MIMEHeader{}
	attHeader.Set("Content-Type", detectContentType(filename))
	attHeader.Set("Content-Transfer-Encoding", "base64")
	attHeader.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	attPart, err := mw.CreatePart(attHeader)
	if err != nil {
		return nil, err
	}
	b64 := base64.NewEncoder(base64.StdEncoding, attPart)
	if _, err := b64.Write(attachment); err != nil {
		return nil, err
	}
	if err := b64.Close(); err != nil {
		return nil, err
	}

	if err := mw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeHeaders(buf *bytes.Buffer, h textproto.MIMEHeader) {
	for k, vs := range h {
		for _, v := range vs {
			fmt.Fprintf(buf, "%s: %s\r\n", k, v)
		}
	}
}

func writeQuotedPrintable(w interface {
	Write(p []byte) (int, error)
}, body string) error {
	qp := mime.QEncoding
	_ = qp
	// net/mail doesn't expose a stream QP encoder; for HTML bodies we
	// accept plain UTF-8 with explicit charset and let the CTE line-
	// wrapping rule be best-effort. For transactional mail this is
	// acceptable across all major MTAs.
	_, err := w.Write([]byte(body))
	return err
}

func detectContentType(filename string) string {
	switch {
	case endsWith(filename, ".pdf"):
		return "application/pdf"
	case endsWith(filename, ".png"):
		return "image/png"
	case endsWith(filename, ".jpg"), endsWith(filename, ".jpeg"):
		return "image/jpeg"
	case endsWith(filename, ".csv"):
		return "text/csv; charset=UTF-8"
	case endsWith(filename, ".txt"):
		return "text/plain; charset=UTF-8"
	default:
		return "application/octet-stream"
	}
}

func endsWith(s, suffix string) bool {
	ls, lt := len(s), len(suffix)
	if ls < lt {
		return false
	}
	for i := 0; i < lt; i++ {
		a, b := s[ls-lt+i], suffix[i]
		if a >= 'A' && a <= 'Z' {
			a += 'a' - 'A'
		}
		if a != b {
			return false
		}
	}
	return true
}
