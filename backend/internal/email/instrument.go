package email

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Kind classifies an outbound message for the brygge.email.send counter.
// The set is closed so the metric's cardinality stays fixed: anything not
// tagged via WithKind is counted as KindOther.
type Kind string

const (
	KindMagicLink       Kind = "magic_link"
	KindInvoice         Kind = "invoice"
	KindInvoiceReminder Kind = "invoice_reminder"
	KindOther           Kind = "other"
)

var allKinds = []Kind{KindMagicLink, KindInvoice, KindInvoiceReminder, KindOther}

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
	for _, known := range allKinds {
		if k == known {
			return k
		}
	}
	return KindOther
}

// Instrument wraps next so every send increments brygge.email.send with
// the context's kind and the outcome. Attributes are deliberately limited
// to kind and result: recipients, subjects and error text would leak
// member data into the metrics backend and explode cardinality. Returns
// nil when next is nil so callers can keep their "email not configured"
// nil checks.
//
// Call after the global MeterProvider is installed (telemetry.Setup): the
// zero-valued series created here are recorded against whichever provider
// is current.
func Instrument(next Sender) Sender {
	if next == nil {
		return nil
	}
	return instrument(next, otel.Meter("brygge-api"))
}

func instrument(next Sender, meter metric.Meter) Sender {
	sends, err := meter.Int64Counter("brygge.email.send",
		metric.WithDescription("Outbound app emails handed to the SMTP relay, by kind and result"),
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
			sends.Add(ctx, 0, metric.WithAttributes(kindAttr(k), resultAttr(r)))
		}
	}
	return &instrumentedSender{next: next, sends: sends}
}

func kindAttr(k Kind) attribute.KeyValue     { return attribute.String("kind", string(k)) }
func resultAttr(r string) attribute.KeyValue { return attribute.String("result", r) }

type instrumentedSender struct {
	next  Sender
	sends metric.Int64Counter
}

var _ Sender = (*instrumentedSender)(nil)

func (s *instrumentedSender) record(ctx context.Context, err error) error {
	result := resultOK
	if err != nil {
		result = resultError
	}
	s.sends.Add(ctx, 1, metric.WithAttributes(kindAttr(kindFrom(ctx)), resultAttr(result)))
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
