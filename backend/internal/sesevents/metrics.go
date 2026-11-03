package sesevents

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// event_type labels for messages that produce no records: one that could
// not be parsed, and a well-formed event of a type the worker skips.
const (
	eventTypeInvalid = "invalid"
	eventTypeIgnored = "ignored"
)

// instruments holds the worker's metrics. Attributes are limited to small
// closed sets (kind, mapped status, domain class, event type): recipient
// addresses or raw domains would put member data into the metrics backend.
type instruments struct {
	delivery metric.Int64Counter
	received metric.Int64Counter
	dlq      metric.Int64Gauge
}

// newInstruments registers the worker's metrics on meter. Call after the
// global MeterProvider is installed (telemetry.Setup). A registration error
// is reported to otel and leaves that instrument a no-op.
func newInstruments(meter metric.Meter) *instruments {
	in := &instruments{}
	var err error
	if in.delivery, err = meter.Int64Counter("brygge.email.delivery",
		metric.WithDescription("Delivery outcomes SES reported for app email, by kind, mapped status, recipient domain class and SMTP status class"),
		metric.WithUnit("{event}"),
	); err != nil {
		otel.Handle(err)
	}
	if in.received, err = meter.Int64Counter("brygge.ses.events.received",
		metric.WithDescription("SES event messages taken off the SQS queue, by event type"),
		metric.WithUnit("{message}"),
	); err != nil {
		otel.Handle(err)
	}
	if in.dlq, err = meter.Int64Gauge("brygge.ses.dlq.messages",
		metric.WithDescription("Approximate number of messages in the SES events dead-letter queue"),
		metric.WithUnit("{message}"),
	); err != nil {
		otel.Handle(err)
	}

	// Pre-create every series at 0 so the first bounce or complaint is a
	// visible increase rather than a series that appears from nothing.
	ctx := context.Background()
	if in.delivery != nil {
		for _, k := range Kinds {
			for _, s := range EventStatuses {
				for _, d := range DomainClasses {
					in.delivery.Add(ctx, 0, metric.WithAttributes(deliveryAttrs(k, s, d, smtpStatusNone)...))
				}
			}
		}
	}
	if in.received != nil {
		for _, t := range append(append([]string{}, AllEventTypes...), eventTypeInvalid, eventTypeIgnored) {
			in.received.Add(ctx, 0, metric.WithAttributes(attribute.String("event_type", t)))
		}
	}
	return in
}

func defaultInstruments() *instruments { return newInstruments(otel.Meter("brygge-api")) }

const smtpStatusNone = "none"

// smtpStatusClass reduces an RFC 3463 enhanced status such as "5.1.1" to its
// class and subject ("5.1"). An alert can then tell an unknown mailbox (5.1)
// from a policy refusal (5.7) without the label's values growing past the
// few dozen the RFC defines; the full code and diagnostic stay in
// mail_delivery_events.
func smtpStatusClass(status string) string {
	if len(status) >= 3 && (status[0] == '2' || status[0] == '4' || status[0] == '5') &&
		status[1] == '.' && status[2] >= '0' && status[2] <= '7' &&
		(len(status) == 3 || status[3] == '.') {
		return status[:3]
	}
	return smtpStatusNone
}

func deliveryAttrs(kind, result, domainClass, statusClass string) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("kind", kind),
		attribute.String("result", result),
		attribute.String("recipient_domain_class", domainClass),
		attribute.String("smtp_status_class", statusClass),
	}
}

func (in *instruments) recordReceived(ctx context.Context, eventType string) {
	if in.received != nil {
		in.received.Add(ctx, 1, metric.WithAttributes(attribute.String("event_type", eventType)))
	}
}

func (in *instruments) recordDelivery(ctx context.Context, r Record) {
	if in.delivery != nil {
		in.delivery.Add(ctx, 1, metric.WithAttributes(
			deliveryAttrs(r.Kind, r.Status, DomainClass(RecipientDomain(r.Recipient)), smtpStatusClass(r.SMTPStatus))...))
	}
}

func (in *instruments) recordDLQ(ctx context.Context, n int64) {
	if in.dlq != nil {
		in.dlq.Record(ctx, n)
	}
}
