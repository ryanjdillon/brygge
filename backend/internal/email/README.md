# `email/` — outbound email senders + Norwegian-localized templates

A small package: an interface (`Sender`), two implementations (SMTP, mock), and a templates file holding Subject/Body helpers + HTML templates for the few email types we send (magic link, invoice, reminder).

## Files

| File | What |
|---|---|
| `sender.go` | `Sender` interface — `Send(ctx, to, subject, html) error` and `SendWithAttachment(ctx, to, subject, html, filename, attachment) error`. Implementations are interchangeable. |
| `smtp.go` | `SMTPClient` — production sender. Uses `mime/multipart` for the attachment variant; sends RFC-compliant `Content-Type: multipart/mixed`. |
| `mock.go` | `MockSender` — records every call in a slice for tests. Use with `&MockSender{}` and assert against `mock.Calls`. |
| `templates.go` | `Subject(...)`, `Body(...)` helpers + `html/template` definitions for invoice, reminder, magic-link emails. Plus `DetectLocale`, `formatNOK`. |

## Template pattern

Every email type follows the same shape:

```go
func InvoiceSubject(_, clubName string, invoiceNumber int) string {
    if clubName == "" { clubName = "klubben" }
    return fmt.Sprintf("Du har mottatt en ny faktura fra %s [Faktura %d]", clubName, invoiceNumber)
}

func InvoiceBody(_ string, memberName, clubName string, invoiceNumber int, dueDate time.Time, total float64, kid, bankAccount string) string {
    // Defaults for empty inputs
    // Execute invoiceHTMLTpl with a struct value
    // Fall back to a flat <p>-tag summary on template error
}
```

Notes:

- The `locale` parameter is currently ignored — every email goes out in Norwegian. Multi-locale email is tracked separately
- Always provide a fallback path that doesn't depend on the template parsing — a template bug in production shouldn't drop the email
- Sender attaches the PDF separately; templates don't embed binary data

## Money formatting

Always use `formatNOK(amount)`:

```go
"kr 1 234,50"  // Norwegian thousands grouping, decimal comma
```

Never `fmt.Sprintf("%.2f kr", amount)` — the format is different (decimal point vs comma) and inconsistency is jarring for recipients.

## Localized locale detection

`DetectLocale(r *http.Request) string` reads `Accept-Language` and returns one of the 8 supported locale codes (`nb`, `nn`, `en`, `de`, `fr`, `it`, `nl`, `pl`). Currently the email templates ignore this and always render Norwegian — passing it through is forward-compat for when the templates get translated.

## Sender lifecycle

- Constructed once at server startup in `cmd/api/main.go` with `email.NewSMTPClient(host, port, user, pass, from, replyTo)`
- Stored as the `email` field on whatever handler needs it
- Optional throughout — handlers must check `if h.email == nil` and return 503 if the operation requires email

```go
if h.email == nil {
    Error(w, http.StatusServiceUnavailable, "email delivery not configured")
    return
}
```

## Common changes

- **New email type** (e.g. password reset, board announcement) → add `XSubject`, `XBody`, `xHTMLTpl` in `templates.go`. Mirror the InvoiceBody pattern: fallback on template error, sensible defaults for empty fields. The caller calls `SendWithAttachment` for attachments or `Send` for plain.
- **Localize an existing template** → introduce a `switch locale { ... }` inside the helper. The helper signature already takes locale; today's implementations just ignore it.
- **New rendering field** → add to the `data struct` literal, the template's `{{...}}` references, AND the fallback string. Forgetting the fallback is the most common miss.

## Invariants

- Sender lifecycle: stamp DB first (e.g. `sent_at`), then send. If send fails, roll back. The reverse order leaves the recipient with an email and the DB thinking it never went out.
- Reminder vs invoice subject: the "[Faktura N]" bracket in the subject is what email clients use to thread reminders with the original delivery. Don't drop the bracket; don't change the format.
