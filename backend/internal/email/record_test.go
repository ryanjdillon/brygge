package email

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/rs/zerolog"
)

type fakeSendLog struct {
	startErr error
	started  []LoggedSend
	finished []finishCall
}

type finishCall struct{ id, status, messageID, errText string }

func (f *fakeSendLog) Start(_ context.Context, s LoggedSend) error {
	if f.startErr != nil {
		return f.startErr
	}
	f.started = append(f.started, s)
	return nil
}

func (f *fakeSendLog) Finish(_ context.Context, id, status, messageID, errText string) error {
	f.finished = append(f.finished, finishCall{id, status, messageID, errText})
	return nil
}

// transport stands in for SMTPClient: it reports the send_id it was handed
// and a message id, then returns err.
type transport struct {
	MockSender
	sawSendID string
}

func (t *transport) Send(ctx context.Context, to, subject, html string) error {
	if a := attemptFrom(ctx); a != nil {
		t.sawSendID = a.sendID
		a.messageID = "ses-msg-1"
	}
	return t.MockSender.Send(ctx, to, subject, html)
}

func TestRecordStatusTransitions(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err        error
		wantStatus string
	}{
		{"submitted", nil, SendStatusSubmitted},
		{"failed", errors.New("smtp RCPT TO: 550"), SendStatusFailed},
		{"unknown", fmt.Errorf("smtp close DATA: %w: EOF", ErrDeliveryAmbiguous), SendStatusUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := &fakeSendLog{}
			tr := &transport{MockSender: MockSender{Err: tc.err}}
			s := Record(tr, ProviderSES, log, zerolog.Nop())
			meta := SendMeta{ClubID: "c", Kind: KindInvoice, RelatedType: "invoice", RelatedID: "i"}

			err := s.Send(WithSendMeta(context.Background(), meta), "Member@Example.NET", "s", "b")
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want the transport's error unchanged", err)
			}
			if len(log.started) != 1 || len(log.finished) != 1 {
				t.Fatalf("started %d, finished %d; want one each", len(log.started), len(log.finished))
			}
			st, fin := log.started[0], log.finished[0]
			if st.Meta != meta || st.Provider != ProviderSES || st.Recipient != "Member@Example.NET" {
				t.Errorf("started %+v", st)
			}
			if tr.sawSendID != st.ID || fin.id != st.ID {
				t.Errorf("send_id: row %s, transport %s, finish %s", st.ID, tr.sawSendID, fin.id)
			}
			if fin.status != tc.wantStatus || fin.messageID != "ses-msg-1" {
				t.Errorf("finish = %+v, want status %s", fin, tc.wantStatus)
			}
			if (fin.errText != "") != (tc.err != nil) {
				t.Errorf("error text %q for err %v", fin.errText, tc.err)
			}
		})
	}
}

// A send the log cannot file still goes out, without a send_id.
func TestRecordSendsWhenRowCannotBeWritten(t *testing.T) {
	log := &fakeSendLog{startErr: ErrNoClub}
	tr := &transport{}
	s := Record(tr, ProviderStalwart, log, zerolog.Nop())

	if err := s.Send(context.Background(), "a@example.net", "s", "b"); err != nil {
		t.Fatal(err)
	}
	if len(tr.Calls) != 1 || tr.sawSendID != "" || len(log.finished) != 0 {
		t.Errorf("calls %d, send_id %q, finished %d", len(tr.Calls), tr.sawSendID, len(log.finished))
	}
}

func TestRecordKindFallsBackToWithKind(t *testing.T) {
	log := &fakeSendLog{}
	s := Record(&MockSender{}, ProviderSES, log, zerolog.Nop())
	_ = s.Send(WithKind(context.Background(), KindMagicLink), "a@example.net", "s", "b")
	if log.started[0].Meta.Kind != KindMagicLink {
		t.Errorf("kind = %q", log.started[0].Meta.Kind)
	}
}

func TestRecordNil(t *testing.T) {
	if Record(nil, ProviderSES, &fakeSendLog{}, zerolog.Nop()) != nil {
		t.Fatal("Record(nil) must stay nil so handlers' nil checks still disable email")
	}
}

func TestTruncateKeepsRunes(t *testing.T) {
	if got := truncate("abæ", 3); got != "ab" {
		t.Errorf("truncate = %q, want a cut before the split rune", got)
	}
	if got := truncate("abc", 5); got != "abc" {
		t.Errorf("truncate = %q", got)
	}
}

func TestMagicLinkFallback(t *testing.T) {
	refused := errors.New("smtp RCPT TO: 554 rejected")
	ambiguous := fmt.Errorf("smtp close DATA: %w: EOF", ErrDeliveryAmbiguous)
	for _, tc := range []struct {
		name         string
		kind         Kind
		primaryErr   error
		wantFallback bool
	}{
		{"magic link refused", KindMagicLink, refused, true},
		{"magic link ambiguous", KindMagicLink, ambiguous, false},
		{"magic link ok", KindMagicLink, nil, false},
		{"invoice refused", KindInvoice, refused, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			primary := &MockSender{Err: tc.primaryErr}
			fallback := &MockSender{}
			s := WithMagicLinkFallback(primary, fallback, zerolog.Nop())

			err := s.Send(WithSendMeta(context.Background(), SendMeta{Kind: tc.kind}), "a@example.net", "s", "b")
			if got := len(fallback.Calls) == 1; got != tc.wantFallback {
				t.Fatalf("fallback used = %v, want %v", got, tc.wantFallback)
			}
			switch {
			case tc.wantFallback && err != nil:
				t.Errorf("fallback succeeded but err = %v", err)
			case !tc.wantFallback && !errors.Is(err, tc.primaryErr):
				t.Errorf("err = %v, want the primary's error", err)
			}
		})
	}

	// Both relays failing reports both.
	primary, fallback := &MockSender{Err: refused}, &MockSender{Err: ambiguous}
	err := WithMagicLinkFallback(primary, fallback, zerolog.Nop()).
		Send(WithKind(context.Background(), KindMagicLink), "a@example.net", "s", "b")
	if !errors.Is(err, refused) || !errors.Is(err, ErrDeliveryAmbiguous) {
		t.Errorf("err = %v, want both failures", err)
	}

	if WithMagicLinkFallback(primary, nil, zerolog.Nop()) != Sender(primary) {
		t.Error("no fallback must return primary unwrapped")
	}
}
