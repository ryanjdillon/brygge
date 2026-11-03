package email

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"time"
)

// Compile-time check that *SMTPClient implements Sender.
var _ Sender = (*SMTPClient)(nil)

// SMTPClient sends email via an external SMTP relay: the self-hosted mail
// server on the same host (Stalwart), or the Amazon SES SMTP interface when
// built with WithSESConfigurationSet.
type SMTPClient struct {
	host        string
	port        int
	username    string
	password    string
	fromAddress string
	replyTo     string
	// sesConfigurationSet, when set, makes this an SES client: every message
	// names the configuration set and carries the send_id / kind tags, and
	// the SES message id is read back from the end-of-DATA reply.
	sesConfigurationSet string
}

// SMTPOption configures an SMTPClient.
type SMTPOption func(*SMTPClient)

// WithSESConfigurationSet sends through an SES configuration set, whose
// event destination reports what happened to each message.
func WithSESConfigurationSet(name string) SMTPOption {
	return func(c *SMTPClient) { c.sesConfigurationSet = name }
}

// NewSMTPClient returns a Sender that speaks SMTP. Returns nil if host
// or fromAddress is empty. Default port is 587 (Submission with STARTTLS);
// port 465 is treated as implicit TLS (SMTPS). replyTo is optional; when
// set it's added as the Reply-To header so recipients' mail clients
// direct replies at a monitored address (e.g. post@) even when the
// From address is a sending-only identity (e.g. login@).
func NewSMTPClient(host string, port int, username, password, fromAddress, replyTo string, opts ...SMTPOption) *SMTPClient {
	if host == "" || fromAddress == "" {
		return nil
	}
	if port == 0 {
		port = 587
	}
	c := &SMTPClient{
		host:        host,
		port:        port,
		username:    username,
		password:    password,
		fromAddress: fromAddress,
		replyTo:     replyTo,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// withSESHeaders returns extraHeaders plus the SES event-publishing headers.
// They are set last so no caller header can redirect the configuration set
// or forge a send_id.
func (c *SMTPClient) withSESHeaders(ctx context.Context, extraHeaders map[string]string) map[string]string {
	if c.sesConfigurationSet == "" {
		return extraHeaders
	}
	h := make(map[string]string, len(extraHeaders)+2)
	for k, v := range extraHeaders {
		h[k] = v
	}
	var sendID string
	if a := attemptFrom(ctx); a != nil {
		sendID = a.sendID
	}
	h[headerSESConfigurationSet] = c.sesConfigurationSet
	h[headerSESMessageTags] = sesMessageTags(sendID, kindFrom(ctx))
	return h
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
	msg, err := c.buildMessage(to, subject, htmlBody, filename, attachment, c.withSESHeaders(ctx, extraHeaders))
	if err != nil {
		return fmt.Errorf("build message: %w", err)
	}

	addr := net.JoinHostPort(c.host, strconv.Itoa(c.port))
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp dial %s: %w", addr, err)
	}
	// The dial honours ctx, but every later read and write would not: a relay
	// that accepts the connection and then stalls would hold the caller
	// forever. The connection deadline makes the whole exchange obey ctx's.
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
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

	reply, err := writeData(client, msg)
	if err != nil {
		return err
	}
	if c.sesConfigurationSet != "" {
		if a := attemptFrom(ctx); a != nil {
			a.messageID = sesMessageID(reply)
		}
	}
	// The relay accepted the message when DATA closed cleanly; a QUIT that
	// fails after that must not report a delivered message as failed, or the
	// caller retries and the recipient gets it twice.
	// The package has no logger, and the error carries nothing to act on, so
	// it is dropped; the deferred Close ends the connection either way.
	_ = client.Quit()
	return nil
}

// writeData runs the DATA exchange and returns the relay's reply to the end
// of the message. It is net/smtp's Client.Data spelled out, because that
// discards the reply, which is where SES puts its message id.
//
// Errors are classified for the caller's retry decision. Up to the 354 the
// relay has nothing, so a failure is plain. After it, a definite SMTP reply
// to the final dot (4xx/5xx) is still a refusal; but a broken connection or
// timeout while the body is written or the reply awaited leaves the relay
// possibly holding the message, which is ErrDeliveryAmbiguous.
func writeData(client *smtp.Client, msg []byte) (string, error) {
	text := client.Text
	id, err := text.Cmd("DATA")
	if err != nil {
		return "", fmt.Errorf("smtp DATA: %w", err)
	}
	text.StartResponse(id)
	_, _, err = text.ReadResponse(354)
	text.EndResponse(id)
	if err != nil {
		return "", fmt.Errorf("smtp DATA: %w", err)
	}

	w := text.DotWriter()
	if _, err := w.Write(msg); err != nil {
		_ = w.Close()
		return "", fmt.Errorf("smtp write body: %w: %w", ErrDeliveryAmbiguous, err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("smtp close DATA: %w: %w", ErrDeliveryAmbiguous, err)
	}
	_, reply, err := text.ReadResponse(250)
	if err != nil {
		var refused *textproto.Error
		if errors.As(err, &refused) {
			return "", fmt.Errorf("smtp close DATA: %w", err)
		}
		return "", fmt.Errorf("smtp close DATA: %w: %w", ErrDeliveryAmbiguous, err)
	}
	return reply, nil
}

// encodeAddressHeader renders a configured "Name <addr>" value for a header.
// Header bytes must be 7-bit ASCII (RFC 5322): a raw non-ASCII display name
// such as a club's own name is refused outright by strict receivers (Yahoo
// answers 554 "From header is invalid"), so the name is RFC 2047-encoded. A
// bare address stays bare, and a value that does not parse is left as is.
func encodeAddressHeader(v string) string {
	addr, err := mail.ParseAddress(v)
	if err != nil {
		return v
	}
	if addr.Name == "" {
		return addr.Address
	}
	return addr.String()
}

func (c *SMTPClient) buildMessage(to, subject, htmlBody, filename string, attachment []byte, extraHeaders map[string]string) ([]byte, error) {
	var buf bytes.Buffer
	header := textproto.MIMEHeader{}
	header.Set("From", encodeAddressHeader(c.fromAddress))
	header.Set("To", to)
	if c.replyTo != "" {
		header.Set("Reply-To", encodeAddressHeader(c.replyTo))
	}
	header.Set("Subject", mime.QEncoding.Encode("utf-8", subject))
	header.Set("MIME-Version", "1.0")
	header.Set("Date", time.Now().UTC().Format(time.RFC1123Z))
	for k, v := range extraHeaders {
		header.Set(k, v)
	}

	if len(attachment) == 0 {
		header.Set("Content-Type", "text/html; charset=UTF-8")
		header.Set("Content-Transfer-Encoding", "quoted-printable")
		writeHeaders(&buf, header)
		buf.WriteString("\r\n")
		if err := writeQuotedPrintable(&buf, htmlBody); err != nil {
			return nil, err
		}
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

// writeQuotedPrintable encodes body with the standard-library streaming
// quoted-printable writer. This MUST match the declared
// Content-Transfer-Encoding: quoted-printable — writing the raw body instead
// makes the recipient's QP decoder mangle every literal '=' (so `style="..."`
// arrives as `style"..."`), which silently breaks all HTML attributes.
func writeQuotedPrintable(w io.Writer, body string) error {
	qw := quotedprintable.NewWriter(w)
	if _, err := qw.Write([]byte(body)); err != nil {
		return err
	}
	return qw.Close()
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
