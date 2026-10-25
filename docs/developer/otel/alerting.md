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
| `<domain>/brygge-api` | the API's `ServiceName` constant ([instrumentation.md](instrumentation.md)) | `brygge_email_send_total{kind,result}`, `brygge_mail_queue_oldest_age_seconds`, `brygge_mail_queue_last_success_seconds` |

Shapes the rules rely on:

- `stalwart_journal_*` are gauges holding per-batch event counts. Rules use `sum_over_time`, never `rate`/`increase`. A series exists only after its first event, so "no series" means "no events", not a failure. `smtp_code` is `none` for connection/TLS failures and `server_address` can be `unknown`; deferrals are matched with `smtp_code=~"4.."`.
- `tls_check` reports every 10 minutes and `file_stats` every 5, at or past Prometheus' 5-minute lookback, so their rules read `last_over_time(…[20m])` / `[15m]`. `tlscheck_*` series also carry the certificate's CN and issuer, which change on renewal: the rule takes `min by (tlscheck_target)`.
- The queue gauges exist only on deployments with the queue monitor configured ([instrumentation.md § Mail queue](instrumentation.md#mail-queue-stalwart)); `last_success` is 0 before the first poll, and the other two go absent after 15 minutes without a good poll.

Routing labels are **static on every rule**: `service_namespace`, `club` and `severity`. Series carry only `job`, and `absent_over_time` returns no series labels at all, so Alertmanager can't route on what the data carries. Severity is one of:

| `severity` | Use | ntfy priority (suggested) | Repeat |
|---|---|---|---|
| `page` | Members are or soon will be affected; act now | `urgent` | 4h |
| `warn` | Needs attention within days | `default` | 24h |
| `daily` | Digest-type notices (bounces) | `low` | 24h |

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
brygge_email_send_total{job="<domain>/brygge-api"}          # 8 series at 0 after start
brygge_mail_queue_last_success_seconds{job="<domain>/brygge-api"}
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
- The CR is named `brygge-<slug>`; group names are `brygge-<slug>-host` and `brygge-<slug>-mail`, so several clubs' CRs coexist.

## Home-cluster operator recipe

Done once per Alertmanager, plus step B2 per club. Nothing in this repo deploys it. Stay in order: B1 confirms the data the rules need is there.

**B1 — verify jobs and names.** Run the queries in [Verify metric names](#verify-metric-names-in-prometheus) for the club. All three jobs present (Stalwart's after its first event) before loading rules; otherwise HostSilent fires at once.

**B2 — render and load the rules.** Render into the home repo next to the existing alert files, with the same `metadata.labels` they carry:

```bash
BRYGGE_ALERTS_LABELS="<labels of the existing alert files>" \
  scripts/render-brygge-alerts.sh <domain> <slug> > <home-repo>/…/alerts-brygge-<slug>.yaml
```

Add the file to that directory's `kustomization.yaml`, commit, and let Flux apply it. Check it loaded: Prometheus UI → Alerts shows the `brygge-<slug>-*` groups. Re-render after every change to the rules file in this repo.

**B3 — ntfy secret.** Create an ntfy account and an access token, and three unguessable topics (page, warn, daily); subscribe to them in the ntfy app on the phone. Then a Secret `alertmanager-ntfy` in the Alertmanager's namespace (SOPS / sealed, like the cluster's other secrets) with keys:

| Key | Value |
|---|---|
| `token` | the ntfy access token |
| `url-page` | `https://ntfy.sh/<page-topic>?template=alertmanager&priority=urgent` |
| `url-warn` | `https://ntfy.sh/<warn-topic>?template=alertmanager&priority=default` |
| `url-daily` | `https://ntfy.sh/<daily-topic>?template=alertmanager&priority=low` |

Mount it with `alertmanager.alertmanagerSpec.secrets: [alertmanager-ntfy]` (kube-prometheus-stack), which puts the keys under `/etc/alertmanager/secrets/alertmanager-ntfy/`. `template=alertmanager` makes ntfy format Alertmanager's webhook JSON itself, so no bridge is needed.

**B4 — route and receivers.** Merge [alerts/alertmanager-ntfy.yaml](alerts/alertmanager-ntfy.yaml) into the Alertmanager config: its route goes **first** in `route.routes` (it matches `service_namespace=~".+"`, which only Brygge rules set), its receivers are appended to `receivers`. Set `continue: true` on the Brygge route to also send these alerts to the existing receiver (Home Assistant). `group_by: [alertname, service_namespace]` keeps clubs apart.

**B5 — egress.** From an Alertmanager pod: `kubectl exec -n <ns> <alertmanager pod> -c alertmanager -- wget -qO- --header "Authorization: Bearer <token>" https://ntfy.sh/<warn-topic>/json?poll=1` succeeds (network policies or an egress proxy may block `ntfy.sh`).

**B6 — test.**

1. Routing: with the merged config, `amtool config routes test --config.file=<file> 'service_namespace="<domain>"' 'severity="page"'` prints `brygge-ntfy-page`; same for `warn` and `daily`; an alert without `service_namespace` still reaches the existing receiver.
2. Delivery: `kubectl port-forward -n <ns> svc/<alertmanager> 9093`, then `amtool alert add --alertmanager.url=http://127.0.0.1:9093 alertname=BryggeTest service_namespace=<domain> club=<slug> severity=page --annotation=summary="Brygge test alert"`; it arrives on the phone within `group_wait` (30s), and the resolved notice follows after Alertmanager's `resolve_timeout` (5 minutes by default).
3. End to end: render the rules for a namespace that sends nothing (`hostsilent-test.invalid`, slug `hostsilent-test`), load them as in B2; HostSilent for that namespace fires on the first evaluation (no series at all) and reaches the phone. Remove the file afterwards.

BRY-414 tracks this recipe for the home cluster and stays open until step B6 reaches the phone.

## Testing the rules

```bash
just alerts-check
```

Renders the rules for `example.invalid` / `example`, runs `promtool check rules`, and runs [alerts/brygge-alerts.test.yaml](alerts/brygge-alerts.test.yaml) with `promtool test rules`. `nix flake check` runs the same as the `alerts` check. The tests pin the behaviour the rules were designed for: HostSilent fires after 30 minutes and carries the routing labels; MailDeferredByProvider needs 4xx in both hours and ignores `none`/5xx; JournalRetentionShort stays quiet below the size gate and ignores user journals; DmarcDkimFailing fires on one DKIMFAIL-derived sample; EmailSendErrors needs three errors, not two; MailQueueStuck resolves when its gauge goes absent; MailQueueUnknown stays quiet before the first poll and without the queue monitor.

When changing a rule, change the test first: a promtool test compares labels **and** annotations exactly, so an edited summary breaks its test case.

## Common misses

- **A rule matching on a series label for routing.** Only the static labels are reliable; `absent_over_time` and every `sum(…)` drop series labels.
- **`rate()` on a `stalwart_journal_*` metric.** They are gauges of per-batch counts; `rate` treats each batch as a counter reset.
- **An instant selector on a sparse metric.** `tlscheck_*` (10m) and `file_*` (5m) are missing from a plain instant query half the time; use `last_over_time`.
- **Changing the rules without re-rendering into the home repo.** The home cluster runs the rendered copy (B2).
- **Stalwart upgrades.** The journal-derived metrics depend on Stalwart 0.15.5's event strings, and the queue gauges on its admin API's query parsing; both are on the [upgrade checklist](../mail/stalwart-internals.md#upgrading-stalwart).
- **Watching the watcher.** These rules can't report a dead Prometheus or Alertmanager; a heartbeat to an external service is a separate follow-up.
