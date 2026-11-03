// Package sesevents consumes Amazon SES delivery events (configuration set
// → SNS → SQS) and records them against the email_sends rows the sender
// wrote. See README.md for the event flow, idempotency and ordering rules.
package sesevents

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Normalised event types, as stored in mail_delivery_events.event_type.
const (
	EventSend             = "send"
	EventDelivery         = "delivery"
	EventDeliveryDelay    = "delivery_delay"
	EventBounce           = "bounce"
	EventComplaint        = "complaint"
	EventReject           = "reject"
	EventRenderingFailure = "rendering_failure"
)

// AllEventTypes lists every event type the worker records.
var AllEventTypes = []string{
	EventSend, EventDelivery, EventDeliveryDelay, EventBounce,
	EventComplaint, EventReject, EventRenderingFailure,
}

// sesEventTypes maps the eventType values SES publishes to the stored
// names. Open, Click and Subscription are absent on purpose: the
// configuration set does not enable them (no open/click tracking).
// https://docs.aws.amazon.com/ses/latest/dg/event-publishing-retrieving-sns-contents.html
var sesEventTypes = map[string]string{
	"Send":              EventSend,
	"Delivery":          EventDelivery,
	"DeliveryDelay":     EventDeliveryDelay,
	"Bounce":            EventBounce,
	"Complaint":         EventComplaint,
	"Reject":            EventReject,
	"Rendering Failure": EventRenderingFailure,
}

// Tag names the sender sets through X-SES-MESSAGE-TAGS.
const (
	TagSendID = "send_id"
	TagKind   = "kind"
)

// ErrIgnoredEventType marks a well-formed event of a type the worker does
// not record. The message is deleted rather than left for the DLQ, since
// retrying it can never succeed and it is not a fault.
var ErrIgnoredEventType = errors.New("sesevents: ignored event type")

// Event is the SES event-publishing record. Only the fields the worker
// stores are modelled; the full document is kept verbatim in Raw.
// https://docs.aws.amazon.com/ses/latest/dg/event-publishing-retrieving-sns-contents.html
type Event struct {
	EventType     string         `json:"eventType"`
	Mail          Mail           `json:"mail"`
	Bounce        *Bounce        `json:"bounce,omitempty"`
	Complaint     *Complaint     `json:"complaint,omitempty"`
	Delivery      *Delivery      `json:"delivery,omitempty"`
	Send          *struct{}      `json:"send,omitempty"`
	Reject        *Reject        `json:"reject,omitempty"`
	DeliveryDelay *DeliveryDelay `json:"deliveryDelay,omitempty"`
	Failure       *Failure       `json:"failure,omitempty"`
}

// Mail is the message the event is about.
type Mail struct {
	MessageID     string              `json:"messageId"`
	Timestamp     string              `json:"timestamp"`
	Destination   []string            `json:"destination"`
	Tags          map[string][]string `json:"tags"`
	CommonHeaders CommonHeaders       `json:"commonHeaders"`
}

// CommonHeaders are the parsed headers SES echoes back.
type CommonHeaders struct {
	From      []string `json:"from"`
	To        []string `json:"to"`
	MessageID string   `json:"messageId"`
	Subject   string   `json:"subject"`
}

// Bounce is present when eventType is Bounce.
type Bounce struct {
	BounceType        string             `json:"bounceType"`
	BounceSubType     string             `json:"bounceSubType"`
	BouncedRecipients []BouncedRecipient `json:"bouncedRecipients"`
	Timestamp         string             `json:"timestamp"`
	ReportingMTA      string             `json:"reportingMTA"`
}

// BouncedRecipient is one address in a bounce.
type BouncedRecipient struct {
	EmailAddress   string `json:"emailAddress"`
	Action         string `json:"action"`
	Status         string `json:"status"`
	DiagnosticCode string `json:"diagnosticCode"`
}

// Complaint is present when eventType is Complaint.
type Complaint struct {
	ComplainedRecipients  []ComplainedRecipient `json:"complainedRecipients"`
	Timestamp             string                `json:"timestamp"`
	ComplaintFeedbackType string                `json:"complaintFeedbackType"`
	// ComplaintSubType is null, or OnAccountSuppressionList /
	// OnTenantSuppressionList when SES accepted the message but did not
	// send it because a past complaint put the address on a suppression
	// list. Such an event is a suppression, not a new complaint.
	ComplaintSubType string `json:"complaintSubType"`
}

// ComplainedRecipient is one address in a complaint.
type ComplainedRecipient struct {
	EmailAddress string `json:"emailAddress"`
}

// Delivery is present when eventType is Delivery.
type Delivery struct {
	Timestamp    string   `json:"timestamp"`
	Recipients   []string `json:"recipients"`
	SMTPResponse string   `json:"smtpResponse"`
	RemoteMtaIP  string   `json:"remoteMtaIp"`
	ReportingMTA string   `json:"reportingMTA"`
}

