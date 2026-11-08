# AWS account setup

Back to [developer docs](README.md).

Brygge uses AWS for one thing today: **Amazon SES** for app mail and its
delivery events (SES → SNS → SQS, polled by the API). Everything on the AWS
side is created by Terraform in
[`terraform/aws/ses.nix`](../../terraform/aws/ses.nix). This page covers what
a person does by hand: the AWS account, the two access keys, the tfvars flags,
and the order of the first rollout. What each AWS resource is for, the DNS
records, and the cutover itself are in [mail/ses.md](mail/ses.md).

## Two principals, two keys

| | Terraform principal | App user |
|---|---|---|
| Who creates it | You, in the IAM console | Terraform (`aws_iam_user.app`) |
| Name | Anything **not** starting with `brygge-`, e.g. `terraform-brygge` | `brygge-<slug>-app` |
| Used by | `nix run .#tf-plan` / `.#tf-apply` on your machine | The Brygge API on the host (SMTP sending, SQS worker, un-suppress) |
| Key lives in | `terraform/terraform.tfvars.json` (`aws_access_key`, `aws_secret_key`) | `/etc/brygge/env` on the host (`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`) |
| Console use case | *Command Line Interface (CLI)* | *Application running outside AWS* |
| Between uses | Deactivated, and reactivated before **any** `tf-plan`/`tf-apply` | Always active |

The console's "use case" choice is only a label; it does not change what the
key can do. Never create an access key for the root user.

### Why long-term keys

AWS recommends temporary credentials over access keys. Neither key can use them
today:

