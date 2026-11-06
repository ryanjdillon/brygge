package email

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Kind classifies an outbound message for the brygge.email.send counter, the
// email_sends log and the SES kind tag. The set is closed so the metric's
// cardinality stays fixed: anything not tagged via WithSendMeta or WithKind
// is counted as KindOther.
type Kind string

const (
	KindMagicLink       Kind = "magic_link"
	KindInvoice         Kind = "invoice"
	KindInvoiceReminder Kind = "invoice_reminder"
	KindCreditNote      Kind = "credit_note"
	KindDispute         Kind = "dispute"
	KindContact         Kind = "contact"
	// KindJoinConfirm is the link that confirms a membership application;
	// KindJoinAlreadyMember is the login link sent instead when the address
	// already belongs to the club.
	KindJoinConfirm       Kind = "join_confirm"
	KindJoinAlreadyMember Kind = "join_already_member"
	// KindJoinContactBoard is sent instead when the board has marked the
	// address "can't rejoin": it points the person to the board.
	KindJoinContactBoard Kind = "join_contact_board"
	// Settling a join payment (BRY-630): the new member's welcome, the
	// board's new-member notice, and the board's notice that a join payment
	// was refunded or credited.
	KindMembershipWelcome      Kind = "membership_welcome"
	KindMembershipBoardNotice  Kind = "membership_board_notice"
	KindMembershipJoinRefunded Kind = "membership_join_refunded"
	// KindMembershipGrantWithheld tells the board a paid join was not
	// granted by the integrity check.
	KindMembershipGrantWithheld Kind = "membership_grant_withheld"
	// Cancelling online (BRY-633): the member's receipt, the board's notices
	// of the cancellation and of its undo, and the board's notice that the
	// year-end deactivation was refused (the member is the only admin).
	KindMembershipCancelReceipt Kind = "membership_cancel_receipt"
	KindMembershipCancelBoard   Kind = "membership_cancel_board_notice"
	KindMembershipCancelUndone  Kind = "membership_cancel_undone"
	KindMembershipEndRefused    Kind = "membership_end_refused"
	KindOther                   Kind = "other"
)

var allKinds = []Kind{
	KindMagicLink, KindInvoice, KindInvoiceReminder,
	KindCreditNote, KindDispute, KindContact,
	KindJoinConfirm, KindJoinAlreadyMember, KindJoinContactBoard,
	KindMembershipWelcome, KindMembershipBoardNotice, KindMembershipJoinRefunded,
	KindMembershipGrantWithheld, KindMembershipCancelReceipt, KindMembershipCancelBoard,
	KindMembershipCancelUndone, KindMembershipEndRefused, KindOther,
}

const (
	resultOK    = "ok"
	resultError = "error"
)

type kindKey struct{}

// WithKind tags ctx so an instrumented Sender counts the send under kind.
func WithKind(ctx context.Context, kind Kind) context.Context {
	return context.WithValue(ctx, kindKey{}, kind)
}

func kindFrom(ctx context.Context) Kind {
	k, _ := ctx.Value(kindKey{}).(Kind)
	if m, ok := SendMetaFrom(ctx); ok && m.Kind != "" {
		k = m.Kind
	}
	for _, known := range allKinds {
		if k == known {
			return k
		}
	}
	return KindOther
}

// Instrument wraps next so every send increments brygge.email.send with
// the context's kind, the relay (provider) and the outcome. Attributes are
// deliberately limited to kind, provider and result: recipients, subjects
// and error text would leak
// member data into the metrics backend and explode cardinality. Returns
// nil when next is nil so callers can keep their "email not configured"
// nil checks.
//
// Call after the global MeterProvider is installed (telemetry.Setup): the
// zero-valued series created here are recorded against whichever provider
// is current.
func Instrument(next Sender, provider Provider) Sender {
	if next == nil {
		return nil
	}
	return instrument(next, provider, otel.Meter("brygge-api"))
}

func instrument(next Sender, provider Provider, meter metric.Meter) Sender {
	sends, err := meter.Int64Counter("brygge.email.send",
		metric.WithDescription("Outbound app emails handed to the SMTP relay, by kind, provider and result"),
		metric.WithUnit("{message}"),
	)
	if err != nil {
		otel.Handle(err)
		return next
	}
	// Pre-create every series at 0 so a rate or increase over the first
	// error is computable; a counter that appears only on its first
	// failure has no prior sample to diff against.
	ctx := context.Background()
	for _, k := range allKinds {
		for _, r := range []string{resultOK, resultError} {
			sends.Add(ctx, 0, metric.WithAttributes(kindAttr(k), providerAttr(provider), resultAttr(r)))
		}
	}
	return &instrumentedSender{next: next, provider: provider, sends: sends}
}

func kindAttr(k Kind) attribute.KeyValue         { return attribute.String("kind", string(k)) }
func providerAttr(p Provider) attribute.KeyValue { return attribute.String("provider", string(p)) }
func resultAttr(r string) attribute.KeyValue     { return attribute.String("result", r) }

type instrumentedSender struct {
	next     Sender
	provider Provider
	sends    metric.Int64Counter
}

var _ Sender = (*instrumentedSender)(nil)

func (s *instrumentedSender) record(ctx context.Context, err error) error {
	result := resultOK
	if err != nil {
		result = resultError
	}
	s.sends.Add(ctx, 1, metric.WithAttributes(kindAttr(kindFrom(ctx)), providerAttr(s.provider), resultAttr(result)))
	return err
}

func (s *instrumentedSender) Send(ctx context.Context, to, subject, htmlBody string) error {
	return s.record(ctx, s.next.Send(ctx, to, subject, htmlBody))
}

func (s *instrumentedSender) SendWithAttachment(ctx context.Context, to, subject, htmlBody, filename string, attachment []byte) error {
	return s.record(ctx, s.next.SendWithAttachment(ctx, to, subject, htmlBody, filename, attachment))
}

func (s *instrumentedSender) SendWithHeaders(ctx context.Context, to, subject, htmlBody string, extraHeaders map[string]string) error {
	return s.record(ctx, s.next.SendWithHeaders(ctx, to, subject, htmlBody, extraHeaders))
}
