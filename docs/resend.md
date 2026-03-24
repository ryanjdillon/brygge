# Email Delivery (Resend)

Brygge uses [Resend](https://resend.com) for transactional email delivery. Emails are sent for:

- **Magic link login** — single-use login links
- **Invoice delivery** — PDF faktura attachments
- **Broadcasts** — club announcements to members

---

## Setup

### 1. Create a Resend account

Sign up at [resend.com](https://resend.com). The free tier includes 100 emails/day and 3,000 emails/month.

### 2. Verify your domain

In the Resend dashboard, go to **Domains** → **Add Domain** and add your club's domain (e.g. `klubb.no`). Follow the DNS instructions to add the required TXT and MX records.

Until your domain is verified, you can use `onboarding@resend.dev` as the sender for testing.

### 3. Create an API key

Go to **API Keys** → **Create API Key**. Select "Sending access" permission. Copy the key — it starts with `re_`.

### 4. Configure environment

Add to `deploy/.env`:

```bash
RESEND_API_KEY=re_your_key_here
RESEND_FROM_ADDRESS=noreply@klubb.no
```

The from address must use a domain you've verified in Resend.

---

## How it works

The email client (`internal/email/resend.go`) uses Resend's REST API directly — no SDK dependency. It supports:

- **`Send(to, subject, html)`** — plain HTML emails (magic links, broadcasts)
- **`SendWithAttachment(to, subject, html, filename, data)`** — emails with binary attachments (invoice PDFs)

Both methods:
- Use a 10-second HTTP timeout
- Are instrumented with OpenTelemetry (outbound HTTP spans)
- Return structured errors for rate limiting (429) vs permanent failures
- Accept the `email.Sender` interface, so handlers can be tested with `email.MockSender`

---

## Degraded mode

If `RESEND_API_KEY` is not set, the app starts normally with a warning:

```
WRN email delivery disabled (no RESEND_API_KEY)
```

In this mode:
- Magic link login creates tokens in the database but doesn't send emails (useful for local dev — check logs for the token)
- Invoice creation works but email delivery is skipped
- Broadcasts are stored but not delivered

---

## Rate limits

Resend's free tier allows 100 emails/day. The client returns a specific error on 429 responses, which handlers log but don't fail on. If you're sending invoices to many members, consider upgrading to a paid plan or batching sends.

---

## Testing

Unit tests use `email.MockSender` which records calls without hitting the API:

```go
mock := &email.MockSender{}
handler := NewMagicLinkHandler(db, cfg, mock, sessions, log)
// ... invoke handler ...
assert(len(mock.Calls) == 1)
assert(mock.Calls[0].To == "user@example.com")
```

For end-to-end testing with real delivery, use Resend's test mode or a `@resend.dev` address.

---

See also: [configuration.md](configuration.md) | [deploy.md](deploy.md)
