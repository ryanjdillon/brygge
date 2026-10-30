package stripe

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Checkout Session statuses.
const (
	SessionOpen     = "open"
	SessionComplete = "complete"
	SessionExpired  = "expired"
)

// PaymentIntent statuses Brygge acts on.
const (
	PaymentIntentRequiresCapture = "requires_capture"
	PaymentIntentSucceeded       = "succeeded"
	PaymentIntentCanceled        = "canceled"
)

// CheckoutSession is the part of a Checkout Session Brygge reads.
type CheckoutSession struct {
	ID string `json:"id"`
	// URL is the hosted payment page; set only while the session is open.
	URL string `json:"url"`
	// Status is open, complete or expired.
	Status string `json:"status"`
	// PaymentStatus is paid, unpaid or no_payment_required. With manual
	// capture a completed card session is unpaid: authorized, not taken.
	PaymentStatus   string            `json:"payment_status"`
	AmountTotal     int64             `json:"amount_total"`
	Currency        string            `json:"currency"`
	Metadata        map[string]string `json:"metadata"`
	CustomerDetails *CustomerDetails  `json:"customer_details"`
	PaymentIntent   *PaymentIntentRef `json:"payment_intent"`
}

// CustomerDetails is what Checkout collected from the payer.
type CustomerDetails struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// PaymentIntentRef is a session's payment_intent, which Stripe sends as a
// bare id unless the request expanded it into the object.
type PaymentIntentRef struct {
	ID string
	// Object is set only when the PaymentIntent was expanded.
	Object *PaymentIntent
}

// UnmarshalJSON accepts both the id string and the expanded object.
func (r *PaymentIntentRef) UnmarshalJSON(b []byte) error {
	if bytes.Equal(b, []byte("null")) {
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &r.ID)
	}
	var pi PaymentIntent
	if err := json.Unmarshal(b, &pi); err != nil {
		return err
	}
	r.ID, r.Object = pi.ID, &pi
	return nil
}

// PaymentIntent is the part of a PaymentIntent Brygge reads.
type PaymentIntent struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Amount int64  `json:"amount"`
	// AmountReceived is what was captured, which a partial capture makes
	// less than Amount.
	AmountReceived int64             `json:"amount_received"`
	Currency       string            `json:"currency"`
	Metadata       map[string]string `json:"metadata"`
	// LatestCharge is the charge that took the money; set on the calls that
	// expand it.
	LatestCharge *ChargeRef `json:"latest_charge"`
}

// CardBrandAndWallet reports the card brand and wallet the PaymentIntent's latest charge
// was paid with, empty when the charge was not expanded or was not a card.
func (pi *PaymentIntent) CardBrandAndWallet() (brand, wallet string) {
	if pi == nil || pi.LatestCharge == nil || pi.LatestCharge.Object == nil {
		return "", ""
	}
	return pi.LatestCharge.Object.PaymentMethodDetails.CardBrandAndWallet()
}

// Charge is the part of a Charge object Brygge reads.
type Charge struct {
	ID                   string                `json:"id"`
	PaymentIntent        string                `json:"payment_intent"`
	Metadata             map[string]string     `json:"metadata"`
	PaymentMethodDetails *PaymentMethodDetails `json:"payment_method_details"`
}

// ChargeRef is a PaymentIntent's latest_charge, which Stripe sends as a bare
// id unless the request expanded it into the object.
type ChargeRef struct {
	ID string
	// Object is set only when the charge was expanded.
	Object *Charge
}

// UnmarshalJSON accepts both the id string and the expanded object. An
// object that does not decode is dropped rather than failing the
// PaymentIntent around it: the charge only describes how a payment was
// made, and must never stop Brygge reading whether it was made.
func (r *ChargeRef) UnmarshalJSON(b []byte) error {
	if bytes.Equal(b, []byte("null")) {
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &r.ID)
	}
	var ch Charge
	if err := json.Unmarshal(b, &ch); err != nil {
		var id ObjectID
		_ = json.Unmarshal(b, &id)
		r.ID = string(id)
		return nil
	}
	r.ID, r.Object = ch.ID, &ch
	return nil
}

