package email

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net"
	"net/mail"
	"strings"
	"sync"
	"testing"
	"time"
)

// Vectors produced by AWS's published Python conversion script
// (https://docs.aws.amazon.com/ses/latest/dg/smtp-credentials.html#smtp-credentials-convert)
// run on the documentation's example secret key.
func TestSESSMTPPasswordMatchesAWSAlgorithm(t *testing.T) {
	const docExampleKey = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	for region, want := range map[string]string{
		"us-west-2":  "BF2PynzbSCAjX08zhZZnP/kW+T9P5zs/1Er0pi5vTEmd",
		"eu-north-1": "BPfy85A/4ChxYc0s4mIG5bpIlvRl5AP1uupODe8KaHpf",
	} {
		if got := SESSMTPPassword(docExampleKey, region); got != want {
			t.Errorf("SESSMTPPassword(%s) = %q, want %q", region, got, want)
		}
	}
}

func TestSESSMTPHost(t *testing.T) {
	if got := SESSMTPHost("eu-north-1"); got != "email-smtp.eu-north-1.amazonaws.com" {
		t.Errorf("SESSMTPHost = %q", got)
	}
}

func TestSESTagValueKeepsOnlyAllowedCharacters(t *testing.T) {
	for in, want := range map[string]string{
		"magic_link":                           "magic_link",
		"3f1c2a9e-7b4d-4e2a-9c61-0d5b8e7f1a23": "3f1c2a9e-7b4d-4e2a-9c61-0d5b8e7f1a23",
		"a.b@c":                                "a_b_c",
		"bad kind,=":                           "bad_kind__",
		"båt":                                  "b_t",
	} {
		if got := sesTagValue(in); got != want {
			t.Errorf("sesTagValue(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSESMessageID(t *testing.T) {
	for reply, want := range map[string]string{
		"Ok 0100018f2c3d4e5f-aaaa-bbbb-cccc-000000000000-000000": "0100018f2c3d4e5f-aaaa-bbbb-cccc-000000000000-000000",
		"2.0.0 Message queued": "",
		"":                     "",
	} {
		if got := sesMessageID(reply); got != want {
			t.Errorf("sesMessageID(%q) = %q, want %q", reply, got, want)
		}
	}
}

const testSendID = "3f1c2a9e-7b4d-4e2a-9c61-0d5b8e7f1a23"

func TestSESHeadersOnSESClientOnly(t *testing.T) {
	ctx := withAttempt(WithSendMeta(context.Background(), SendMeta{Kind: KindCreditNote}), &attempt{sendID: testSendID})
	// A caller header cannot override the configuration set.
	extra := map[string]string{"Reply-To": "visitor@example.com", headerSESConfigurationSet: "other"}

	ses := NewSMTPClient("localhost", 465, "", "", "relay@club.example", "", WithSESConfigurationSet("brygge-test"))
	msg, err := ses.buildMessage("to@example.com", "Hi", "<p>x</p>", "", nil, ses.withSESHeaders(ctx, extra))
	if err != nil {
		t.Fatal(err)
	}
	m, err := mail.ReadMessage(bytes.NewReader(msg))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Header.Get(headerSESConfigurationSet); got != "brygge-test" {
		t.Errorf("configuration set = %q", got)
	}
	if got, want := m.Header.Get(headerSESMessageTags), "send_id="+testSendID+", kind=credit_note"; got != want {
		t.Errorf("message tags = %q, want %q", got, want)
	}
	if got := m.Header.Get("Reply-To"); got != "visitor@example.com" {
		t.Errorf("Reply-To = %q, caller headers must survive", got)
	}
	if _, ok := extra[headerSESMessageTags]; ok {
		t.Error("the caller's header map was modified")
	}

	// An unrecorded send still carries its kind.
	if got := ses.withSESHeaders(context.Background(), nil)[headerSESMessageTags]; got != "kind=other" {
		t.Errorf("unrecorded tags = %q, want kind=other", got)
	}

	stalwart := NewSMTPClient("localhost", 587, "", "", "relay@club.example", "")
	if h := stalwart.withSESHeaders(ctx, nil); len(h) != 0 {
		t.Errorf("Stalwart client added headers %v", h)
	}
}

// relay is a scripted one-connection SMTP server. rcpt and dataEnd are the
// replies to RCPT TO and to the end of DATA; "drop" closes the connection
// instead of answering.
type relay struct {
	rcpt, dataEnd string

	mu   sync.Mutex
	body string
}

func (rl *relay) start(t *testing.T) (string, int) {
	return fakeSMTP(t, func(c net.Conn, r *bufio.Reader) {
		write := func(s string) { _, _ = c.Write([]byte(s + "\r\n")) }
		write("220 fake ESMTP")
		var data strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					rl.mu.Lock()
					rl.body = data.String()
					rl.mu.Unlock()
					if rl.dataEnd == "drop" {
						return
					}
					write(rl.dataEnd)
					inData = false
					continue
				}
				data.WriteString(line)
				continue
			}
			switch cmd := strings.ToUpper(strings.TrimRight(line, "\r\n")); {
			case strings.HasPrefix(cmd, "RCPT"):
				write(rl.rcpt)
			case cmd == "DATA":
				inData = true
				write("354 go ahead")
			case cmd == "QUIT":
				write("221 bye")
				return
			default:
				write("250 ok")
			}
		}
	})
}

func (rl *relay) received() string {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return rl.body
}

func TestSendClassifiesFailures(t *testing.T) {
	for _, tc := range []struct {
		name          string
		rcpt, dataEnd string
		wantErr       bool
		wantAmbiguous bool
	}{
		{name: "accepted", rcpt: "250 ok", dataEnd: "250 Ok 0100-abc-000000"},
		{name: "rcpt refused", rcpt: "550 5.1.1 no such user", dataEnd: "250 ok", wantErr: true},
		{name: "refused at end of data", rcpt: "250 ok", dataEnd: "554 Message rejected", wantErr: true},
		{name: "connection lost at end of data", rcpt: "250 ok", dataEnd: "drop", wantErr: true, wantAmbiguous: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rl := &relay{rcpt: tc.rcpt, dataEnd: tc.dataEnd}
			host, port := rl.start(t)
			c := NewSMTPClient(host, port, "", "", "relay@club.example", "")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			err := c.Send(ctx, "to@example.com", "Hi", "<p>x</p>")
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			if got := errors.Is(err, ErrDeliveryAmbiguous); got != tc.wantAmbiguous {
				t.Errorf("ambiguous = %v, want %v (err %v)", got, tc.wantAmbiguous, err)
			}
		})
	}
}

func TestSESSendTagsMessageAndCapturesMessageID(t *testing.T) {
	rl := &relay{rcpt: "250 ok", dataEnd: "250 Ok 0100018f-test-000000"}
	host, port := rl.start(t)
	c := NewSMTPClient(host, port, "", "", "relay@club.example", "", WithSESConfigurationSet("brygge-test"))
	a := &attempt{sendID: testSendID}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Send(withAttempt(WithKind(ctx, KindMagicLink), a), "to@example.com", "Hi", "<p>x</p>"); err != nil {
		t.Fatal(err)
	}
	if a.messageID != "0100018f-test-000000" {
		t.Errorf("message id = %q", a.messageID)
	}
	body := rl.received()
	for _, want := range []string{
		"X-Ses-Configuration-Set: brygge-test\r\n",
		"X-Ses-Message-Tags: send_id=" + testSendID + ", kind=magic_link\r\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("message lacks %q:\n%s", want, body)
		}
	}
}
