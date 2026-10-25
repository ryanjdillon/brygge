# Alerts runbook

One section per alert in [alerts/brygge-alerts.rules.yaml](alerts/brygge-alerts.rules.yaml). Each alert's `runbook_url` points at its section here. How the alerts are wired and routed: [alerting.md](alerting.md).

Every alert carries `service_namespace` (the club domain) and `club`. Commands below run on that club's host as root unless they say otherwise; `<domain>` is the club domain. Most mail checks use the Stalwart admin API:

```bash
PW=$(cat /etc/stalwart/admin-password); U=http://127.0.0.1:8088
```

| Alert | Severity | Signal |
|---|---|---|
| [HostSilent](#hostsilent) | page | No host metrics for 30 minutes |
| [MailDeferredByProvider](#maildeferredbyprovider) | page | A receiving server answered 4xx in each of the last two hours |
| [MailQueueStuck](#mailqueuestuck) | page | Oldest message in Stalwart's queue is older than 6 hours |
| [MailQueueUnknown](#mailqueueunknown) | warn | The API hasn't read the queue for 15 minutes |
| [DkimSignerMissing](#dkimsignermissing) | page | Stalwart sent mail without a DKIM signature |
| [DmarcDkimFailing](#dmarcdkimfailing) | page | A DMARC aggregate report shows DKIM failures |
| [EmailSendErrors](#emailsenderrors) | page | The API failed to hand 3+ messages to the relay in 15 minutes |
| [MailBounced](#mailbounced) | daily | Stalwart gave up on a message |
| [TlsCertExpiring](#tlscertexpiring) | warn | A served certificate expires within 14 days |
| [DiskFilling](#diskfilling) | warn | A filesystem is over 85 % full |
| [JournalRetentionShort](#journalretentionshort) | warn | The journal is near its size cap and keeps less than 7 days |

---

## HostSilent

**Meaning.** Prometheus has had no `system_memory_usage_bytes` sample from `job="<domain>/brygge-host"` for 30 minutes. Every signal from the host goes through the same collector and export path, so all of the club's other alerts are blind while this fires. Causes, most likely first: the export path is broken (tailnet, DNS, upstream token), the collector is down, the VM is down.

**First checks.**

1. Is the site up? `curl -sI https://<domain>/` from anywhere. If not, the VM or its network is down: Hetzner console.
2. On the host: `systemctl status opentelemetry-collector` and `journalctl -u opentelemetry-collector --since -1h | tail`.
3. Export health: `curl -s 127.0.0.1:8888/metrics | grep otelcol_exporter_` — `send_failed_*` rising means the upstream is unreachable or refusing.
4. Name resolution: `dig @127.0.0.1 <endpoint FQDN>`. `no children to pick from` in the collector log means the endpoint didn't resolve ([local-collector.md § Common misses](local-collector.md#common-misses)).
5. Tailnet: `tailscale status`; is the node logged in and the upstream reachable on 4317?
6. Home side: is the `otel-ingress` / gateway healthy, and did other sources stop too? If everything stopped, the problem is at home, not on the VM.

**Fixes.** Restart the collector (`systemctl restart opentelemetry-collector`); re-join the tailnet ([local-collector.md § Operating](local-collector.md#operating)); rotate the token in `/etc/otel/env` if the upstream rejects it. The collector's memory queue drops data after a 10-minute outage, so a gap upstream after recovery is expected.

## MailDeferredByProvider

**Meaning.** One receiving mail server (`server_address`) answered with a 4xx SMTP code in the last hour and in the hour before. Stalwart keeps retrying, so mail to that provider is delayed, and it bounces when the message's queue lifetime runs out. The usual cause at large providers is rate limiting from poor sender reputation, e.g. Gmail's `421 … unusual rate of unsolicited mail … from your IP Netblock`, made worse when mail fails DKIM.

The rule depends on Stalwart's retry schedule: a message is only rejected when Stalwart tries it. Retries start minutes apart and stretch out; once they are more than an hour apart, an hour without an attempt resolves the alert even though the message is still deferred. MailQueueStuck covers that case.

**First checks.**

1. The rejections and the provider's reply text: `journalctl -u stalwart --since -3h -o json | jq 'select(.MESSAGE == "Message rejected by remote server") | del(.__CURSOR, ._BOOT_ID, ._MACHINE_ID)'`; `HOSTNAME` and `CODE` are what the metric is keyed on.
2. What is queued, oldest first, with each recipient's status: `curl -fsS -u "admin:$PW" "$U/api/queue/messages?values=1&limit=20" | jq '.data'`.
3. Is DKIM passing? A failing signature turns a reputation problem into a block: see [DkimSignerMissing](#dkimsignermissing) and [DmarcDkimFailing](#dmarcdkimfailing).
4. Is the host's IP on a blocklist, and are PTR, SPF and DMARC intact? ([../mail/setup.md § Verifying deliverability](../mail/setup.md#verifying-deliverability)).

**Fixes.** Fix authentication first. For reputation throttling there is no switch: keep volume low, let Stalwart retry, and use the provider's postmaster tools or sender-support form (Google Postmaster Tools, Microsoft SNDS). Tell recipients at that provider their mail is delayed if it matters (invoices, magic links). A 4xx without a code (`smtp_code="none"`, a connection or TLS failure) is not counted here; look in the journal for those.

## MailQueueStuck

**Meaning.** The oldest message in Stalwart's outbound queue was created more than 6 hours ago. Something has been retrying for hours: a provider is deferring, a recipient domain doesn't resolve, or delivery itself is broken (port 25 egress, DNS).

**First checks.**

1. The oldest messages: `curl -fsS -u "admin:$PW" "$U/api/queue/messages?values=1&limit=5" | jq '.data.total, .data.items'` — the queue lists oldest first; look at each recipient's status and last error.
2. Is any delivery working? `journalctl -u stalwart --since -1h | grep -iE 'delivered|rejected|connection'`.
3. Port 25 egress and DNS from the host: `nc -vz <mx host> 25`, `dig MX <recipient domain>`.

**Fixes.** If one provider is deferring, see [MailDeferredByProvider](#maildeferredbyprovider). A message to a dead domain or mailbox can be cancelled in the web admin (Queue) or via the admin API once you've confirmed it will never deliver; the sender gets a DSN. The alert resolves once the oldest message is younger than 6 hours, and also when the API stops reporting the gauge — then [MailQueueUnknown](#mailqueueunknown) fires instead.

## MailQueueUnknown

**Meaning.** The API's last successful poll of Stalwart's queue (`brygge_mail_queue_last_success_seconds`) is more than 15 minutes old. After 15 minutes without a good poll the API stops reporting the queue gauges, so MailQueueStuck can't fire. The alert also fires when the API stops exporting metrics at all (crashed, not running): the last reported value keeps counting for up to a day. Clubs that don't configure the queue monitor never report the series and never get this alert.

**First checks.**

1. Is the API running? `systemctl status brygge`.
2. The poll's warning: `journalctl -u brygge --since -1h | grep -i 'queue'` — an auth error means the admin credential (`STALWART_ADMIN_PASSWORD` / `STALWART_ADMIN_TOKEN` in `/etc/brygge/env`) is wrong; a connection error means Stalwart's admin listener is down.
3. The same request by hand: `curl -fsS -u "admin:$PW" "$U/api/queue/messages?values=1&limit=1" | jq`. The request contract is in [../mail/stalwart-internals.md § Outbound queue listing](../mail/stalwart-internals.md#outbound-queue-listing).
4. Is Stalwart up? `systemctl status stalwart`.

**Fixes.** Fix the credential or restart the failing service. If the API is down, check its journal for the crash. If the request shape stopped working after a Stalwart upgrade, see the upgrade checklist in [stalwart-internals.md](../mail/stalwart-internals.md#upgrading-stalwart).

## DkimSignerMissing

**Meaning.** Stalwart logged `DKIM signer not found` in the last hour: it picked a signature id that doesn't exist and sent the message **unsigned**. Unsigned mail fails DMARC at providers that enforce it and erodes reputation (unsigned mail made an earlier Gmail throttling incident worse). The signing rule is `auth.dkim.sign` in `nix/host.nix`; with it unset, Stalwart 0.15 defaults to `rsa-<domain>` / `ed25519-<domain>`, which don't exist.

**First checks.**

1. Which rule Stalwart resolved, and the signature it names: the read-only commands in [../mail/setup.md § How signing is selected](../mail/setup.md#how-signing-is-selected).
2. `journalctl -u stalwart --since -2h | grep -iE 'signer not found|dkim'` — which sender domain? The rule only signs mail whose MAIL FROM domain is exactly the club domain, so a subdomain or foreign From goes out unsigned (and is not counted as missing).
3. Did a deploy or a web-admin edit change signing settings? The local TOML shadows the DB for the same key.

**Fixes.** Restore `auth.dkim.sign` (and the report/ARC signing keys next to it) in `nix/host.nix` and deploy. If the `mail` signature itself is gone from Stalwart, recreate it with the provisioning unit and check the public key still matches DNS ([../mail/setup.md § 4](../mail/setup.md#4-provision-dkim)).

## DmarcDkimFailing

**Meaning.** A DMARC aggregate report received in the last 6 hours (Stalwart parses reports sent to `postmaster@`) shows DKIM failures for mail claiming to be from the club domain. Either our own mail is failing DKIM (signing broken, key mismatch with DNS, a forwarder breaking signatures), or someone else is sending as the domain.

**First checks.**

1. The report: Stalwart web admin → Reports (parsed reports are kept 30 days), or `journalctl -u stalwart --since -7h -o json | jq 'select(.MESSAGE == "DMARC report received with warnings")'` for the reporter and counts.
2. Look at the source IPs with failures. Our host's IP → our signing is broken: go through [DkimSignerMissing](#dkimsignermissing), and check the published key: `dig +short TXT mail._domainkey.<domain>` against `stalwart-cli dkim get-public-key mail` ([../mail/setup.md § 4](../mail/setup.md#4-provision-dkim)). Unknown IPs → spoofing or a forwarder; DMARC policy already handles that and no fix is needed on the host.
3. Send a test message to a Gmail or Outlook mailbox and check "Show original" for `dkim=pass header.d=<domain> header.s=mail`.

**Fixes.** Fix signing or the DNS record. Reports cover a past day, so the alert keeps firing for 6 hours after the last bad report even when the fix is already in; check the next day's reports. If postmaster@ mail stops arriving at all, no reports are parsed and this alert can't fire: postmaster is a list with external members ([../mail/setup.md](../mail/setup.md)).

## EmailSendErrors

**Meaning.** About three or more app sends (`brygge_email_send_total{result="error"}`: magic links, invoices, invoice reminders) failed to reach the SMTP relay (Stalwart) in 15 minutes. These are hand-off failures — dial, auth, or the relay refusing — not remote bounces. Members are not getting login links or invoices. Broadcast mail goes through JMAP and is not counted.

The reminder worker retries a failed delivery up to three times, so a single failing reminder can reach the threshold by itself.

**First checks.**

1. The errors: `journalctl -u brygge --since -30min | grep -iE 'send|smtp'` — the error text is only in the journal, never in the metric.
2. Which kind: in Prometheus, `sum by (kind) (increase(brygge_email_send_total{job="<domain>/brygge-api", result="error"}[30m]))`.
3. Is Stalwart up and accepting submissions? `systemctl status stalwart`; `journalctl -u stalwart --since -30min | grep -i relay`.
4. Did the relay password change? `SMTP_PASSWORD` in `/etc/brygge/env` must match the `relay` principal ([../mail/setup.md § Rotating the brygge SMTP relay password](../mail/setup.md#rotating-the-brygge-smtp-relay-password)).

**Fixes.** Restart Stalwart if it's down; re-sync the relay password; for a single bad recipient (a reminder retrying), fix or remove the address. Re-send whatever failed once the cause is fixed; the journal lines say which sends those were.

## MailBounced

**Meaning.** A daily notice: Stalwart gave up on at least one message in the last 24 hours. `kind="dsn_permfail"` means it sent a permanent-failure notice (DSN) to the envelope sender; `kind="double_bounce"` means that notice itself couldn't be delivered and was discarded — app mail's bounce mailbox is broken, see [../mail/setup.md § Where bounces for app mail go](../mail/setup.md#where-bounces-for-app-mail-go).

**First checks.** The DSN lands in the bounce mailbox (the shared mailbox flagged `receives_bounces`, else postmaster@) and names the recipient and the remote server's answer. `journalctl -u stalwart --since -1d | grep -iE 'permanent failure|double bounce'` gives the times.

**Fixes.** Usually data: a mistyped or dead member address — correct it in the member record. Many bounces to one provider point at a block rather than bad addresses: see [MailDeferredByProvider](#maildeferredbyprovider). Double bounces need the bounce mailbox fixed.

## TlsCertExpiring

**Meaning.** The certificate served on `tlscheck_target` (`<domain>:443`, `mail.<domain>:443` or `mail.<domain>:465`) expires within 14 days. These are what clients see, not the files on disk: renewal can have succeeded while a service still serves the old certificate, because Caddy and Stalwart load `mail.<domain>`'s certificate separately and nothing reloads them after an ACME renewal unless the cert has `reloadServices`.

**First checks.**

1. What is served: `openssl s_client -connect mail.<domain>:465 -servername mail.<domain> </dev/null 2>/dev/null | openssl x509 -noout -enddate` (same for 443 and the site domain).
2. What is on disk: `openssl x509 -noout -enddate -in /var/lib/acme/mail.<domain>/cert.pem`, and `systemctl status acme-mail.<domain>.service` / its journal.
3. The site's own certificate comes from Caddy's ACME: `journalctl -u caddy --since -1d | grep -i -E 'certificate|acme'`.

**Fixes.** Disk newer than served → `systemctl restart stalwart` (and `systemctl reload caddy`), then re-check all three ports. Renewal failing → fix the ACME challenge (DNS, the webroot handler in Caddy, rate limits) and re-run `systemctl start acme-mail.<domain>.service`. After a fix the alert can linger for up to 20 minutes (the probe runs every 10).

## DiskFilling

**Meaning.** A real filesystem (`mountpoint`) has been over 85 % full for 15 minutes. On a full root filesystem Postgres, Stalwart and the journal stop writing.

**First checks.** `df -h`; `du -xh --max-depth=2 / 2>/dev/null | sort -h | tail -20`; `nix path-info -Sh /run/current-system`; `journalctl --disk-usage`; Postgres size (`sudo -u brygge psql "postgres:///brygge?host=/run/postgresql" -c "select pg_size_pretty(pg_database_size('brygge'))"`); Stalwart's data under `/var/lib/stalwart`.

**Fixes.** Old NixOS generations are the usual culprit: `nix-collect-garbage --delete-older-than 14d` and `/run/current-system/bin/switch-to-configuration boot` to prune boot entries (on a small `/boot`, this is the fix). Then old backups or dumps left in place, then mail storage. Resize the volume if growth is real.

## JournalRetentionShort

**Meaning.** The journal is within 85 % of its 500M cap (`SystemMaxUse`) and its oldest archived system file is less than 7 days old. Some unit is logging so much that history needed for investigating mail problems is being rotated away — the original mail incident lost its history this way, to the OTel collector's export-error stack traces. Retention is estimated from the file's mtime (when it was sealed), so it errs short.

**First checks.** `journalctl --disk-usage`; the noisiest units in the last hour: `journalctl --since -1h -o json | jq -r '._SYSTEMD_UNIT // .SYSLOG_IDENTIFIER' | sort | uniq -c | sort -rn | head`.

**Fixes.** Quiet the noisy unit (fix its error loop, lower its log level, or give it `LogRateLimitIntervalSec`/`LogRateLimitBurst` as the collector has — [local-collector.md § Self-telemetry and log volume](local-collector.md#self-telemetry-and-log-volume)). Raising `SystemMaxUse` is a last resort. Retention recovers only as the noisy files age out.