// PaymentMethodDetails is the part of a charge's payment_method_details
// Brygge reads: how the payer paid, never the card number.
type PaymentMethodDetails struct {
	// Type is the payment method type (card, link, ...).
	Type string       `json:"type"`
	Card *CardDetails `json:"card"`
}

// CardDetails is the card a charge was paid with.
type CardDetails struct {
	// Brand is Stripe's lowercase brand code (visa, mastercard, amex, ...).
	Brand string `json:"brand"`
	// Wallet is set when the card was paid through a wallet.
	Wallet *CardWallet `json:"wallet"`
}

// CardWallet names the wallet a card payment went through.
type CardWallet struct {
	// Type is apple_pay, google_pay, link, samsung_pay, ...
	Type string `json:"type"`
}

// UnmarshalJSON decodes best-effort: details of an unexpected shape decode
// as empty instead of failing the charge, balance page or PaymentIntent
// they arrive in.
func (d *PaymentMethodDetails) UnmarshalJSON(b []byte) error {
	type plain PaymentMethodDetails
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		*d = PaymentMethodDetails{}
		return nil
	}
	*d = PaymentMethodDetails(p)
	return nil
}

// CardBrandAndWallet reports the card brand and wallet type, lowercased; both are empty
// when the payment was not made with a card.
func (d *PaymentMethodDetails) CardBrandAndWallet() (brand, wallet string) {
	if d == nil || d.Card == nil {
		return "", ""
	}
	brand = strings.ToLower(strings.TrimSpace(d.Card.Brand))
	if d.Card.Wallet != nil {
		wallet = strings.ToLower(strings.TrimSpace(d.Card.Wallet.Type))
	}
	return brand, wallet
}

// Event is a webhook event. Data.Object is decoded by the receiver according
// to Type.
type Event struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Created int64  `json:"created"`
	Data    struct {
		Object json.RawMessage `json:"object"`
	} `json:"data"`
}

// Event types Brygge handles or records.
const (
	EventCheckoutSessionCompleted             = "checkout.session.completed"
	EventCheckoutSessionAsyncPaymentSucceeded = "checkout.session.async_payment_succeeded"
	EventCheckoutSessionAsyncPaymentFailed    = "checkout.session.async_payment_failed"
	EventCheckoutSessionExpired               = "checkout.session.expired"
	EventPaymentIntentSucceeded               = "payment_intent.succeeded"
	EventPaymentIntentCanceled                = "payment_intent.canceled"
	EventPayoutPaid                           = "payout.paid"
	EventPayoutFailed                         = "payout.failed"
	EventPayoutCanceled                       = "payout.canceled"
	EventChargeRefunded                       = "charge.refunded"
	EventChargeRefundUpdated                  = "charge.refund.updated"
	EventChargeDisputeCreated                 = "charge.dispute.created"
	EventChargeDisputeUpdated                 = "charge.dispute.updated"
	EventChargeDisputeClosed                  = "charge.dispute.closed"
	EventChargeDisputeFundsWithdrawn          = "charge.dispute.funds_withdrawn"
	EventChargeDisputeFundsReinstated         = "charge.dispute.funds_reinstated"
)

// WebhookEventTypes lists the event types above. The webhook's failure metric
// reports these under their own name and every other type as "other", so its
// label set stays closed.
var WebhookEventTypes = []string{
	EventCheckoutSessionCompleted, EventCheckoutSessionAsyncPaymentSucceeded,
	EventCheckoutSessionAsyncPaymentFailed, EventCheckoutSessionExpired,
	EventPaymentIntentSucceeded, EventPaymentIntentCanceled,
	EventPayoutPaid, EventPayoutFailed, EventPayoutCanceled,
	EventChargeRefunded, EventChargeRefundUpdated,
	EventChargeDisputeCreated, EventChargeDisputeUpdated, EventChargeDisputeClosed,
	EventChargeDisputeFundsWithdrawn, EventChargeDisputeFundsReinstated,
}

