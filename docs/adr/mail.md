# Mail decisions

See [README.md](README.md) for the format. How the mail stack is set up and operated: [developer/mail/setup.md](../developer/mail/setup.md). DNS records: `terraform/dns.nix`.

### MAIL-1: DMARC policy is `quarantine`, with aggregate reports to `postmaster@`

- **Status:** accepted · 2026-10-30 · BRY-500, BRY-502
- **Decision:** each club publishes one `_dmarc` TXT record with `p=quarantine`, `rua=mailto:postmaster@<domain>` and `fo=1`. The policy is the `dmarc_policy` tfvars value (default `quarantine`) and is published by `terraform/dns.nix`.
- **Why:** `quarantine` stops spoofed mail from reaching inboxes while a legitimate message that fails authentication is still delivered to spam rather than lost. The club's own mail was briefly unsigned in October 2026 (BRY-399, BRY-400), which a `reject` policy would have turned into lost invoices and login links. `none` protects nothing.
- **Consequences:**
  - Tighten to `reject` only after several weeks of aggregate reports with no DKIM or SPF failures from the club's own IPs. Change `dmarc_policy`, run `tf-apply`, and watch the next reports.
  - A domain can have only one `_dmarc` record. A service that asks the club to "add your DMARC policy" (the Stripe custom email domain dialog is one) needs no new record: the existing one applies.
  - A third-party sender may use the club's domain only if its mail passes DMARC through DKIM or SPF aligned with the club domain. Stripe's does: it signs with `d=<domain>` and uses `bounce.<domain>` as its envelope sender. See [Stripe custom email domain](../developer/mail/setup.md#stripe-custom-email-domain).

### MAIL-2: The DMARC alert stays sensitive

- **Status:** accepted · 2026-10-30 · BRY-502
- **Decision:** `DmarcDkimFailing` pages on a single DKIM failure in an aggregate report, with no count threshold and no lower severity for one failure.
- **Why:** mail to members (invoices, login links, reminders) depends on DKIM passing, and a failure is rare enough that a page is worth the noise. A report covers a past day, so an alert can fire long after the cause is fixed. The runbook says how to tell (the report's date range).

### MAIL-3: App mail goes through Amazon SES; Stalwart keeps inbound and the club mailboxes

- **Status:** accepted · 2026-11-03 · BRY-583 (spec), BRY-587, BRY-588, BRY-589, BRY-590, BRY-591, BRY-592
- **Decision:**
  - The app's own mail (magic links, fakturas, reminders, credit notes, dispute and contact mail) is sent through Amazon SES in `eu-north-1`, in the owner's AWS account, over SES's SMTP interface with the existing SMTP client. Each send carries the club's configuration set and `send_id` / `kind` message tags. It is switched per club (`ses_sending`), separately from provisioning the AWS resources (`ses_enabled`), so DNS can verify before the cutover.
  - Delivery events go SES configuration set → SNS → SQS (with a dead-letter queue) and are long-polled by a worker in the API. There is no public webhook.
  - Stalwart keeps inbound mail and the club mailboxes. Reply-To stays a club address the board reads, so replies to app mail land where the board works.
  - A magic link whose SES send fails unambiguously is sent again through Stalwart's SMTP relay, so members can still log in while SES is down.
  - Lettermint is the named fallback provider if SES has to be replaced.
  - A send whose outcome is ambiguous (the connection failed while the body was written or at the end of DATA) is recorded `unknown` and never retried automatically: SES's SMTP interface has no idempotency key, so a retry can send the message twice.
- **Why:** sending from the club host's own IP left deliverability to the reputation of one small server: Yahoo refused app mail with a 554 (BRY-586), and the only delivery signal was scraping Stalwart's journal (BRY-581). SES carries a shared, managed sending reputation, reports every outcome as a structured event, and keeps the data in the EU. Turned down:
  - **Resend:** the free tier's 100 mails a day is less than one faktura run, and data is processed in the US.
  - **Brevo:** click tracking can't be turned off, so links in fakturas and login mail would be rewritten through its tracker.
  - **Scaleway Transactional Email:** the 2 MB API message cap is too small for fakturas with PDF attachments.
  - **Lettermint:** a good fit, but about €10 a month against SES's per-message price at a club's volume; kept as the fallback.
  - **Staying on Stalwart direct:** the IP-reputation burden stays with the club host (the Yahoo 554, BRY-586), and monitoring needs journal scraping.
- **Consequences:**
  - Every send writes its `email_sends` row before the SMTP `DATA`, so a crash mid-send still leaves a record; event statuses only move forward by rank; undeliverable events go to the DLQ for a person. See [invariants § Mail](../developer/reference/invariants.md#mail) and [sesevents README](../../backend/internal/sesevents/README.md).
  - Bounces, suppressions, complaints and rejects page with the query that names the recipient domain, SMTP status and diagnostic ([alerts runbook § SesDeliveryFailed](../developer/otel/alerts-runbook.md#sesdeliveryfailed)); the Stalwart bounce alert stays for mail that still goes through Stalwart.
  - SES's account-level suppression list is authoritative: a suppressed address gets no mail until it is removed, which the board does from the app (BRY-590).
  - Stalwart relaying through SES (BRY-591) and retiring Stalwart (BRY-592) are separate decisions.
  - Setup and operation: [developer/mail/ses.md](../developer/mail/ses.md).