- **App user:** SES SMTP authenticates with a password derived from a
  long-term secret key; temporary credentials cannot sign in to SMTP
  ([SMTP credentials](https://docs.aws.amazon.com/ses/latest/dg/smtp-credentials.html)).
  Brygge derives the password at startup, so there is no separate SMTP secret.
  The key is narrowly scoped (send as the club domain, read the event queue,
  read/remove suppression-list entries) and never enters Terraform state.
- **Terraform principal:** the tf wrappers export `AWS_ACCESS_KEY_ID` /
  `AWS_SECRET_ACCESS_KEY` with the *Hetzner* Object Storage keys for the state
  backend, so the `aws` provider takes explicit keys from tfvars. Our provider
  block passes no session `token` and there is no tfvars variable for one, so
  short-lived SSO credentials do not work yet (BRY-668).

  Once the SES resources are in state, **every** plan and apply calls AWS (the
  provider checks the key and refreshes the 12 AWS resources), including runs
  that only change Hetzner DNS or the server. You can keep the key deactivated
  between Terraform sessions, but a deactivated key fails every plan with
  `InvalidClientTokenId`: reactivate it first.

## Terraform principal permissions

Create an IAM user (e.g. `terraform-brygge`) or a group for it, then
**Create policy → JSON** and attach this. Replace `<domain>` with the club
domain, and `eu-north-1` if `aws_region` differs. The ready-made
`AmazonSESFullAccess` is not enough (Terraform also creates SNS, SQS and IAM
resources), and adding `IAMFullAccess` would amount to admin on the account.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "SesIdentityAndConfigSet",
      "Effect": "Allow",
      "Action": [
        "ses:CreateEmailIdentity", "ses:GetEmailIdentity", "ses:DeleteEmailIdentity",
        "ses:PutEmailIdentity*",
        "ses:CreateConfigurationSet", "ses:GetConfigurationSet", "ses:DeleteConfigurationSet",
        "ses:PutConfigurationSet*",
        "ses:CreateConfigurationSetEventDestination", "ses:GetConfigurationSetEventDestinations",
        "ses:UpdateConfigurationSetEventDestination", "ses:DeleteConfigurationSetEventDestination",
        "ses:TagResource", "ses:UntagResource", "ses:ListTagsForResource"
      ],
      "Resource": [
        "arn:aws:ses:eu-north-1:*:identity/<domain>",
        "arn:aws:ses:eu-north-1:*:configuration-set/brygge-*"
      ]
    },
    {
      "Sid": "SnsEventsTopic",
      "Effect": "Allow",
      "Action": [
        "sns:CreateTopic", "sns:DeleteTopic",
        "sns:GetTopicAttributes", "sns:SetTopicAttributes",
        "sns:Subscribe", "sns:Unsubscribe",
        "sns:GetSubscriptionAttributes", "sns:SetSubscriptionAttributes",
        "sns:TagResource", "sns:UntagResource", "sns:ListTagsForResource"
      ],
      "Resource": "arn:aws:sns:eu-north-1:*:brygge-*"
    },
    {
      "Sid": "SqsEventsQueues",
      "Effect": "Allow",
      "Action": [
        "sqs:CreateQueue", "sqs:DeleteQueue", "sqs:GetQueueUrl",
        "sqs:GetQueueAttributes", "sqs:SetQueueAttributes",
        "sqs:TagQueue", "sqs:UntagQueue", "sqs:ListQueueTags"
      ],
      "Resource": "arn:aws:sqs:eu-north-1:*:brygge-*"
    },
    {
      "Sid": "IamAppUser",
      "Effect": "Allow",
      "Action": [
        "iam:CreateUser", "iam:GetUser", "iam:DeleteUser",
        "iam:TagUser", "iam:UntagUser", "iam:ListUserTags",
        "iam:PutUserPolicy", "iam:GetUserPolicy", "iam:DeleteUserPolicy",
        "iam:ListGroupsForUser"
      ],
      "Resource": "arn:aws:iam::*:user/brygge-*"
    }
  ]
}
```

- The tag actions are there because the provider puts a `club` default tag on
  every resource. `iam:ListGroupsForUser` is what the provider reads before it
  deletes the user.
- The first apply proves the create and read paths. The delete paths only run
  at teardown; if one is refused there, see the `AccessDenied` note below. AWS
  may want `sns:Unsubscribe` and the subscription-attribute actions on
  `"Resource": "*"` rather than the topic ARN; if so, give them their own
  statement.
- Nothing is needed for Terraform state (it lives in Hetzner Object Storage) or
  for `sts:GetCallerIdentity` (allowed for every key).
- The policy can attach any inline policy to `brygge-*` users. That is why the
  Terraform principal itself must not be named `brygge-…`: it could grant
  itself anything. It has no `iam:CreateAccessKey`, so it cannot mint a key
  for the app user either; you do that in the console.
- A missing action shows up as `AccessDenied … is not authorized to perform:
  <action>` in `tf-plan`/`tf-apply`. Add the named action and rerun.

## tfvars flags

Two flags in `terraform/terraform.tfvars.json`, both default `false`:

| Flag | Read by | What it does |
|------|---------|--------------|
| `ses_enabled` | Terraform **and** the deploy (`nix/host.nix`) | Terraform creates the SES/SNS/SQS/IAM resources and 5 DNS records. The deploy sets `SES_REGION`, `SES_CONFIGURATION_SET`, `SES_EVENTS_QUEUE_NAME`, `SES_EVENTS_DLQ_NAME`, which start the delivery-event worker. |
| `ses_sending` | The deploy only | Sets `EMAIL_PROVIDER=ses`: app mail goes through SES instead of Stalwart. Needs `ses_enabled`. |

**`ses_enabled` stays `true` from the first apply onwards.** It is not a
"Terraform is running" switch: the deploy reads it too, and with it off the
host gets no `SES_*` variables, so the event worker does not run. Switching it
off and running `tf-apply` would delete every SES resource and the DKIM and
MAIL FROM records. To stop that, the SES identity has `prevent_destroy`; such
a plan fails with `Error: Instance cannot be destroyed`. If you see that, set
`ses_enabled` back to `true`.

**`ses_sending` is the cutover and rollback switch.** Turning it off moves app
mail back to Stalwart at the next deploy and leaves the AWS side alone.

Both flags are migration scaffolding and are expected to go once SES is the
default for every deployment (BRY-668).

## First rollout

1. **Terraform principal.** Create the IAM user and its policy (above) and an
   access key with use case *CLI*.
2. **tfvars.** Add, without committing the file (the pre-commit hook refuses
   real values):

   ```json
   "ses_enabled": true,
   "aws_region": "eu-north-1",
   "aws_access_key": "<terraform key id>",
   "aws_secret_key": "<terraform secret>"
   ```

   Set `club_slug` before the first apply; it names every resource
   (`brygge-<slug>`, …) and defaults to `brygge`. Do not change it afterwards:
   a new slug replaces the configuration set, topic, queues and app user
   (losing the hand-made app key) and changes the names the host uses, and
   `prevent_destroy` does not stop that.
3. **Apply.** `nix run .#tf-plan`, review, `nix run .#tf-apply`. A first apply
   adds 17 resources: 12 on AWS (identity, MAIL FROM, configuration set, event
   destination, SNS topic + policy + subscription, two queues + queue policy,
   IAM user + policy) and 5 DNS records (3 DKIM CNAMEs, `send` MX and TXT).