// Balance transaction reporting categories Brygge classifies.
const (
	CategoryCharge          = "charge"
	CategoryRefund          = "refund"
	CategoryRefundFailure   = "refund_failure"
	CategoryDispute         = "dispute"
	CategoryDisputeReversal = "dispute_reversal"
	CategoryFee             = "fee"
	CategoryPayout          = "payout"
	CategoryPayoutReversal  = "payout_reversal"
	CategoryOtherAdjustment = "other_adjustment"
)

// Payout statuses.
const (
	PayoutPending   = "pending"
	PayoutInTransit = "in_transit"
	PayoutPaid      = "paid"
	PayoutFailed    = "failed"
	PayoutCanceled  = "canceled"
)

// BalanceTransaction is one movement of the Stripe balance. Amounts are in
// the currency's minor unit (øre for NOK); Amount is gross, Net is Amount
// less Fee.
type BalanceTransaction struct {
	ID       string `json:"id"`
	Amount   int64  `json:"amount"`
	Fee      int64  `json:"fee"`
	Net      int64  `json:"net"`
	Currency string `json:"currency"`
	// Created and AvailableOn are unix seconds.
	Created     int64  `json:"created"`
	AvailableOn int64  `json:"available_on"`
	Type        string `json:"type"`
	// ReportingCategory groups Type into what the books need to know
	// (charge, refund, fee, payout, ...).
	ReportingCategory string `json:"reporting_category"`
	// FeeDetails breaks Fee down by kind (Stripe's processing fee, tax, ...).
	FeeDetails []FeeDetail `json:"fee_details"`
	// Source is the object that moved the balance: a charge, refund,
	// dispute or payout. Expanded on the list calls.
	Source *BalanceSource `json:"source"`
}

// FeeDetail is one part of a balance transaction's fee. Description is
// Stripe's own text ("Stripe processing fees").
type FeeDetail struct {
	Amount      int64  `json:"amount"`
	Currency    string `json:"currency"`
	Description string `json:"description"`
	Type        string `json:"type"`
}

// BalanceSource is the part of a balance transaction's source object Brygge
// reads. Which fields are set depends on Object: charges, refunds and
// disputes name their PaymentIntent (and refunds and disputes their charge);
// payouts carry the payout fields.
type BalanceSource struct {
	ID            string            `json:"id"`
	Object        string            `json:"object"`
	PaymentIntent ObjectID          `json:"payment_intent"`
	Charge        ObjectID          `json:"charge"`
	Metadata      map[string]string `json:"metadata"`
	// PaymentMethodDetails is set on a charge: how it was paid.
	PaymentMethodDetails *PaymentMethodDetails `json:"payment_method_details"`

	Amount              int64  `json:"amount"`
	Currency            string `json:"currency"`
	ArrivalDate         int64  `json:"arrival_date"`
	Status              string `json:"status"`
	Automatic           bool   `json:"automatic"`
	StatementDescriptor string `json:"statement_descriptor"`
}

// UnmarshalJSON accepts the bare id Stripe sends for an unexpanded source as
// well as the object.
func (s *BalanceSource) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &s.ID)
	}
	type plain BalanceSource
	return json.Unmarshal(b, (*plain)(s))
}

// ObjectID is a reference Stripe sends either as a bare id or, when it was
// expanded, as the object; only the id is kept.
type ObjectID string

// UnmarshalJSON accepts null, the id string and an object with an id.
func (o *ObjectID) UnmarshalJSON(b []byte) error {
	if bytes.Equal(b, []byte("null")) {
		*o = ""
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, (*string)(o))
	}
	var obj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}
	*o = ObjectID(obj.ID)
	return nil
}

// BalanceTransactionPage is one page of a balance transaction list.
type BalanceTransactionPage struct {
	Data    []BalanceTransaction `json:"data"`
	HasMore bool                 `json:"has_more"`
}

// Payout is a transfer from the Stripe balance to the club's bank account.
type Payout struct {
	ID       string `json:"id"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	// ArrivalDate is unix seconds at the start of the expected bank date.
	ArrivalDate int64  `json:"arrival_date"`
	Status      string `json:"status"`
	// Automatic payouts are the scheduled ones; only for those can Stripe
	// list the balance transactions a payout paid out.
	Automatic           bool   `json:"automatic"`
	StatementDescriptor string `json:"statement_descriptor"`
}
