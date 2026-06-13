# `finance/` — invoice data contract + PDF generation + KID

Pure helpers, no DB, no HTTP. Two main responsibilities: the `Invoice` data struct that `GeneratePDF` consumes, and the KID number format + check digit.

## Files

| File | What |
|---|---|
| `invoice.go` | `Invoice`, `OrgRecipient`, `InvoiceLine` data structs + `GeneratePDF(inv) ([]byte, error)` |
| `kid.go` | `GenerateKID(prefix, sequenceNumber, padding) string` + Luhn (mod-10) check digit |

## Invoice data contract

```go
type Invoice struct {
    // Seller
    ClubName        string
    OrgNumber       string
    ClubAddress     string
    Website         string
    TreasurerEmail  string
    LogoData        []byte  // PNG or JPEG bytes, optional
    LogoMIME        string  // "image/png" or "image/jpeg"

    // Buyer (private). Used when OrgRecipient is nil.
    MemberName    string
    MemberAddress string

    // Buyer (organization). When non-nil, replaces the private buyer block.
    OrgRecipient *OrgRecipient

    // Invoice details
    InvoiceNumber int
    IssueDate     time.Time
    DueDate       time.Time
    KID           string
    BankAccount   string

    Lines []InvoiceLine
}

type OrgRecipient struct {
    Name          string
    OrgNumber     string
    Address       string
    ContactPerson string  // Renders as "Att: <name>"
    TheirRef      string  // Shows in the yellow info box as "Deres ref."
}

type InvoiceLine struct {
    Description    string
    SubDescription string
    Quantity       int
    UnitPrice      float64
}
```

`GeneratePDF(inv)` returns the PDF bytes or an error. It's deterministic — same input produces the same bytes — except for an embedded timestamp that fpdf includes by default. Callers store the bytes in `invoices.pdf_data`.

## Building an invoice from DB rows

The handler builds an `Invoice` literal from:

1. Club seller fields — `clubs.name`, `clubs.org_number`, `clubs.address`, `clubs.website_url`, `clubs.treasurer_email`, `clubs.faktura_logo_data`, `clubs.faktura_logo_mime`
2. Bank account — `club_bank_accounts.account_number` where `is_default_for_invoices` (falls back to legacy `clubs.bank_account` for one release)
3. Buyer — `users.full_name` + `users.address_line + postal_code + city` for private; `invoices.recipient_*` columns for organization
4. Invoice metadata — `invoices.invoice_number`, `invoices.issue_date`, `invoices.due_date`, `invoices.kid_number`
5. Lines — `invoice_lines.description`, `sub_description`, `quantity`, `unit_price`

See `handlers/invoices_bulk_actions.go:regenerateOnePDF` for the canonical "rebuild Invoice from DB row" pattern.

## KID generation

```go
finance.GenerateKID("000", invoiceSeq, 1)
// → "00000004200X" (where X is the Luhn check digit)
```

The prefix is informational — used to namespace KIDs if a club has multiple billing streams. Length matters for total KID length (Norwegian banks accept 6-25 digits; we settle on ~12).

The last digit is always a Luhn (mod-10) check. `extractKID` in the accounting package validates Luhn on inbound bank rows to avoid false positives on arbitrary digit runs.

## Norwegian formatting helpers

PDF rendering uses Norwegian conventions throughout:

- Dates: `02.01.2006` (DD.MM.YYYY)
- Amounts: `kr 1 234,50` (non-breaking space groupings, decimal comma)
- Account numbers: `xxxx.xx.xxxxx` (11 digits with periods at positions 4 and 6)

These helpers are unexported (`finance/invoice.go`). When generating user-facing strings outside the PDF (e.g. faktura email body), the equivalent helpers live in `email/templates.go` — keep them in sync.

## Invariants

- Invoice line snapshots are denormalized — once an invoice is created, updating the underlying `price_items.amount` does NOT change historical `invoice_lines`. The PDF reflects the prices at issue time.
- KIDs are unique per club (`idx_invoices_kid`). Sequence generation reserves the next number atomically.
- The PDF stored in `invoices.pdf_data` is the authoritative record sent to the recipient. Regeneration archives the prior bytes before overwriting (bokføringsloven §13). See [`../../../docs/developer/reference/invariants.md`](../../../docs/developer/reference/invariants.md).

## Common changes

- **New layout element on the PDF** (e.g. QR code, payment-link block) → edit `GeneratePDF` body. Test against the rendered output, not byte equality, since fpdf timestamps embedded objects
- **New invoice field** (e.g. payment terms text) → add to the `Invoice` struct, the DB column, the `regenerateOnePDF` reload path, and any `HandleCreateInvoice`-style construction sites. Don't forget the archive path test (it'd silently drop the new field otherwise).
- **New KID format** → edit `GenerateKID` and `extractKID` together; the parser must accept what the generator emits
