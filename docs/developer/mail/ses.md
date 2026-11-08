# Amazon SES — app mail and delivery events

Back to [mail docs](README.md).

Brygge's own outbound mail (magic links, fakturas, reminders, credit notes,
contact replies) is sent through Amazon SES. Stalwart keeps inbound mail and
the board's own mailboxes. SES reports what happened to every message
(accepted by the receiving server, deferred, bounced, complained about) and
Brygge stores those events per send, which is what the Leveringslogg shows.

Everything on the AWS side is created by Terraform in
[`terraform/aws/ses.nix`](../../../terraform/aws/ses.nix), gated on the tfvars flag
`ses_enabled`. Before the first apply with the flag on (it defaults to off)
Terraform creates nothing on AWS, publishes no SES DNS records and makes no
AWS API calls, and the host sets none of the `SES_*` variables. After that the
flag stays on; see [aws-setup.md § tfvars flags](../aws-setup.md#tfvars-flags).

## AWS resources

All names derive from `club_slug`, so `nix/host.nix` builds the same strings
from `clubConfig.slug` without reading Terraform outputs.

| Resource | Name | Why it exists |
|----------|------|---------------|
| `aws_sesv2_email_identity` | `<domain>` | Verifies the club domain with Easy DKIM (2048-bit). SES signs with `d=<domain>`, so DKIM aligns with the From address and DMARC passes. ([Easy DKIM](https://docs.aws.amazon.com/ses/latest/dg/send-email-authentication-dkim-easy.html)) |
| `aws_sesv2_email_identity_mail_from_attributes` | `send.<domain>` | Custom MAIL FROM, so the envelope sender is on the club's domain and SPF aligns under relaxed DMARC. On MX failure SES falls back to its own MAIL FROM (mail still flows, DKIM still aligns). ([custom MAIL FROM](https://docs.aws.amazon.com/ses/latest/dg/mail-from.html)) |
| `aws_sesv2_configuration_set` | `brygge-<slug>` | Named in every message via `X-SES-CONFIGURATION-SET`. Reputation metrics on; account suppression for bounces and complaints. **No open/click tracking**: it rewrites links and would break magic links and payment links. Also set as the identity's default, so mail that omits the header still produces events. |
| `aws_sesv2_configuration_set_event_destination` | `sns` | Publishes Send, Delivery, DeliveryDelay, Bounce, Complaint, Reject and Rendering Failure events to SNS. ([SNS event destination](https://docs.aws.amazon.com/ses/latest/dg/event-publishing-add-event-destination-sns.html)) |
| `aws_sns_topic` + policy | `brygge-<slug>-ses-events` | SES can only publish to SNS (or Firehose/CloudWatch/EventBridge), not straight to SQS. The topic policy lets `ses.amazonaws.com` publish only for this account and this configuration set. |
| `aws_sqs_queue` | `brygge-<slug>-ses-events` | Buffer Brygge long-polls. No public webhook, and events survive Brygge downtime (4-day retention). SSE-SQS encryption, 60 s visibility timeout, 20 s long poll. |
| `aws_sqs_queue` (DLQ) | `brygge-<slug>-ses-events-dlq` | Messages the worker fails to process five times. Kept 14 days (the SQS maximum) for inspection and redrive. |
| `aws_sqs_queue_policy` | — | Lets only this SNS topic send to the queue. |
| `aws_sns_topic_subscription` | — | SNS → SQS with raw message delivery, so the SQS body is the SES event JSON without the SNS envelope. ([SNS to SQS](https://docs.aws.amazon.com/sns/latest/dg/sns-sqs-as-subscriber.html)) |
| `aws_iam_user` + inline policy | `brygge-<slug>-app` | Brygge's runtime principal: `ses:SendRawEmail` on the identity and configuration set (what the SMTP interface authorises against), `GetQueueUrl`/`ReceiveMessage`/`DeleteMessage` on the queue, only `GetQueueUrl`/`GetQueueAttributes` on the DLQ (the worker reads its depth; redrive is an operator task), and get/delete on the account suppression list (no resource ARN exists for those, so `*`). |

The app user's **access key is not managed by Terraform.** An
`aws_iam_access_key` resource would put the secret in plaintext in the remote
state, so the key is created by hand and goes straight into the host's secret
env file.

### Provider credentials

The tf wrappers (`nix run .#tf-plan` / `.#tf-apply`) export
`AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` with the **Hetzner** Object
Storage keys for the state backend. The `aws` provider in
[`terraform/providers.nix`](../../../terraform/providers.nix) therefore always
gets explicit keys: `aws_access_key` / `aws_secret_key` from tfvars, or a
placeholder with credential validation skipped when they are empty. It never
falls back to the ambient credential chain. With `ses_enabled = true` and
empty keys the plan fails: on the precondition for a first apply, and once
resources are in state possibly with an AWS authentication error from the
refresh as well.

These Terraform keys belong to an admin/bootstrap principal in the owner's
account. They are separate from the app user's runtime key.

## DNS records

Published by [`terraform/hetzner/dns.nix`](../../../terraform/hetzner/dns.nix) when
`ses_enabled` is true:

| Type | Name | Value |
|------|------|-------|
| CNAME ×3 | `<token>._domainkey` | `<token>.<dkim zone>.` (tokens from the identity; zone below) |
| MX | `send` | `10 feedback-smtp.<aws_region>.amazonses.com.` |
| TXT | `send` | `"v=spf1 include:amazonses.com ~all"` |

The DKIM zone is the identity's `SigningHostedZone`, which the aws provider
does not export. `aws/ses.nix` derives it from AWS's
[per-region DKIM domain table](https://docs.aws.amazon.com/general/latest/gr/ses.html#ses_dkim_domains):
`dkim.amazonses.com` for eu-north-1 and most regions, `dkim.<region>.amazonses.com`
for the listed newer ones. AWS notes the zone can differ per identity, so after
the first apply compare the CNAME targets with the SES console (identity →
Authentication → DNS records) or `aws sesv2 get-email-identity` (see the runbook below).

Untouched: the apex MX (inbound stays on Stalwart), the apex SPF
(`v=spf1 mx -all`; SES mail authenticates SPF on `send.`, not the apex) and
`_dmarc` (relaxed alignment). Two rules:

- **Never `bounce.`** for the MAIL FROM: that name is Stripe's custom email
  domain CNAME.
- **Never `aspf=s`** in DMARC: SES's SPF passes on `send.<domain>`, which only
  aligns under relaxed SPF alignment.

## Owner setup runbook

The account, the Terraform principal and its IAM policy, the app key, the
tfvars flags and the order of the first rollout are in
[aws-setup.md](../aws-setup.md). In short: Terraform principal and tfvars →
`tf-apply` → DKIM and MAIL FROM *Successful* → app key in `/etc/brygge/env` →
production access → deploy with `ses_enabled` on and `ses_sending` off.

`ses_enabled` stays `true` once applied; `ses_sending` is the cutover switch.

SES SMTP authenticates with the app key id as username and a password derived
from the secret key with AWS's SigV4 algorithm (region-specific). Brygge
derives it at startup from `AWS_SECRET_ACCESS_KEY`, so there is no separate
SMTP secret to store
([Obtaining SES SMTP credentials](https://docs.aws.amazon.com/ses/latest/dg/smtp-credentials.html)).
After the first apply, `aws sesv2 get-email-identity` shows the identity's
real DKIM zone, to compare with the CNAME targets:

```sh
aws sesv2 get-email-identity --email-identity <domain> --region eu-north-1 \
  --query '{dkim:DkimAttributes.Status,dkimZone:DkimAttributes.SigningHostedZone,mailFrom:MailFromAttributes.MailFromDomainStatus}'
```

## Sending app mail through SES

With `EMAIL_PROVIDER=ses` the API sends over the SES SMTP interface,
`email-smtp.<SES_REGION>.amazonaws.com:465` with implicit TLS
([endpoints](https://docs.aws.amazon.com/general/latest/gr/ses.html)). Each
message carries two headers that SES strips before delivery
([event publishing headers](https://docs.aws.amazon.com/ses/latest/dg/event-publishing-send-email.html)):

- `X-SES-CONFIGURATION-SET`: routes the message's events to the SNS → SQS
  pipeline above.
- `X-SES-MESSAGE-TAGS: send_id=<uuid>, kind=<kind>`: SES echoes the tags in
  every event, which is how an event finds its `email_sends` row.

Every send, through SES or Stalwart, first writes an `email_sends` row with
status `sending`. A send that fails while the message body is in flight is
marked `unknown` and never retried automatically, because SES has no
idempotency key and the message may already be queued. Magic links fall back
to Stalwart SMTP when SES refuses them outright. The full invariants are in
[backend/internal/email/README.md](../../../backend/internal/email/README.md),
and the decision is recorded as MAIL-3 in [docs/adr/mail.md](../../adr/mail.md).

## Cutover and rollback

Infrastructure (`ses_enabled`) and sending (`ses_sending`) are separate tfvars
flags, so the AWS side can be created and verified before any mail moves.

1. Complete the [first rollout](../aws-setup.md#first-rollout). DKIM and
   MAIL FROM must both be *Successful*, the event worker running and
   production access granted. In the sandbox, mail to any unverified member
   is refused.
2. Set `"ses_sending": true` in `terraform/terraform.tfvars.json` and deploy.
   The host then sets `EMAIL_PROVIDER=ses`. The API refuses to start if the
   AWS keys or the configuration set are missing.
3. Send yourself a magic link and check that `email_sends` gets a row whose
   status moves to `queued`, then `accepted`, within a minute.

**Rollback:** set `"ses_sending": false` and deploy. App mail goes back to
Stalwart at once. The event worker keeps running while `ses_enabled` is on,
so events for mail already sent through SES still arrive. Never roll back with
`ses_enabled`: the deploy would stop the worker, and Terraform refuses the
plan (`prevent_destroy`) rather than delete the identity and DNS records.

## Leveringslogg and the suppression list

The faktura delivery log (`GET /api/v1/admin/financials/invoices/{id}/delivery-log`)
reads the faktura's `email_sends` rows (`related_type = 'invoice'`, which covers
the faktura mail, its resends, reminders and a credit note's own mail) and
their `mail_delivery_events`. Sends from before the faktura's first row come
from `sent_at` and the audit log and are matched against the Stalwart journal,
as are rows with `provider = 'stalwart'`. That journal path is read-only legacy
and goes away with the `brygge-mail-log` units 35 days after the cutover
(BRY-594). The list badge (`GET …/invoices/delivery-statuses`) is the latest
row per faktura, one query for the club, tables only. A Stalwart row's status
comes from the journal outcome sweep
([stalwart-internals.md § Outcome sweep](stalwart-internals.md#outcome-sweep-journalsweep)),
which BRY-594 removes along with the helper; after the cutover only the SES
event worker writes outcomes. The member list's bounce badge
(`GET /admin/users/email-bounces`) reads the same table.

SES puts an address on the account-level suppression list after a hard bounce
or a complaint and then refuses every send to it; the event is a `Bounce` with
subtype `OnAccountSuppressionList`, stored as status `suppressed`
([suppression list](https://docs.aws.amazon.com/ses/latest/dg/sending-email-suppression-list.html)).
A hard bounce caused by our own fault (BRY-586) suppresses a good address, so
the delivery log offers **Fjern fra blokkeringsliste** on an SES send whose
status is `suppressed`, or `rejected` with a `Permanent` bounce event. It calls
`POST …/invoices/{id}/email-sends/{sendID}/unsuppress` (treasurer/admin, fresh
TOTP), which reads the address from the send row (the client never posts an
address), reads the list entry with SESv2
[`GetSuppressedDestination`](https://docs.aws.amazon.com/ses/latest/APIReference-V2/API_GetSuppressedDestination.html)
and then calls
[`DeleteSuppressedDestination`](https://docs.aws.amazon.com/ses/latest/APIReference-V2/API_DeleteSuppressedDestination.html)
with the app user's key; the IAM policy above grants both. `NotFoundException`
on either (already off the list) counts as success. Without `SES_REGION` the
route answers 503. It never resends; the treasurer resends the faktura
afterwards. Each removal is audited as `invoice.recipient_unsuppressed`.

Never remove an address that complained: sending to it again is what gets an
SES account paused. A `suppressed` send does not say why the address was
listed, so the action refuses (409, no SES delete, no audit row) when any
`email_sends` row of the club to that address has status `complaint`, and
again when SES's list entry has reason `COMPLAINT` (a complaint the club's
rows no longer hold, e.g. past the 13-month retention).

To do the same by hand (for an address that is not on a faktura send):

```bash
aws sesv2 get-suppressed-destination --email-address <address> --region eu-north-1
aws sesv2 delete-suppressed-destination --email-address <address> --region eu-north-1
```

## Delivery-event worker

See [backend/internal/sesevents/README.md](../../../backend/internal/sesevents/README.md).
