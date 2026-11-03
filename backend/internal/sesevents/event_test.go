package sesevents

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Fixtures in testdata/ follow the documented SES event-publishing
// examples, with synthetic addresses and the send_id / kind tags the
// sender sets.
// https://docs.aws.amazon.com/ses/latest/dg/event-publishing-retrieving-sns-examples.html

const (
	fixtureSendID    = "3f1c2a9e-7b4d-4e2a-9c61-0d5b8e7f1a23"
	fixtureMessageID = "0100018b2f3c4d5e-6f7a8b9c-0d1e-2f3a-4b5c-6d7e8f9a0b1c-000000"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestParse(t *testing.T) {
	mailAt := ts("2026-11-03T10:00:00.123Z")
	tests := []struct {
		file string
		want []Record
	}{
		{"send.json", []Record{{
			EventType: EventSend, EventAt: mailAt, Recipient: "member@example.net",
			Status: StatusQueued,
		}}},
		{"delivery.json", []Record{{
			EventType: EventDelivery, EventAt: ts("2026-11-03T10:00:02.456Z"),
			Recipient: "member@example.net", SMTPStatus: "2.6.0",
			Diagnostic: "250 2.6.0 Message received", RemoteMTA: "198.51.100.25",
			Status: StatusAccepted,
		}}},
		{"delivery_delay.json", []Record{{
			EventType: EventDeliveryDelay, EventAt: ts("2026-11-03T10:10:00.095Z"),
			Recipient: "member@example.net", DelayType: "TransientCommunicationFailure",
			SMTPStatus: "4.4.1", Diagnostic: "smtp; 421 4.4.1 Unable to connect to remote host",
			RemoteMTA: "198.51.100.9", Status: StatusDeferred,
		}}},
		{"bounce_permanent.json", []Record{{
			EventType: EventBounce, EventAt: ts("2026-11-03T10:00:03.669Z"),
			Recipient: "member@example.net", BounceType: "Permanent", BounceSubType: "General",
			SMTPStatus: "5.1.1", Diagnostic: "smtp; 550 5.1.1 user unknown",
			RemoteMTA: "mx.example.net", Status: StatusRejected,
		}}},
		{"bounce_suppressed.json", []Record{{
			EventType: EventBounce, EventAt: ts("2026-11-03T10:00:00.900Z"),
			Recipient: "member@example.net", BounceType: "Permanent",
			BounceSubType: "OnAccountSuppressionList", SMTPStatus: "5.1.1",
			Diagnostic: "Amazon SES did not send the message to this address because it is on the suppression list for your account.",
			RemoteMTA:  "amazonses.com", Status: StatusSuppressed,
		}}},
		{"bounce_multi.json", []Record{
			{
				EventType: EventBounce, EventAt: ts("2026-11-03T10:00:04Z"),
				Recipient: "first@example.net", BounceType: "Transient", BounceSubType: "MailboxFull",
				SMTPStatus: "4.2.2", Diagnostic: "smtp; 452 4.2.2 mailbox full",
				RemoteMTA: "mx.example.net", Status: StatusRejectedTemporary,
			},
			{
				EventType: EventBounce, EventAt: ts("2026-11-03T10:00:04Z"),
				Recipient: "Second@Example.ORG", BounceType: "Transient", BounceSubType: "MailboxFull",
				SMTPStatus: "4.2.2", Diagnostic: "smtp; 452 4.2.2 over quota",
				RemoteMTA: "mx.example.net", Status: StatusRejectedTemporary,
			},
		}},
		{"complaint.json", []Record{{
			EventType: EventComplaint, EventAt: ts("2026-11-04T08:41:02.669Z"),
			Recipient: "member@example.net", Diagnostic: "abuse", Status: StatusComplaint,
		}}},
		{"reject.json", []Record{{
			EventType: EventReject, EventAt: mailAt, Recipient: "member@example.net",
			Diagnostic: "Bad content", Status: StatusRejected,
		}}},
		{"rendering_failure.json", []Record{{
			EventType: EventRenderingFailure, EventAt: mailAt, Recipient: "member@example.net",
			Diagnostic: "Attribute 'attributeName' is not present in the rendering data.",
			Status:     StatusRejected,
		}}},
		{"sns_envelope.json", []Record{{
			EventType: EventDelivery, EventAt: ts("2026-11-03T10:00:02.456Z"),
			Recipient: "member@example.net", SMTPStatus: "2.6.0",
			Diagnostic: "250 2.6.0 Message received", RemoteMTA: "198.51.100.25",
			Status: StatusAccepted,
		}}},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			got, err := Parse(loadFixture(t, tt.file))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d records, want %d", len(got), len(tt.want))
			}
			for i, w := range tt.want {
				g := got[i]
				if len(g.Raw) == 0 || g.Raw[0] != '{' {
					t.Errorf("record %d: Raw is not the event JSON", i)
				}
				if g.SESMessageID != fixtureMessageID {
					t.Errorf("record %d: SESMessageID = %q", i, g.SESMessageID)
				}
				if g.SendIDTag != fixtureSendID {
					t.Errorf("record %d: SendIDTag = %q", i, g.SendIDTag)
				}
				if g.Kind != "invoice" {
					t.Errorf("record %d: Kind = %q", i, g.Kind)
				}
				g.Raw, g.SESMessageID, g.SendIDTag, g.Kind = nil, "", "", ""
				if !g.EventAt.Equal(w.EventAt) {
					t.Errorf("record %d: EventAt = %v, want %v", i, g.EventAt, w.EventAt)
				}
				if !recordEqual(g, w) {
					t.Errorf("record %d:\n got  %+v\n want %+v", i, g, w)
				}
			}
		})
	}
}