4. **Deactivate the Terraform key** if you like: IAM → Users → the principal
   → Security credentials → Actions → Deactivate. Reactivate it before the
   next `tf-plan`/`tf-apply` of any kind (see above). Leave `ses_enabled` at
   `true`.
5. **Verify the domain.** SES console (region eu-north-1) → Identities →
   `<domain>`: *DKIM configuration* and *MAIL FROM configuration* both
   **Successful**. With the DNS records already published this takes minutes.
6. **App key.** IAM → Users → `brygge-<slug>-app` → Security credentials →
   Create access key → *Application running outside AWS*. IAM users are global,
   so the user does not appear in a regional view such as Resource Explorer
   filtered to eu-north-1; open it from the IAM console. Copy both values once
   into `/etc/brygge/env` on the host (root-owned, `0400`):

   ```sh
   AWS_ACCESS_KEY_ID=<app key id>
   AWS_SECRET_ACCESS_KEY=<app secret>
   ```

7. **Production access.** SES console → Account dashboard → Request
   production access. Until AWS grants it (usually within a day) the account is
   in the sandbox: only verified recipients, 200 messages a day
   ([quotas](https://docs.aws.amazon.com/ses/latest/dg/quotas.html)).
8. **Deploy** with `ses_enabled: true` and `ses_sending` absent or `false`.
   Mail still goes through Stalwart; the event worker starts. Check:

   ```sh
   journalctl -u brygge --since -10min | grep -iE "SES events|SQS queue"
   ```

   Expect `SES events worker started`. `SQS queue URL lookup failed` means the
   app key is missing or wrong in `/etc/brygge/env`; `SES events worker
   disabled (SES_EVENTS_QUEUE_NAME not set)` means the deploy ran with
   `ses_enabled` off.
9. **Cut over** when production access is granted:
   [mail/ses.md § Cutover and rollback](mail/ses.md#cutover-and-rollback).
   In the sandbox you can rehearse by verifying your own address (SES →
   Identities → Create identity → Email address) and sending yourself a magic
   link.

## Rotating keys

- **App key:** create a second key on `brygge-<slug>-app` (a user may hold
  two), replace both lines in `/etc/brygge/env`, restart `brygge.service`,
  check the worker log line above, then deactivate and delete the old key.
- **Terraform key:** create a new key, update tfvars, delete the old one.

## Teardown

Deliberately removing SES from a deployment (not a rollback; for that use
`ses_sending: false`):

1. Deploy with `ses_sending: false` so no mail depends on SES.
2. Set `ses_enabled: false` and deploy, so the host drops the `SES_*`
   variables and the event worker stops. Remove the AWS lines from
   `/etc/brygge/env`.
3. Delete the app user's access key in the IAM console (IAM → Users →
   `brygge-<slug>-app` → Security credentials). Terraform cannot delete a user
   that still has a key (`DeleteConflict`), and the Terraform principal has no
   right to delete keys.
4. Remove the `lifecycle.prevent_destroy` line from the identity in
   `terraform/aws/ses.nix`, then `tf-plan` (expect 17 to destroy) and
   `tf-apply`.