// Reject is present when eventType is Reject.
type Reject struct {
	Reason string `json:"reason"`
}

// DeliveryDelay is present when eventType is DeliveryDelay.
type DeliveryDelay struct {
	DelayType         string             `json:"delayType"`
	ExpirationTime    string             `json:"expirationTime"`
	DelayedRecipients []DelayedRecipient `json:"delayedRecipients"`
	Timestamp         string             `json:"timestamp"`
	ReportingMTA      string             `json:"reportingMTA"`
}

// DelayedRecipient is one address in a delivery delay.
type DelayedRecipient struct {
	EmailAddress   string `json:"emailAddress"`
	Status         string `json:"status"`
	DiagnosticCode string `json:"diagnosticCode"`
}

// Failure is present when eventType is "Rendering Failure".
type Failure struct {
	ErrorMessage string `json:"errorMessage"`
	TemplateName string `json:"templateName"`
}

// Record is one per-recipient row for mail_delivery_events, plus the
// derived email_sends status it implies. Empty strings are stored as NULL.
type Record struct {
	SESMessageID  string
	EventType     string
	EventAt       time.Time
	Recipient     string
	BounceType    string
	BounceSubType string
	DelayType     string
	SMTPStatus    string
	Diagnostic    string
	// RemoteMTA is the IP of the receiving server SES handed the message
	// to; only Delivery events carry it. The reportingMTA of Bounce and
	// DeliveryDelay events names the MTA that reported the outcome, which
	// is SES's own for the bounces it generates, so it stays in raw only.
	RemoteMTA string
	Raw       []byte
	// ComplaintSubType steers StatusFor for Complaint records; it is not
	// a column (raw keeps it).
	ComplaintSubType string

	// SendIDTag is the raw send_id tag; the store validates it.
	SendIDTag string
	// Kind is the kind tag, normalised to the closed metric set.
	Kind string
	// Status is the email_sends status this event implies.
	Status string
}

// snsEnvelope is the SNS notification wrapper. The subscription uses raw
// message delivery, so the SQS body is normally the SES event itself; the
// envelope is tolerated so a subscription that loses that setting still
// works.
// https://docs.aws.amazon.com/sns/latest/dg/sns-large-payload-raw-message-delivery.html
type snsEnvelope struct {
	Type    string `json:"Type"`
	Message string `json:"Message"`
}

// Parse decodes an SQS message body into per-recipient records. It returns
// ErrIgnoredEventType (wrapped) for event types the worker does not store.
func Parse(body []byte) ([]Record, error) {
	raw, err := unwrapSNS(body)
	if err != nil {
		return nil, err
	}

	var ev Event
	if err := json.Unmarshal(raw, &ev); err != nil {
		return nil, fmt.Errorf("sesevents: decode event: %w", err)
	}
	if ev.EventType == "" {
		return nil, errors.New("sesevents: missing eventType")
	}
	eventType, ok := sesEventTypes[ev.EventType]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrIgnoredEventType, ev.EventType)
	}
	if ev.Mail.MessageID == "" {
		return nil, errors.New("sesevents: missing mail.messageId")
	}
	mailAt, err := parseTime(ev.Mail.Timestamp)
	if err != nil {
		return nil, fmt.Errorf("sesevents: mail.timestamp: %w", err)
	}

	base := Record{
		SESMessageID: ev.Mail.MessageID,
		EventType:    eventType,
		EventAt:      mailAt,
		Raw:          raw,
		SendIDTag:    firstTag(ev.Mail.Tags, TagSendID),
		Kind:         normaliseKind(firstTag(ev.Mail.Tags, TagKind)),
	}

	var recs []Record
	switch eventType {
	case EventSend, EventReject, EventRenderingFailure:
		recs = messageRecords(&ev, base)
	case EventDelivery:
		recs, err = deliveryRecords(&ev, base)
	case EventDeliveryDelay:
		recs, err = delayRecords(&ev, base)
	case EventBounce:
		recs, err = bounceRecords(&ev, base)
	case EventComplaint:
		recs, err = complaintRecords(&ev, base)
	}
	if err != nil {
		return nil, err
	}
	for i := range recs {
		recs[i].Status = StatusFor(recs[i])
	}
	return recs, nil
}

func unwrapSNS(body []byte) ([]byte, error) {
	var env snsEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("sesevents: decode body: %w", err)
	}
	if env.Type == "Notification" && env.Message != "" {
		return []byte(env.Message), nil
	}
	return body, nil
}

// messageRecords covers Send, Reject and Rendering Failure, which describe
// the whole message and carry no timestamp of their own; SES stamps them
// when it accepted the message, which is mail.timestamp.
func messageRecords(ev *Event, base Record) []Record {
	diag := ""
	if ev.Reject != nil {
		diag = ev.Reject.Reason
	}
	if ev.Failure != nil {
		diag = ev.Failure.ErrorMessage
	}
	var recs []Record
	for _, to := range recipientsOrNone(ev.Mail.Destination) {
		r := base
		r.Recipient = to
		r.Diagnostic = diag
		recs = append(recs, r)
	}
	return recs
}