func recordEqual(a, b Record) bool {
	return a.EventType == b.EventType && a.Recipient == b.Recipient &&
		a.BounceType == b.BounceType && a.BounceSubType == b.BounceSubType &&
		a.DelayType == b.DelayType && a.SMTPStatus == b.SMTPStatus &&
		a.Diagnostic == b.Diagnostic && a.RemoteMTA == b.RemoteMTA &&
		a.Status == b.Status && a.EventAt.Equal(b.EventAt)
}

func TestParseSNSEnvelopeKeepsInnerEventAsRaw(t *testing.T) {
	body := loadFixture(t, "sns_envelope.json")
	var env struct{ Message string }
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	got, err := Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got[0].Raw) != env.Message {
		t.Fatalf("Raw = %.60s…, want the SES event inside the envelope", got[0].Raw)
	}
}

func TestParseMissingSendIDTag(t *testing.T) {
	got, err := Parse(loadFixture(t, "delivery_no_send_id.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got[0].SendIDTag != "" {
		t.Errorf("SendIDTag = %q, want empty", got[0].SendIDTag)
	}
	if got[0].Kind != "other" {
		t.Errorf("Kind = %q, want other", got[0].Kind)
	}
	if got[0].Status != StatusAccepted {
		t.Errorf("Status = %q", got[0].Status)
	}
}

func TestParseIgnoredEventType(t *testing.T) {
	_, err := Parse(loadFixture(t, "open.json"))
	if !errors.Is(err, ErrIgnoredEventType) {
		t.Fatalf("err = %v, want ErrIgnoredEventType", err)
	}
}

func TestParseErrors(t *testing.T) {
	tests := map[string]string{
		"not json":          `not json`,
		"no event type":     `{"mail":{"messageId":"m","timestamp":"2026-11-03T10:00:00Z"}}`,
		"no message id":     `{"eventType":"Send","mail":{"timestamp":"2026-11-03T10:00:00Z"},"send":{}}`,
		"bad mail time":     `{"eventType":"Send","mail":{"messageId":"m","timestamp":"yesterday"},"send":{}}`,
		"bounce w/o object": `{"eventType":"Bounce","mail":{"messageId":"m","timestamp":"2026-11-03T10:00:00Z"}}`,
		"bad event time":    `{"eventType":"Delivery","mail":{"messageId":"m","timestamp":"2026-11-03T10:00:00Z"},"delivery":{"timestamp":"soon"}}`,
		"envelope bad body": `{"Type":"Notification","Message":"{broken"}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(body))
			if err == nil {
				t.Fatal("want error")
			}
			if errors.Is(err, ErrIgnoredEventType) {
				t.Fatal("malformed event must not be treated as ignorable")
			}
		})
	}
}

func TestParseNoRecipientsKeepsOneRecord(t *testing.T) {
	got, err := Parse([]byte(`{"eventType":"Send","mail":{"messageId":"m","timestamp":"2026-11-03T10:00:00Z"},"send":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Recipient != "" {
		t.Fatalf("got %+v, want one record with no recipient", got)
	}
}

func TestRecipientDomain(t *testing.T) {
	tests := map[string]string{
		"member@example.net": "example.net",
		"Second@Example.ORG": "example.org",
		"no-at-sign":         "",
		"trailing@":          "",
		"":                   "",
	}
	for in, want := range tests {
		if got := RecipientDomain(in); got != want {
			t.Errorf("RecipientDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEnhancedStatus(t *testing.T) {
	tests := map[string]string{
		"250 2.6.0 Message received":            "2.6.0",
		"250 Ok":                                "",
		"smtp; 550 5.7.1 rejected by policy":    "5.7.1",
		"250 2.0.0 OK 1730000000 a1-20020a.123": "2.0.0",
	}
	for in, want := range tests {
		if got := enhancedStatus(in); got != want {
			t.Errorf("enhancedStatus(%q) = %q, want %q", in, got, want)
		}
	}
}
