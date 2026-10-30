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
