package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// MountLedgerRoutes registers the journal, sync, bank-import and bank
// reconciliation routes under the accounting router. moneyGate wraps every
// route that can create or change a posted GL entry (directly, or by
// auto-matching during an import or sync); reads and previews stay open to
// every accounting role. It lives here rather than inline in main so the
// gate placement can be tested route by route.
func (h *AccountingHandler) MountLedgerRoutes(r chi.Router, moneyGate ...func(http.Handler) http.Handler) {
	r.Route("/journal", func(r chi.Router) {
		r.Get("/", h.HandleListJournalEntries)
		r.With(moneyGate...).
			Post("/", h.HandleCreateJournalEntry)
		r.Post("/parse-receipt", h.HandleParseReceipt)
		r.Get("/{entryID}", h.HandleGetJournalEntry)
		r.With(moneyGate...).
			Post("/{entryID}/post", h.HandlePostJournalEntry)
		r.With(moneyGate...).
			Post("/{entryID}/void", h.HandleVoidJournalEntry)
		r.With(moneyGate...).
			Delete("/{entryID}", h.HandleDeleteDraftJournalEntry)
		r.Post("/{entryID}/attachment", h.HandleUploadJournalAttachment)
		r.Get("/{entryID}/attachment", h.HandleGetJournalAttachment)
	})

	r.Route("/sync", func(r chi.Router) {
		r.With(moneyGate...).
			Post("/payments", h.HandleSyncPayments)
		r.With(moneyGate...).
			Post("/invoices", h.HandleSyncInvoices)
		r.Post("/invoices/resplit/preview", h.HandlePreviewResplitInvoiceBilags)
		r.With(moneyGate...).
			Post("/invoices/resplit", h.HandleApplyResplitInvoiceBilags)
		r.Post("/vipps-drafts/preview", h.HandlePreviewVippsDraftBacklog)
		r.With(moneyGate...).
			Post("/vipps-drafts", h.HandleApplyVippsDraftBacklog)
	})

	r.Get("/bank-formats", h.HandleListBankFormats)
	r.Route("/bank-import", func(r chi.Router) {
		r.With(moneyGate...).
			Post("/", h.HandleImportBankStatement)
		// Reimports run the import cascade, which posts KID
		// matches; reassigning an import rewrites posted
		// journal_lines. Both are money moves.
		r.With(moneyGate...).
			Post("/account-reimport", h.HandleReimportAccountBankStatement)
		r.Get("/{importID}", h.HandleGetBankImport)
		r.Get("/{importID}/unmatched", h.HandleListUnmatchedRows)
		r.Post("/{importID}/rows/{rowID}/match", h.HandleMatchBankRow)
		r.Post("/{importID}/auto-match", h.HandleAutoMatchImport)
		r.With(moneyGate...).
			Patch("/{importID}/account", h.HandleReassignBankImport)
		r.With(moneyGate...).
			Post("/{importID}/reimport", h.HandleReimportBankStatement)
	})

	r.Route("/vipps-imports", func(r chi.Router) {
		r.With(moneyGate...).
			Post("/", h.HandleImportVippsSettlement)
		r.Get("/", h.HandleListVippsImports)
		r.Get("/{importID}", h.HandleGetVippsImport)
	})

	r.Get("/bank-imports", h.HandleListBankImports)
	r.Get("/vipps-missing-settlements", h.HandleListMissingVippsSettlements)
	r.Get("/bank-rows", h.HandleListBankRowsByAccount)
	r.Get("/vipps-rows", h.HandleListVippsRowsByMSN)
	r.With(moneyGate...).Post("/bank-sync", h.HandleBankSync)
	r.With(moneyGate...).Post("/vipps-resync", h.HandleResyncVipps)
	r.With(moneyGate...).Post("/stripe/sync", h.HandleStripeSync)
	r.Get("/stripe/review", h.HandleListStripeReview)
	r.Get("/stripe/assignable-accounts", h.HandleListStripeAssignableAccounts)
	r.Route("/stripe/transactions", func(r chi.Router) {
		r.Get("/", h.HandleListStripeTransactions)
		r.Get("/{txnID}/potential-invoices", h.HandleStripeTxnPotentialInvoices)
		r.With(moneyGate...).Post("/{txnID}/assign-invoice", h.HandleAssignStripeTxnToInvoice)
		r.With(moneyGate...).Post("/{txnID}/assign-account", h.HandleAssignStripeTxnToAccount)
		r.With(moneyGate...).Post("/{txnID}/dismiss", h.HandleDismissStripeTxn)
		r.With(moneyGate...).Post("/{txnID}/undismiss", h.HandleUndismissStripeTxn)
		r.With(moneyGate...).Post("/{txnID}/unassign", h.HandleUnassignStripeTxn)
		r.With(moneyGate...).Post("/{txnID}/rebook", h.HandleRebookStripeTxn)
	})
	r.Get("/stripe/payouts", h.HandleListStripePayouts)
	r.With(moneyGate...).Post("/stripe/payouts/{payoutID}/clear", h.HandleClearStripePayout)

	r.Route("/bank-rows/{rowID}/reconcile-vipps", func(r chi.Router) {
		r.Get("/", h.HandleVippsReconcilePreview)
		r.With(moneyGate...).Post("/confirm", h.HandleVippsReconcileConfirm)
	})
}
