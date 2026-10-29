package email

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func TestBuildMessageHeaderLinesStayWithinRFCLimit(t *testing.T) {
	c := NewSMTPClient("localhost", 587, "", "", "relay@club.example", "")
	subject := ContactSubject(strings.Repeat("😀", 150))

	msg, err := c.buildMessage("to@example.com", subject, "<p>x</p>", "", nil, map[string]string{"Reply-To": "v@example.com"})
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	head, _, _ := strings.Cut(string(msg), "\r\n\r\n")
	for _, line := range strings.Split(head, "\r\n") {
		if len(line) > 998 {
			t.Errorf("header line of %d bytes exceeds the RFC 5322 limit: %.40s...", len(line), line)
		}
	}
}

// The contact form sets Reply-To to the submitter; it must replace, not
// join, the configured EMAIL_REPLY_TO so the recipient answers the visitor.
func TestBuildMessageExtraReplyToOverridesConfigured(t *testing.T) {
	c := NewSMTPClient("localhost", 587, "", "", "relay@club.example", "post@club.example")

	msg, err := c.buildMessage("to@example.com", "Hi", "<p>x</p>", "", nil, map[string]string{"Reply-To": "visitor@example.com"})
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	m, err := mail.ReadMessage(bytes.NewReader(msg))
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	got := m.Header["Reply-To"]
	if len(got) != 1 || got[0] != "visitor@example.com" {
		t.Errorf("Reply-To = %v, want exactly the visitor", got)
	}
	if from := m.Header.Get("From"); from != "relay@club.example" {
		t.Errorf("From = %q, want the configured sender", from)
	}
}

// fakeSMTP is a one-connection SMTP server whose behaviour after the greeting
// is chosen by the test.
func fakeSMTP(t *testing.T, serve func(c net.Conn, r *bufio.Reader)) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		serve(c, bufio.NewReader(c))
	}()
	addr := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port
}

// A relay that accepts the connection and then goes silent must not hold the
// caller past its context deadline: the dial timeout alone does not cover it.
func TestSendBoundedByContextDeadlineWhenRelayStalls(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	host, port := fakeSMTP(t, func(c net.Conn, _ *bufio.Reader) {
		// Accepted, never greets.
		<-release
	})
	c := NewSMTPClient(host, port, "", "", "relay@club.example", "")

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := c.Send(ctx, "to@example.com", "Hi", "<p>x</p>")

	if err == nil {
		t.Fatal("expected an error from a stalled relay")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("send took %s, want it bounded by the 300ms deadline", elapsed)
	}
}

// Once DATA is accepted the message is the relay's; a failing QUIT must not
// turn a delivered message into an error.
func TestSendIgnoresQuitErrorAfterDataAccepted(t *testing.T) {
	host, port := fakeSMTP(t, func(c net.Conn, r *bufio.Reader) {
		write := func(s string) { _, _ = c.Write([]byte(s + "\r\n")) }
		write("220 fake ESMTP")
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if inData {
				if line == "." {
					inData = false
					write("250 queued")
				}
				continue
			}
			switch cmd := strings.ToUpper(line); {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				write("250 fake")
			case strings.HasPrefix(cmd, "MAIL"), strings.HasPrefix(cmd, "RCPT"):
				write("250 ok")
			case cmd == "DATA":
				inData = true
				write("354 go ahead")
			case cmd == "QUIT":
				write("421 shutting down")
				return
			default:
				write("250 ok")
			}
		}
	})
	c := NewSMTPClient(host, port, "", "", "relay@club.example", "")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Send(ctx, "to@example.com", "Hi", "<p>x</p>"); err != nil {
		t.Fatalf("a QUIT failure after accepted DATA must not fail the send: %v", err)
	}
}