func deliveryRecords(ev *Event, base Record) ([]Record, error) {
	d := ev.Delivery
	if d == nil {
		return nil, errors.New("sesevents: Delivery event without delivery object")
	}
	at, err := eventTime(d.Timestamp, base.EventAt)
	if err != nil {
		return nil, err
	}
	to := d.Recipients
	if len(to) == 0 {
		to = ev.Mail.Destination
	}
	var recs []Record
	for _, addr := range recipientsOrNone(to) {
		r := base
		r.EventAt = at
		r.Recipient = addr
		r.SMTPStatus = enhancedStatus(d.SMTPResponse)
		r.Diagnostic = d.SMTPResponse
		r.RemoteMTA = d.RemoteMtaIP
		recs = append(recs, r)
	}
	return recs, nil
}

func delayRecords(ev *Event, base Record) ([]Record, error) {
	d := ev.DeliveryDelay
	if d == nil {
		return nil, errors.New("sesevents: DeliveryDelay event without deliveryDelay object")
	}
	at, err := eventTime(d.Timestamp, base.EventAt)
	if err != nil {
		return nil, err
	}
	delayed := d.DelayedRecipients
	if len(delayed) == 0 {
		for _, addr := range recipientsOrNone(ev.Mail.Destination) {
			delayed = append(delayed, DelayedRecipient{EmailAddress: addr})
		}
	}
	var recs []Record
	for _, dr := range delayed {
		r := base
		r.EventAt = at
		r.Recipient = dr.EmailAddress
		r.DelayType = d.DelayType
		r.SMTPStatus = dr.Status
		r.Diagnostic = dr.DiagnosticCode
		recs = append(recs, r)
	}
	return recs, nil
}

func bounceRecords(ev *Event, base Record) ([]Record, error) {
	b := ev.Bounce
	if b == nil {
		return nil, errors.New("sesevents: Bounce event without bounce object")
	}
	at, err := eventTime(b.Timestamp, base.EventAt)
	if err != nil {
		return nil, err
	}
	bounced := b.BouncedRecipients
	if len(bounced) == 0 {
		for _, addr := range recipientsOrNone(ev.Mail.Destination) {
			bounced = append(bounced, BouncedRecipient{EmailAddress: addr})
		}
	}
	var recs []Record
	for _, br := range bounced {
		r := base
		r.EventAt = at
		r.Recipient = br.EmailAddress
		r.BounceType = b.BounceType
		r.BounceSubType = b.BounceSubType
		r.SMTPStatus = br.Status
		r.Diagnostic = br.DiagnosticCode
		recs = append(recs, r)
	}
	return recs, nil
}

func complaintRecords(ev *Event, base Record) ([]Record, error) {
	c := ev.Complaint
	if c == nil {
		return nil, errors.New("sesevents: Complaint event without complaint object")
	}
	at, err := eventTime(c.Timestamp, base.EventAt)
	if err != nil {
		return nil, err
	}
	complained := c.ComplainedRecipients
	if len(complained) == 0 {
		for _, addr := range recipientsOrNone(ev.Mail.Destination) {
			complained = append(complained, ComplainedRecipient{EmailAddress: addr})
		}
	}
	var recs []Record
	for _, cr := range complained {
		r := base
		r.EventAt = at
		r.Recipient = cr.EmailAddress
		r.Diagnostic = c.ComplaintFeedbackType
		r.ComplaintSubType = c.ComplaintSubType
		recs = append(recs, r)
	}
	return recs, nil
}

// RecipientDomain returns the lower-cased part after the last "@", or ""
// when addr has none.
func RecipientDomain(addr string) string {
	i := strings.LastIndexByte(addr, '@')
	if i < 0 || i == len(addr)-1 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(addr[i+1:]))
}

func firstTag(tags map[string][]string, key string) string {
	if v := tags[key]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// recipientsOrNone keeps one record per message even when SES omits the
// recipient list, so the event is still stored (with a NULL recipient).
func recipientsOrNone(addrs []string) []string {
	if len(addrs) == 0 {
		return []string{""}
	}
	return addrs
}

func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, errors.New("empty timestamp")
	}
	return time.Parse(time.RFC3339Nano, s)
}

func eventTime(s string, fallback time.Time) (time.Time, error) {
	if s == "" {
		return fallback, nil
	}
	t, err := parseTime(s)
	if err != nil {
		return time.Time{}, fmt.Errorf("sesevents: event timestamp: %w", err)
	}
	return t, nil
}

var enhancedStatusRE = regexp.MustCompile(`\b[245]\.\d{1,3}\.\d{1,3}\b`)

// enhancedStatus pulls the RFC 3463 enhanced status code (e.g. 2.6.0) out
// of a free-text SMTP response.
func enhancedStatus(resp string) string {
	return enhancedStatusRE.FindString(resp)
}
