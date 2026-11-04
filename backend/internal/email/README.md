# `email/` — outbound email senders + Norwegian-localized templates

An interface (`Sender`), the SMTP transport (Stalwart or the Amazon SES SMTP interface), the decorators every production send goes through (send log, metrics, magic-link fallback), a mock, and the Subject/Body helpers + HTML templates for the email types we send. SES setup and cutover: [docs/developer/mail/ses.md](../../../docs/developer/mail/ses.md).

## Files

| File | What |
|---|---|
| `sender.go` | `Sender` interface — `Send`, `SendWithAttachment` and `SendWithHeaders`. Implementations are interchangeable. |
| `smtp.go` | `SMTPClient` — the transport. Uses `mime/multipart` for the attachment variant; sends RFC-compliant `Content-Type: multipart/mixed`. Runs the DATA exchange itself (`writeData`) to classify failures and read the relay's reply. `WithSESConfigurationSet` makes it an SES client. |
| `ses.go` | SES specifics: regional SMTP host, SMTP password derivation from the IAM secret key, `X-SES-CONFIGURATION-SET` / `X-SES-MESSAGE-TAGS` rendering, message id parsing. |
| `sendmeta.go` | `SendMeta` + `WithSendMeta(ctx, …)` (what a send is about), `ErrDeliveryAmbiguous`, `Provider`. |
| `record.go` | `Record(Sender, Provider, SendLog, logger)` decorator writing `email_sends`; `PGSendLog` is the table-backed `SendLog`. |
| `fallback.go` | `WithMagicLinkFallback(primary, fallback, logger)` — second relay for login links only. |
| `instrument.go` | `Instrument(Sender, Provider)` decorator counting `brygge.email.send{kind,provider,result}`; the `Kind` set; `WithKind(ctx, Kind)`. See [instrumentation.md](../../../docs/developer/otel/instrumentation.md#app-email-sends). |
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

- Constructed once at server startup by `newEmailSender` in `cmd/api/email.go`, chosen by `EMAIL_PROVIDER`:

  ```
  smtp (default): Instrument(Record(SMTPClient Stalwart, stalwart), stalwart)
  ses:            WithMagicLinkFallback(
                      Instrument(Record(SMTPClient SES, ses), ses),
                      Instrument(Record(SMTPClient Stalwart, stalwart), stalwart))
  ```

  `EMAIL_PROVIDER=ses` without `SES_REGION`, `SES_CONFIGURATION_SET`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` or `EMAIL_FROM` stops the API at startup rather than quietly sending through Stalwart. The SES SMTP password is derived from `AWS_SECRET_ACCESS_KEY` (`SESSMTPPassword`); there is no separate SMTP secret.
- `NewSMTPClient` returns a nil `*SMTPClient` when `SMTP_HOST` or `EMAIL_FROM` is empty; only assign it to an `email.Sender` after checking for nil — a typed nil inside the interface is non-nil, so the handlers' nil check passes and the first send panics
- Stored as the `email` field on whatever handler needs it
- Optional throughout — handlers must check `if h.email == nil` and return 503 if the operation requires email

```go
if h.email == nil {
    Error(w, http.StatusServiceUnavailable, "email delivery not configured")
    return
}
```

## Common changes

- **New email type** (e.g. password reset, board announcement) → add `XSubject`, `XBody`, `xHTMLTpl` in `templates.go`. Mirror the InvoiceBody pattern: fallback on template error, sensible defaults for empty fields. The caller calls `SendWithAttachment` for attachments or `Send` for plain, passing `email.WithSendMeta(ctx, email.SendMeta{Kind, ClubID, UserID, RelatedType, RelatedID})` so the `email_sends` row names the club, the member (GDPR erasure follows `user_id`) and the record the mail carries. Add a `Kind` constant (to `allKinds` **and** `sesevents.Kinds`, which a test keeps equal) if none fits, otherwise the send is counted as `other`. Kinds are a closed set: never derive one from request data.
- **Localize an existing template** → introduce a `switch locale { ... }` inside the helper. The helper signature already takes locale; today's implementations just ignore it.
- **New rendering field** → add to the `data struct` literal, the template's `{{...}}` references, AND the fallback string. Forgetting the fallback is the most common miss.

## Metrics

Every send through the production sender increments `brygge.email.send` with `kind`, `provider` (`ses` / `stalwart`) and `result` (`ok` / `error`) only. Never add recipient, subject or error text as attributes — they are member data and unbounded. Broadcast mail goes through JMAP, not `Sender`, and is neither counted nor logged here.

## Send log (`email_sends`)

`Record` writes one `email_sends` row per relay attempt (migration 000130; the delivery-event side is [sesevents](../sesevents/README.md)):

| Moment | Status |
|---|---|
| Before the relay is dialled | `sending` |
| End of DATA answered 250 | `submitted` (SES also returns its message id, stored as `ses_message_id`) |
| Failure before the relay could hold the message: dial, TLS, auth, MAIL/RCPT/DATA refused, or a 4xx/5xx reply to the end of DATA | `failed` |
| Connection lost or timed out while writing the body or awaiting the end-of-DATA reply | `unknown`, error wraps `ErrDeliveryAmbiguous` |

The row's id is the SES `send_id` message tag, so delivery events find it directly. `Finish` only moves a row out of `sending`: a delivery event that arrived first already ranks higher. Club: `SendMeta.ClubID`, else the `CLUB_SLUG` club (contact form, magic link); if neither resolves, the mail is still sent, unrecorded, with a warning. A failed insert likewise does not block the send — a lost log row is cheaper than a lost login link. Stalwart sends are logged too (`provider='stalwart'`).

## Invariants

- Sender lifecycle: stamp DB first (e.g. `sent_at`), then send. If send fails, roll back — unless the error is `ErrDeliveryAmbiguous`, where the recipient may have the mail and the stamp stays: the faktura send endpoint then audits and books the faktura as issued and answers 502 "email delivery outcome unknown"; the resend endpoint answers the same message instead of "failed to send email". The reverse order leaves the recipient with an email and the DB thinking it never went out.
- The `email_sends` row is written before the relay is contacted (before `DATA`), so a crash mid-send leaves a `sending` row rather than no trace.
- **`ErrDeliveryAmbiguous` is never retried automatically.** The relay may hold the message, so a retry can deliver it twice. `broadcast.Worker` fails such a delivery at once instead of returning it to `pending`; the magic-link fallback does not fire on it. A person decides (Leveringslogg / requeue), knowing the first copy may arrive.
- A definite SMTP reply to the end of DATA (4xx/5xx) is a refusal, not ambiguity: SES rejects unverified senders and sandbox recipients there, and that is exactly when the magic-link fallback must fire.
- The magic-link fallback is for `KindMagicLink` only. Fakturas and reminders report the failure to their caller; sending them through a second relay would split one faktura's delivery history across providers.
- SES headers are set after the caller's extra headers, so no caller can redirect the configuration set or forge a `send_id`. Tag values are reduced to `[A-Za-z0-9_-]`, the characters SES allows in the `X-SES-MESSAGE-TAGS` header.
- Reminder vs invoice subject: the "[Faktura N]" bracket in the subject is what email clients use to thread reminders with the original delivery. Don't drop the bracket; don't change the format.
- Header bytes are 7-bit ASCII. Subjects are Q-encoded and configured `From`/`Reply-To` display names RFC 2047-encoded (`encodeAddressHeader`); a raw non-ASCII club name gets a 554 "From header is invalid" from strict receivers such as Yahoo (BRY-586).
