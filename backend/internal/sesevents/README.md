# `sesevents/` — Amazon SES delivery-event worker

Consumes the delivery events Amazon SES publishes for app mail and records them against the `email_sends` row the sender wrote, so the app knows whether a magic link or faktura actually reached the recipient's server. Setup, AWS resources and the runbook: [docs/developer/mail/ses.md](../../../docs/developer/mail/ses.md). Env vars: [configuration.md § Amazon SES delivery events](../../../docs/developer/configuration.md#amazon-ses-delivery-events). Metrics: [instrumentation.md § SES delivery events](../../../docs/developer/otel/instrumentation.md#ses-delivery-events).

## Files

| File | What |
|---|---|
| `event.go` | SES event-publishing JSON types and `Parse`, which turns one SQS body into per-recipient `Record`s (one per `mail_delivery_events` row). |
| `status.go` | Event → `email_sends.status` mapping, the rank rule, and the recipient domain class used as a metric attribute. |
| `store.go` | `Store.Apply` (one transaction per SES event) and `Store.PurgeExpired` (13-month retention). |
| `suppression.go` | `SuppressionList.Remove`: SESv2 `DeleteSuppressedDestination` for the delivery log's "remove from suppression list" action ([API](https://docs.aws.amazon.com/ses/latest/APIReference-V2/API_DeleteSuppressedDestination.html)); `NotFoundException` is success. `SuppressionAPI` is the interface the tests fake. |
| `worker.go` | `Worker.Run`: SQS long-poll loop, delete-after-commit, backoff, pause while the store is down, DLQ depth sampling. `SQSAPI` and `EventStore` are the small interfaces the tests fake. |
| `metrics.go` | `brygge.email.delivery`, `brygge.ses.events.received`, `brygge.ses.dlq.messages`. |
| `testdata/` | Event bodies modelled on the [documented SES examples](https://docs.aws.amazon.com/ses/latest/dg/event-publishing-retrieving-sns-examples.html), with synthetic addresses and the `send_id` / `kind` tags. |

## Event flow

```
sender ──SMTP (X-SES-CONFIGURATION-SET, X-SES-MESSAGE-TAGS send_id, kind)──▶ SES
SES configuration set ──▶ SNS topic ──(raw message delivery)──▶ SQS queue ──(5 receives)──▶ DLQ
Worker: ReceiveMessage (wait 20 s, max 10) → Parse → Store.Apply (one tx) → DeleteMessage
```

- The SNS subscription uses [raw message delivery](https://docs.aws.amazon.com/sns/latest/dg/sns-large-payload-raw-message-delivery.html), so the SQS body is the SES event JSON itself. `Parse` also accepts an SNS `{"Type":"Notification","Message":"…"}` envelope in case that setting is lost.
- Event fields and types follow [Contents of event data that Amazon SES publishes to Amazon SNS](https://docs.aws.amazon.com/ses/latest/dg/event-publishing-retrieving-sns-contents.html). Recorded: `Send`, `Delivery`, `DeliveryDelay`, `Bounce`, `Complaint`, `Reject`, `Rendering Failure`. `Open`, `Click` and `Subscription` are not enabled on the configuration set; if one arrives it is deleted unrecorded (`event_type="ignored"` on the received counter).
- Long polling with the 20 s maximum wait keeps an idle queue at three requests a minute ([short and long polling](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-short-and-long-polling.html)). The queue URL is resolved once from its name with `GetQueueUrl`, retried with exponential backoff (1 s → 1 min) so a boot during a network blip still starts the worker. Receive errors back off the same way. Cancelling the context stops the loop, including an in-flight long poll.

## Idempotency

SQS delivers at least once, and a message whose delete failed comes back after the visibility timeout. Every event row carries the unique key `(ses_message_id, event_type, event_at, recipient)` with `NULLS NOT DISTINCT`, and the insert is `ON CONFLICT DO NOTHING`. A redelivered event inserts nothing, so `Apply` reports it as not new: the send's status is not touched again and `brygge.email.delivery` does not count it twice. `brygge.ses.events.received` does count redeliveries; it measures queue traffic, not outcomes.

The message is deleted only after the transaction commits. A crash between commit and delete is safe for the same reason.

## Linking events to sends

`mail.tags.send_id[0]` is the `email_sends.id` the sender generated before the SMTP `DATA`. When the tag is a valid UUID and the row exists, the event links to it; otherwise the store falls back to `email_sends.ses_message_id`. Neither matching → the event is still stored with `send_id` NULL (e.g. a send already purged, or mail sent outside the app through the same configuration set). Linking sets `ses_message_id` on the send if it was still NULL, and `last_event_at` to the latest `event_at` seen.

## Ordering and the rank rule

SNS → SQS gives no ordering guarantee, so a `Send` event can arrive after the `Delivery` it preceded. Each mapped status has a rank, and a send moves to the event's status only when the new rank is `>=` the current one:

| Event | Status | Rank |
|---|---|---|
| (sender's own: `sending`, `submitted`, `failed`, `unknown`) | — | 0 |
| Send | `queued` | 1 |
| DeliveryDelay | `deferred` | 2 |
| Delivery | `accepted` | 3 |
| Bounce `Transient` | `rejected_temporary` | 3 |
| Bounce `Permanent` / `OnAccountSuppressionList` or `OnTenantSuppressionList` | `suppressed` | 4 |
| Bounce `Permanent` (other), Bounce `Undetermined`, Reject, Rendering Failure | `rejected` | 4 |
| Complaint with `complaintSubType` `OnAccountSuppressionList` / `OnTenantSuppressionList` | `suppressed` | 4 |
| Complaint (other) | `complaint` | 5 |

A complaint with a suppression-list subtype is SES reporting that it did not send the message because an earlier complaint suppressed the address, so it maps to `suppressed`, not to a fresh complaint. Equal ranks advance: `accepted` and `rejected_temporary` are both final per recipient (SES publishes a `Transient` bounce only after it stops retrying), so for a single-recipient send the tie never arises and the later event wins otherwise. `Undetermined` bounces map to `rejected`: SES could not classify them, but the message was not delivered and should surface for review. The `email_sends` row is locked `FOR UPDATE` before the comparison so two events for one send applied concurrently do not both read the old status.

## Failures and the DLQ

| Failure | What the worker does |
|---|---|
| Body is not a parseable SES event | Logs a warning and leaves the message. It is received again after the visibility timeout (60 s); after `maxReceiveCount` (5) SQS moves it to the DLQ ([dead-letter queues](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-dead-letter-queues.html)). |
| Database error | No delete, and the rest of the batch is left too. The worker then stops receiving and pings the database with backoff (1 s → 1 min) until it answers. Every receive counts towards `maxReceiveCount`, so polling on through an outage would move healthy events to the DLQ within five minutes; pausing keeps them on the main queue (4-day retention). An event the store rejects while the database is up (ping succeeds) is retried and reaches the DLQ after five receives like any poison message. |
| `DeleteMessage` fails | Logs; the redelivery is dropped as a duplicate. |
| AWS receive error | Exponential backoff, then retry. |

Every minute the worker reads the DLQ's `ApproximateNumberOfMessages` into `brygge.ses.dlq.messages` and logs a warning while it is non-zero. The DLQ keeps messages 14 days; anything there needs a human (inspect in the SQS console, fix the cause, redrive).

## Retention

`email_sends` (by `created_at`) and `mail_delivery_events` (by `received_at`) are kept 13 months: both hold recipient addresses. `Store.PurgeExpired` runs in the API's daily retention sweep in `cmd/api/main.go`, alongside the member-PII purge, whether or not the worker is enabled, because the sender also records Stalwart sends in `email_sends`. Deleting a send cascades its events. Erasure of a member's mail history on GDPR deletion is a separate path (`accounting.DeleteMemberMailLog`).

## Common misses

- `remote_mta` is only the receiving server's IP from a `Delivery` event (`remoteMtaIp`). `reportingMTA` on `Bounce` and `DeliveryDelay` names the MTA that reported the outcome, which for the bounces SES generates is SES's own host, so it is not copied into `remote_mta`; the dev view exposes it as `raw_reporting_mta`. For "which provider refuses us", use `recipient_domain` and the diagnostic.
- The `kind`, `recipient_domain_class` and `smtp_status_class` metric attributes are closed sets (the status class is at most 3 × 8 values). A new `email.Kind` value must also be added to `Kinds` in `status.go`, or it counts as `other`.
- `Raw` stores the whole SES event, including `mail.commonHeaders` (subject, To). It is personal data: dev-query views must mask it.
- Tests use synthetic fixtures only; never paste a real event from the queue into `testdata/`.
