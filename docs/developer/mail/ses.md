# Amazon SES — app mail and delivery events

Back to [mail docs](README.md).

Brygge's own outbound mail (magic links, fakturas, reminders, credit notes,
contact replies) is sent through Amazon SES. Stalwart keeps inbound mail and
the board's own mailboxes. SES reports what happened to every message
(accepted by the receiving server, deferred, bounced, complained about) and
Brygge stores those events per send, which is what the Leveringslogg shows.

Everything on the AWS side is created by Terraform in
[`terraform/ses.nix`](../../../terraform/ses.nix), gated on the tfvars flag
`ses_enabled`. With the flag off (the default) Terraform creates nothing on
AWS, publishes no SES DNS records and makes no AWS API calls, and the host
sets none of the `SES_*` variables.

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
| `aws_iam_user` + inline policy | `brygge-<slug>-app` | Brygge's runtime principal: `ses:SendRawEmail` on the identity and configuration set (what the SMTP interface authorises against), receive/delete/visibility/attribute calls on the queue and DLQ, and get/delete on the account suppression list (no resource ARN exists for those, so `*`). |

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
empty keys the plan fails on a precondition before any AWS call.

These Terraform keys belong to an admin/bootstrap principal in the owner's
account. They are separate from the app user's runtime key.

## DNS records

Published by [`terraform/dns.nix`](../../../terraform/dns.nix) when
`ses_enabled` is true:

| Type | Name | Value |
|------|------|-------|
| CNAME ×3 | `<token>._domainkey` | `<token>.dkim.amazonses.com.` (tokens from the identity) |
| MX | `send` | `10 feedback-smtp.<aws_region>.amazonses.com.` |
| TXT | `send` | `"v=spf1 include:amazonses.com ~all"` |

Untouched: the apex MX (inbound stays on Stalwart), the apex SPF
(`v=spf1 mx -all`; SES mail authenticates SPF on `send.`, not the apex) and
`_dmarc` (relaxed alignment). Two rules:

- **Never `bounce.`** for the MAIL FROM: that name is Stripe's custom email
  domain CNAME.
- **Never `aspf=s`** in DMARC: SES's SPF passes on `send.<domain>`, which only
  aligns under relaxed SPF alignment.

## Owner setup runbook

1. **Bootstrap credentials.** In the AWS account, create (or reuse) an
   admin/bootstrap IAM principal able to manage SES, SNS, SQS and IAM, and an
   access key for it.
2. **Fill tfvars** by hand in `terraform/terraform.tfvars.json` (never commit
   it):

   ```json
   "ses_enabled": true,
   "aws_region": "eu-north-1",
   "aws_access_key": "<bootstrap key id>",
   "aws_secret_key": "<bootstrap secret>"
   ```

   `club_slug` must be set (it names every resource).
3. **Plan and apply.** `nix run .#tf-plan`, review, then `nix run .#tf-apply`.
   Expect the SES/SNS/SQS/IAM resources plus five `hcloud_zone_rrset`
   records.
4. **Create the app key by hand.** IAM console → Users →
   `brygge-<slug>-app` → Security credentials → Create access key
   ("Application running outside AWS"). Copy both values once.
5. **Install the secrets** in `/etc/brygge/env` on the host (root-owned,
   0400):

   ```sh
   AWS_ACCESS_KEY_ID=<app key id>
   AWS_SECRET_ACCESS_KEY=<app secret>
   ```

   The non-secret `SES_REGION`, `SES_CONFIGURATION_SET`,
   `SES_EVENTS_QUEUE_NAME` and `SES_EVENTS_DLQ_NAME` come from
   `nix/host.nix` once the deploy picks up `ses_enabled`.
6. **SMTP password.** SES SMTP authenticates with the app key id as username
   and a password derived from the secret key with AWS's SigV4 algorithm
   (region-specific). See
   [Obtaining SES SMTP credentials](https://docs.aws.amazon.com/ses/latest/dg/smtp-credentials.html).
   The SMTP endpoint for the region is listed in
   [SES endpoints and quotas](https://docs.aws.amazon.com/general/latest/gr/ses.html).
7. **Verify.** SES console → Identities → `<domain>`: DKIM status
   *Successful* and custom MAIL FROM status *Successful* (DNS propagation can
   take up to 72 hours, usually minutes). Or:

   ```sh
   aws sesv2 get-email-identity --email-identity <domain> --region eu-north-1 \
     --query '{dkim:DkimAttributes.Status,mailFrom:MailFromAttributes.MailFromDomainStatus}'
   ```
8. **Request production access.** New accounts start in the sandbox: only
   verified recipients, 200 messages per 24 hours, 1 message per second
   ([quotas](https://docs.aws.amazon.com/ses/latest/dg/quotas.html)). Request
   production access from the SES console
   ([request production access](https://docs.aws.amazon.com/ses/latest/dg/request-production-access.html));
   AWS usually answers within 24 hours.

## Delivery-event worker

See backend/internal/sesevents/README.md.
