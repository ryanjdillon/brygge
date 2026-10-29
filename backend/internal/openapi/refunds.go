package openapi

import (
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// Order refunds (BRY-497): the refund button's API. Amounts are in kr.

type OrderRefundRequest struct {
	AttemptID  string   `json:"attempt_id" format:"uuid" doc:"The captured payment to refund. A duplicate capture is a separate attempt."`
	Amount     *float64 `json:"amount,omitempty" minimum:"0.01" doc:"Kr to refund, at most two decimals; omitted refunds all that is refundable."`
	Reason     string   `json:"reason" minLength:"1" maxLength:"500" doc:"Why the money is returned; kept on the refund."`
	Outcome    string   `json:"outcome,omitempty" enum:"credit,recollect" doc:"Only for a full refund of a faktura's payment: credit (the default, a credit note is issued) or recollect (no credit note, the faktura reopens). recollect is refused with 409 recollect_after_partial once part of it was refunded."`
	RequestKey string   `json:"request_key" format:"uuid" doc:"Identifies this click. Made when the dialog opens and repeated on a retry, so a repeated request never refunds twice."`
}

// PaymentRefund is one refund of a payment, whoever made it.
type PaymentRefund struct {
	ID               string    `json:"id" format:"uuid"`
	AttemptID        string    `json:"attempt_id" format:"uuid"`
	Provider         string    `json:"provider" example:"stripe"`
	Amount           float64   `json:"amount" doc:"Kr"`
	Currency         string    `json:"currency" example:"NOK"`
	Status           string    `json:"status" enum:"requested,succeeded,failed"`
	Reason           string    `json:"reason" doc:"The person's reason; \"Stripe Dashboard\" for a refund made there."`
	Outcome          *string   `json:"outcome" enum:"credit,recollect" doc:"Null means credit."`
	FailureReason    *string   `json:"failure_reason"`
	ProviderRefundID *string   `json:"provider_refund_id"`
	CreatedBy        *string   `json:"created_by" format:"uuid" doc:"Null for a refund made in the provider's dashboard."`
	RequestKey       *string   `json:"request_key" format:"uuid"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type OrderRefundResponse struct {
	Refund               PaymentRefund `json:"refund"`
	Replayed             bool          `json:"replayed" doc:"The request key was used before; nothing new was started."`
	AwaitingConfirmation bool          `json:"awaiting_confirmation" doc:"Stripe's answer is not known; the refund is looked up again later (HTTP 202)."`
}

type OrderRefundAttempt struct {
	ID               string    `json:"id" format:"uuid"`
	Provider         string    `json:"provider"`
	Currency         string    `json:"currency"`
	Amount           float64   `json:"amount" doc:"Kr paid"`
	Refunded         float64   `json:"refunded" doc:"Kr in requested and succeeded refunds"`
	Refundable       float64   `json:"refundable" doc:"Kr left to refund"`
	Duplicate        bool      `json:"duplicate" doc:"A payment of an order an earlier payment had paid (dobbeltbetaling)."`
	Disputed         bool      `json:"disputed"`
	CanRefund        bool      `json:"can_refund"`
	RecollectAllowed bool      `json:"recollect_allowed"`
	PaymentURL       *string   `json:"payment_url,omitempty" doc:"The payment in the Stripe Dashboard; absent until its PaymentIntent is known."`
	CreatedAt        time.Time `json:"created_at"`
}

type OrderRefundDispute struct {
	ID            string     `json:"id"`
	AttemptID     *string    `json:"attempt_id" format:"uuid"`
	Status        string     `json:"status"`
	Reason        string     `json:"reason"`
	Amount        *float64   `json:"amount"`
	EvidenceDueBy *time.Time `json:"evidence_due_by"`
	Open          bool       `json:"open" doc:"A dispute that stops a refund."`
}

type OrderRefundsResponse struct {
	Attempts         []OrderRefundAttempt `json:"attempts"`
	Refunds          []PaymentRefund      `json:"refunds"`
	Disputes         []OrderRefundDispute `json:"disputes"`
	HasFakturas      bool                 `json:"has_fakturas"`
	CanRefund        bool                 `json:"can_refund"`
	RecollectAllowed bool                 `json:"recollect_allowed"`
}

// ReissuedInvoice is the faktura "Ny faktura" after a refund issued.
type ReissuedInvoice struct {
	ID                    string  `json:"id" format:"uuid"`
	InvoiceNumber         int     `json:"invoice_number"`
	KID                   string  `json:"kid_number"`
	TotalAmount           float64 `json:"total_amount" doc:"Kr"`
	IssueDate             string  `json:"issue_date" doc:"YYYY-MM-DD"`
	DueDate               string  `json:"due_date" doc:"YYYY-MM-DD"`
	OriginalInvoiceNumber int     `json:"original_invoice_number" doc:"The credited faktura this one replaces."`
	SentAt                *string `json:"sent_at" doc:"When it was emailed to the payer; null when it was not (no address or no mail service), and it is then a draft in the faktura list."`
	GLBooking             *struct {
		Status string `json:"status"`
		Error  string `json:"error,omitempty"`
	} `json:"gl_booking,omitempty" doc:"The ledger booking made when it was sent."`
	Warnings []string `json:"warnings,omitempty" doc:"Steps after the faktura was stored that failed: club_details_unavailable, pdf_unavailable."`
}

func registerRefundOps(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "admin-reissue-invoice-after-refund",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/financials/invoices/{invoiceID}/reissue-after-refund",
		Tags:        []string{"Admin", "Financials"},
		Summary:     "Issue a new faktura for one that card refunds credited in full (treasurer/admin, fresh TOTP)",
		Description: "Copies the credited faktura's lines (net of role exemptions) to the same payer, dated today and due in 21 days, numbered under the club's numbering lock, then emails and books it as a faktura made by hand is. The credit notes the refund issued stand. 201 the new faktura; 404 no such faktura; 409 refused with a `code`: `not_refunded` (the faktura is not credited in full by card refunds) or `already_reissued` (once per credited faktura).",
		Security:    BearerSecurity,
	}, stub[struct {
		InvoiceID string `path:"invoiceID" format:"uuid"`
	}, struct{ Body ReissuedInvoice }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-refund-order",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/orders/{orderID}/refund",
		Tags:        []string{"Admin", "Payments"},
		Summary:     "Refund a card payment of an order (treasurer/admin, fresh TOTP)",
		Description: "Stores the request, then asks Stripe for the refund under an Idempotency-Key derived from `request_key`. Books nothing: the booking pass books the refund and issues the credit note once Stripe's balance lists it. 200 the refund (status says whether Stripe finished it; a repeated request key returns the same refund); 202 Stripe's answer is not known and the refund is looked up again; 409 refused with a `code` (`not_supported`, `not_captured`, `disputed`, `amount_exceeds_refundable`, `recollect_after_partial`, `request_key_reused`); 502 Stripe refused the refund (`code` `refund_failed`).",
		Security:    BearerSecurity,
	}, stub[struct {
		OrderID string `path:"orderID" format:"uuid"`
		Body    OrderRefundRequest
	}, struct{ Body OrderRefundResponse }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-list-order-refunds",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/orders/{orderID}/refunds",
		Tags:        []string{"Admin", "Payments"},
		Summary:     "An order's payments, refunds and disputes, and whether it can be refunded (treasurer/admin)",
		Description: "Looks up requested refunds Stripe has not confirmed for ten minutes before answering.",
		Security:    BearerSecurity,
	}, stub[struct {
		OrderID string `path:"orderID" format:"uuid"`
	}, struct{ Body OrderRefundsResponse }]())
}
