# Alerting

Brygge's alerts are Prometheus rules evaluated by the upstream (home-cluster) Prometheus on the telemetry each club's host exports, routed by Alertmanager to [ntfy](https://ntfy.sh) push notifications. Email is not a channel: mail failing is one of the things being alerted on.

```
club VM: app + host collector ──OTLP──▶ home ingress ──▶ Prometheus ──rules──▶ Alertmanager ──webhook──▶ ntfy ──▶ phone
```

Nothing here is evaluated on the club's host, and nothing is log-based: every alert is PromQL over a metric. The repo owns the rules, their tests, the Alertmanager fragment and the runbook; the home cluster loads them per deployment (recipe below).

| File | What |
|---|---|
| [alerts/brygge-alerts.rules.yaml](alerts/brygge-alerts.rules.yaml) | Rule groups with `__SERVICE_NAMESPACE__`, `__CLUB_SLUG__`, `__RUNBOOK_BASE__` placeholders |
| [alerts/brygge-alerts.test.yaml](alerts/brygge-alerts.test.yaml) | `promtool test rules` cases, against the rules rendered for `example.invalid` |
| [alerts/alertmanager-ntfy.yaml](alerts/alertmanager-ntfy.yaml) | Route and ntfy receivers to merge into the Alertmanager config |
| `scripts/render-brygge-alerts.sh` | Substitutes the placeholders; prints a `PrometheusRule` CR or (`--groups`) the plain groups |
| [alerts-runbook.md](alerts-runbook.md) | What each alert means and what to do |

The placeholders are `__NAME__`, not `${NAME}`, because Flux's post-build substitution in the home-cluster repo would replace `${…}`.

## Label and job contract

Series carry `job` = `<service.namespace>/<service.name>`, built by the upstream Prometheus path from the resource attributes (it sets no `job` without `service.name`). `service.namespace` is the club domain, added by the host collector ([local-collector.md](local-collector.md)). Rules select on `job` with `=` matchers.

| `job` | Set by | Metrics the rules use |
|---|---|---|
| `<domain>/brygge-host` | `resource/host-identity` in the host collector | `system_memory_usage_bytes` (heartbeat), `system_filesystem_utilization_ratio{mountpoint,type}`, `tlscheck_time_left_seconds{tlscheck_target}`, `file_size_bytes{file_name}`, `file_mtime_seconds{file_name}` |
| `<domain>/stalwart` | `resource/stalwart-identity` (journal-derived counts) | `stalwart_journal_remote_rejections{server_address,smtp_code}`, `stalwart_journal_dkim_signer_missing`, `stalwart_journal_dmarc_dkim_failures`, `stalwart_journal_bounces{kind}` |
| `<domain>/brygge-api` | the API's `ServiceName` constant ([instrumentation.md](instrumentation.md)) | `brygge_email_send_total{kind,provider,result}` (also the ApiSilent heartbeat), `brygge_email_delivery_total{kind,result,recipient_domain_class,smtp_status_class}`, `brygge_ses_events_received_total{event_type}`, `brygge_ses_dlq_messages`, `brygge_mail_queue_oldest_age_seconds`, `brygge_mail_queue_last_success_seconds`, `brygge_payment_webhook_failure_total{provider,event_type}`, `brygge_payment_attempt_oldest_authorized_age_seconds`, `brygge_vipps_report_sync_last_success_seconds{msn}`, `brygge_vipps_unbooked_charge_oldest_age_seconds{msn}`, `brygge_vipps_ready_payout_oldest_age_seconds`, `brygge_accounting_auto_booking_last_run_failed{provider}` |

Shapes the rules rely on:

- `stalwart_journal_*` are gauges holding per-batch event counts. Rules use `sum_over_time`, never `rate`/`increase`. A series exists only after its first event, so "no series" means "no events", not a failure. `smtp_code` is `none` for connection/TLS failures and `server_address` can be `unknown`; deferrals are matched with `smtp_code=~"4.."`.
- `tls_check` reports every 10 minutes and `file_stats` every 5, at or past Prometheus' 5-minute lookback, so their rules read `last_over_time(…[20m])` / `[15m]`. `tlscheck_*` series also carry the certificate's CN and issuer, which change on renewal: the rule takes `min by (tlscheck_target)`.
- A failed TLS probe produces no data point at all, so TlsProbeFailing alerts on a target's absence. `absent_over_time` needs the exact target, so the rule lists the collector's `tls_check` endpoints (`<domain>:443`, `mail.<domain>:443`, `mail.<domain>:465`); a change to that list in `nix/host.nix` needs the same change in the rule.
- `brygge_email_send_total` exists only when the API has SMTP configured (it then pre-creates every `kind` × `result` series at 0 for each configured `provider`: 14 for Stalwart alone, 28 with SES and the Stalwart fallback). A deployment without SMTP would see ApiSilent fire permanently; drop that rule from its rendered copy.
- `brygge_email_delivery_total` and `brygge_ses_events_received_total` exist only where the SES events worker runs; every `kind` × `result` × `recipient_domain_class` series (with `smtp_status_class="none"`) and every `event_type` series is pre-created at 0. A bounce that carries an SMTP status (`5.1`, `5.7`, …) starts a new series at 1, which `increase()` alone reads as no change, so SesDeliveryFailed takes `increase()` only for series that existed an hour ago and the current value for those that did not (`(increase(x[1h]) and x offset 1h) or (x unless x offset 1h)`); its rule tests start such a series from nothing. Their labels are closed sets: an alert can say which kind of mail failed and how, at which class of provider and with which SMTP status class, but never the exact recipient domain, full status or diagnostic. SesDeliveryFailed's description therefore carries the `just prod-query` that returns them. There is no log-based alerting to carry free text instead (see the top of this page), and the app posts nothing to ntfy itself.
- `brygge_ses_dlq_messages` is a synchronous gauge exported with cumulative temporality: the SDK keeps exporting the last recorded value while the process lives, even if the worker's sampling loop stops. Its absence therefore only ever means the API stopped exporting, which ApiSilent already covers, so there is no "SES worker absent" rule. A worker that is stuck or misconfigured while the API runs shows up as SesEventsMissing as soon as mail goes out through SES; while nothing is sent there is nothing to lose. An absence rule would also fire for every club without SES unless the rules knew per club whether SES is on.
- SesEventsMissing selects `brygge_email_send_total{provider="ses"}`; the `provider` attribute (`ses` or `stalwart`) is on that counter only from the SES sending change (BRY-589) on, and a club that never sends through SES has no traffic on that series.
- `brygge_payment_webhook_failure_total` is a counter of 5xx webhook answers; both providers' series (every allowlisted `event_type` plus `other`) are pre-created at 0 on start, so `increase()` sees the first failure. It carries no event id, so PaymentWebhookFailing can't tell one event retried from several of one type. It compares two one-hour windows half an hour apart (`increase(x[1h])` and `increase(x[1h] offset 90m)`, both above 0.5) rather than counting inside one window because both providers retry a failed event within seconds to minutes first (a burst), then hours apart (payments.md § Webhook retry windows): a burst alone is a database blip that healed, a failure in both windows is an event that is still being refused. The gap means a burst up to half an hour long is never in both (adjacent windows split a multi-minute burst at their boundary and page for it); hourly retries keep both windows filled, and `keep_firing_for: 15m` bridges retries that drift past the hour so the alert doesn't flap. Detection takes about 1h30 from the first failure and resolution about 1h15 after the last. `keep_firing_for` needs Prometheus 2.42+ and a Prometheus Operator whose `PrometheusRule` CRD has `keepFiringFor` (0.64+).
- `brygge_payment_attempt_oldest_authorized_age_seconds` is read from `payment_attempts` at each export (5 s timeout) and is 0 when no attempt is `authorized`. It goes absent, not 0, when the read fails, so PaymentAttemptStuckAuthorized resolves while the database is unreachable and the 5xx alert and ApiSilent carry that case.
- `brygge_vipps_report_sync_last_success_seconds` is the Unix time of the automatic run's last Report API pull where every date answered. Nothing is exported before a merchant's first success, so after a restart whose pulls fail the series is absent. VippsReportSyncStale reads `last_over_time(…[3d])`, so the last value still counts and the alert fires 36 h after that success whether or not the API restarted in between; with no sample for three days (or none ever, on a fresh deployment), its absent branch fires instead, unless the API is silent altogether (ApiSilent). The two branches carry different labels (`msn`, `job`); `keep_firing_for: 30m` holds the `msn` alert while the `job` one goes through `for: 30m`, so the switch does not resolve and re-fire. The API-silent test in the absent branch uses the `brygge_email_send_total` heartbeat, which exists only with SMTP configured: on a deployment without SMTP the absent branch is always suppressed and only the `msn` branch can fire. A deployment without Vipps Report API credentials never syncs: drop VippsReportSyncStale from its rendered copy, as one without SMTP drops ApiSilent.
- `brygge_accounting_auto_booking_last_run_failed` is 1 or 0 per `provider` and absent before the provider's first run. A busy booking slot and a failed Report API pull are not failures here: the next run retries the first, and the sync alert covers the second. AutomaticBookingFailed resolves on the next good run (24 h, the next start, or for Stripe the next event).
- `brygge_vipps_unbooked_charge_oldest_age_seconds` and `brygge_vipps_ready_payout_oldest_age_seconds` are read from the database at each export and go absent, not 0, when the read fails. The charge gauge has a series only for a merchant with an unbooked charge; the payout gauge is 0 when nothing is ready. Both count only clubs with automatic booking on (on the per-charge model), and leave out rows dated in a reference, missing, closed or locked year: the treasurer's fiscal year notice (BRY-575) covers what an automatic pass would book there, and the rest waits visibly in Tildel, so January does not page the operator for the year nobody has opened. Both clocks start no earlier than the last change to the fiscal period covering the row's date (`fiscal_periods.updated_at`, set on create, close, reopen and reference changes), so creating or reopening a year does not page at once for the rows that waited on it. The charge gauge counts every amount (a zero or negative charge holds its payout up like any other) and labels a charge without a merchant number `msn="unknown"`. A deposit a person declined ("Angre innbetalingen") is left out: a person took it out of the sweep on purpose. The payout clock is `greatest(deposit created_at, last posted_at and last dismissed_at of the settlement's charges, the years' updated_at)`, not the bank date: a deposit uploaded days late, or whose last charge is booked or dismissed days later, would otherwise page at once although the sweep has not had a chance yet. On this clock a healthy deposit is cleared by its upload or the next night, so 48 h means the sweep refuses it.
- The queue gauges exist only on deployments with the queue monitor configured ([instrumentation.md § Mail queue](instrumentation.md#mail-queue-stalwart)); `last_success` is 0 before the first poll, and the other two go absent after 15 minutes without a good poll.

Routing labels are **static on every rule**: `service_namespace`, `club` and `severity`. Series carry only `job`, and `absent_over_time` returns no series labels at all, so Alertmanager can't route on what the data carries. Severity is one of:

| `severity` | Use | ntfy priority (suggested) | Repeat |
|---|---|---|---|
| `page` | Members are or soon will be affected; act now | `urgent` | 4h |
| `warn` | Needs attention within days | `default` | 24h |
| `daily` | Digest-type notices (bounces); no resolved notice | `low` | 24h |

`info` is avoided on purpose: kube-prometheus-stack's `InfoInhibitor` silences `severity="info"`.

## Verify metric names in Prometheus

The rules assume the upstream exporter's default suffixing (unit suffix, `_total` on counters, dots to underscores). Before loading rules for a new deployment, and after any change to the home exporter, check that each name exists for the club. In the Prometheus UI (or `promtool query instant <url> '<expr>'`):

```promql
# Every metric name per job for the club — the three jobs above must appear.
count by (job, __name__) ({job=~"<domain>/.*"})

# The heartbeat HostSilent watches
system_memory_usage_bytes{job="<domain>/brygge-host"}

# Host signals
tlscheck_time_left_seconds{job="<domain>/brygge-host"}
system_filesystem_utilization_ratio{job="<domain>/brygge-host"}
file_mtime_seconds{job="<domain>/brygge-host", file_name=~"system@.*"}

# App
brygge_email_send_total{job="<domain>/brygge-api"}          # 8 series at 0 after start; ApiSilent's heartbeat
brygge_mail_queue_last_success_seconds{job="<domain>/brygge-api"}
brygge_payment_webhook_failure_total{job="<domain>/brygge-api"}  # pre-created at 0 for each provider and event type
brygge_payment_attempt_oldest_authorized_age_seconds{job="<domain>/brygge-api"}  # 0 when nothing is authorized
brygge_vipps_report_sync_last_success_seconds{job="<domain>/brygge-api"}     # one series per merchant after the first good pull
brygge_vipps_unbooked_charge_oldest_age_seconds{job="<domain>/brygge-api"}   # only merchants with an unbooked charge; may be empty
brygge_vipps_ready_payout_oldest_age_seconds{job="<domain>/brygge-api"}      # 0 when nothing is ready
brygge_accounting_auto_booking_last_run_failed{job="<domain>/brygge-api"}    # one series per provider after its first run
```

`stalwart_journal_*` appear only after the first matching event; `stalwart_journal_remote_rejections` usually shows up within a day on a host that sends mail, the others may never appear on a healthy host. If a name differs (e.g. the exporter is configured without suffixes), fix the rules in the repo, not in the rendered copy.

## Rendering

```bash
# PrometheusRule CR on stdout
BRYGGE_ALERTS_LABELS="release=kube-prometheus-stack" \
  just alerts-render <domain> <slug> [runbook_base]

# Plain rule groups (what promtool reads)
scripts/render-brygge-alerts.sh --groups <domain> <slug>
```

- `<domain>` is the club's `service.namespace` (its domain); `<slug>` its short name (`CLUB_SLUG`).
- `runbook_base` defaults to this repo's `alerts-runbook.md` on GitHub; each rule appends its section anchor.
- `BRYGGE_ALERTS_LABELS` (comma-separated `k=v`) sets `metadata.labels`; it must match the Prometheus `ruleSelector`. `BRYGGE_ALERTS_NAMESPACE` sets `metadata.namespace`.
- The CR is named `brygge-<slug>`; group names are `brygge-<slug>-host`, `brygge-<slug>-mail` and `brygge-<slug>-payments`, so several clubs' CRs coexist.

## Home-cluster operator recipe

Done once per Alertmanager, plus step B2 per club. Nothing in this repo deploys it. Stay in order: B1 confirms the data the rules need is there.

**B1 — verify jobs and names.** Run the queries in [Verify metric names](#verify-metric-names-in-prometheus) for the club. All three jobs present (Stalwart's after its first event; no rule fires on the Stalwart job's absence, so loading before it appears is safe), and a `tlscheck_time_left_seconds` series for each of the three targets, before loading rules; otherwise HostSilent, ApiSilent or TlsProbeFailing fire at once.

**B2 — render and load the rules.** Render into the home repo next to the existing alert files, with the same `metadata.labels` they carry:

```bash
BRYGGE_ALERTS_LABELS="<labels of the existing alert files>" \
BRYGGE_ALERTS_NAMESPACE="<namespace the rule lives in>" \
  scripts/render-brygge-alerts.sh <domain> <slug> > <home-repo>/…/alerts-brygge-<slug>.yaml
```

Run the script directly (it needs only bash and sed) or through `just alerts-render`; the dev shell prints its banner on stderr, so redirecting stdout from `nix develop --command` stays valid YAML. Re-rendering with the same variables reproduces the committed file, so keep any hand edits out of it.

Add the file to that directory's `kustomization.yaml`, commit, and let Flux apply it. Check it loaded: Prometheus UI → Alerts shows the `brygge-<slug>-*` groups. Re-render after every change to the rules file in this repo.

**B3 — ntfy secret.** Create an ntfy account and an access token, and three unguessable topics (page, warn, daily); subscribe to them in the ntfy app on the phone. Then a Secret `alertmanager-ntfy` in the Alertmanager's namespace (SOPS / sealed, like the cluster's other secrets) with keys:

| Key | Value |
|---|---|
| `token` | the ntfy access token |
| `url-page` | `https://ntfy.sh/<page-topic>?template=alertmanager&priority=urgent` |
| `url-warn` | `https://ntfy.sh/<warn-topic>?template=alertmanager&priority=default` |
| `url-daily` | `https://ntfy.sh/<daily-topic>?template=alertmanager&priority=low` |

Mount it with `alertmanager.alertmanagerSpec.secrets: [alertmanager-ntfy]` (kube-prometheus-stack), which puts the keys under `/etc/alertmanager/secrets/alertmanager-ntfy/`. `template=alertmanager` makes ntfy format Alertmanager's webhook JSON itself, so no bridge is needed.

**B4 — route and receivers.** Merge [alerts/alertmanager-ntfy.yaml](alerts/alertmanager-ntfy.yaml) into the Alertmanager config: its route goes **first** in `route.routes` (it matches `service_namespace=~".+"`, which only Brygge rules set), its receivers are appended to `receivers`. The fragment ships with `continue: true`, so the existing receiver (Home Assistant) keeps getting a copy — but only through a later sibling route: the root route's own receiver is just the fallback when no child matches. If the existing receiver is reached only as the root default, add a catch-all `- receiver: <existing>` as the last entry in `route.routes`. Set `continue: false` for ntfy only. `group_by: [alertname, service_namespace]` keeps clubs apart.

**B5 — egress.** From an Alertmanager pod: `kubectl exec -n <ns> <alertmanager pod> -c alertmanager -- wget -qO- --header "Authorization: Bearer <token>" https://ntfy.sh/<warn-topic>/json?poll=1` succeeds (network policies or an egress proxy may block `ntfy.sh`).

**B6 — test.**

1. Routing: with the merged config, `amtool config routes test --config.file=<file> 'alertname="HostSilent"' 'service_namespace="<domain>"' 'severity="page"'` prints `brygge-ntfy-page,<existing receiver>` (just `brygge-ntfy-page` with `continue: false`); same for `warn` and `daily`; an alert without `service_namespace` reaches only the existing receiver.
2. Delivery: `kubectl port-forward -n <ns> svc/<alertmanager> 9093`, then `amtool alert add --alertmanager.url=http://127.0.0.1:9093 alertname=BryggeTest service_namespace=<domain> club=<slug> severity=page --annotation=summary="Brygge test alert"`; it arrives on the phone within `group_wait` (30s), and the resolved notice follows after Alertmanager's `resolve_timeout` (5 minutes by default).
3. End to end: render the rules for a namespace that sends nothing (`hostsilent-test.invalid`, slug `hostsilent-test`), load them as in B2; HostSilent and ApiSilent for that namespace fire about two minutes after loading (no series at all, then `for: 2m`) and reach the phone; TlsProbeFailing stays quiet because the host itself is silent. Remove the file afterwards, together with its entry in the kustomization that lists it, in the same commit: a kustomization naming a missing file fails as a whole, taking every other alert file with it.

BRY-414 tracks this recipe for the home cluster and stays open until step B6 reaches the phone.

## Testing the rules

```bash
just alerts-check
```

Renders the rules for `example.invalid` / `example`, runs `promtool check rules`, and runs [alerts/brygge-alerts.test.yaml](alerts/brygge-alerts.test.yaml) with `promtool test rules`. `nix flake check` runs the same as the `alerts` check. The tests pin the behaviour the rules were designed for: HostSilent fires after 30 minutes plus `for` and carries the routing labels, and ApiSilent likewise after 15; TlsProbeFailing fires only for the target that stopped reporting and not while the host is silent; DiskFilling ignores tmpfs and `/nix/store` and waits 15 minutes; DkimSignerMissing fires within the hour and resolves after it; MailDeferredByProvider needs 4xx in both hours and ignores `none`/5xx; JournalRetentionShort stays quiet below the size gate and ignores user journals; DmarcDkimFailing fires on one DKIMFAIL-derived sample; EmailSendErrors needs three errors, not two; MailQueueStuck resolves when its gauge goes absent; MailQueueUnknown stays quiet before the first poll and without the queue monitor; PaymentWebhookFailing stays quiet for a multi-minute burst that heals, fires about 1h30 into hourly retries of a stuck event without resolve gaps (keep_firing_for), and resolves about 1h15 after the last failure, per provider and event type; PaymentAttemptStuckAuthorized fires past an hour and resolves when the gauge returns to 0 or goes absent. SesDeliveryFailed pages on one suppressed or complained-about message per kind, result and domain class, ignores accepted deliveries, and resolves after the hour; SesEventsMissing waits out its 15-minute grace, stays quiet when Send events arrive and ignores Stalwart sends; SesDlqNotEmpty waits 15 minutes.

When changing a rule, change the test first: a promtool test compares labels **and** annotations exactly, so an edited summary breaks its test case.

## Common misses

- **A rule matching on a series label for routing.** Only the static labels are reliable; `absent_over_time` and every `sum(…)` drop series labels.
- **`rate()` on a `stalwart_journal_*` metric.** They are gauges of per-batch counts; `rate` treats each batch as a counter reset.
- **An instant selector on a sparse metric.** `tlscheck_*` (10m) and `file_*` (5m) are missing from a plain instant query half the time; use `last_over_time`.
- **Changing the rules without re-rendering into the home repo.** The home cluster runs the rendered copy (B2).
- **Stalwart upgrades.** The journal-derived metrics depend on Stalwart 0.15.5's event strings, and the queue gauges on its admin API's query parsing; both are on the [upgrade checklist](../mail/stalwart-internals.md#upgrading-stalwart).
- **Watching the watcher.** These rules can't report a dead Prometheus or Alertmanager; a heartbeat to an external service is a separate follow-up.
