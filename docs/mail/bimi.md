# BIMI — Brand logo in inboxes

[BIMI](https://bimigroup.org/) (Brand Indicators for Message Identification) lets a club's logo appear next to its outbound mail in supporting clients (Yahoo, Apple Mail, Fastmail). Gmail also supports BIMI but only renders the logo when the sender holds a paid Verified Mark Certificate — see [Gmail caveat](#gmail-caveat) at the end.

This guide walks through what's already wired up in the codebase, what an operator does once per club to publish the logo, and how to verify it.

---

## Prerequisites (already done by `tf-apply`)

These are the deliverability foundations BIMI rides on. Each is set up automatically when the standard deploy flow runs — listed here so you know what to look at if BIMI validation fails.

| Mechanism | Where it lives                                              |
|-----------|-------------------------------------------------------------|
| SPF       | `terraform/dns.nix` → `root_spf` (`v=spf1 mx -all`)         |
| DKIM      | `terraform/dns.nix` → `mail_dkim` at `mail._domainkey`      |
| DMARC     | `terraform/dns.nix` → `dmarc` (`p=quarantine` or stronger)  |
| PTR/rDNS  | `terraform/server.nix` → `hcloud_rdns.brygge_ipv4`          |

BIMI requires DMARC to be `p=quarantine` or `p=reject` with `pct=100` (the default when `pct` is omitted). Less-strict policies — `p=none` or `pct<100` — disqualify the domain regardless of how clean the SVG is.

---

## SVG requirements

BIMI's image format is a strict subset of SVG (`SVG Tiny 1.2 Portable/Secure`). A logo straight out of Inkscape or Figma will fail several checks. The hard rules:

- Root `<svg>` must declare `version="1.2"` and `baseProfile="tiny-ps"`.
- A `<title>` element directly inside `<svg>` (the brand name).
- Square `viewBox` (e.g. `0 0 512 512`).
- No `<script>`, `<style>`, animation, or external references.
- No raster images embedded as `<image>`.
- File size ≤ **32 KiB**.

Validate before publishing: <https://bimigroup.org/svg-validator/>.

### Optimization workflow

Most logos exported from a vector editor weigh 60–100 KiB. The pipeline that's known to land under 32 KiB:

```bash
# Working file outside the repo (the logo lives in the DB after upload —
# see "Upload" below — so it never needs to be tracked in git).
WORK=$HOME/club-logo-bimi.svg

# 1. Aggressive SVGO pass
nix develop --command npx --yes svgo@4 \
  --multipass --precision=1 -i "$WORK" -o "$WORK"

# 2. (Optional, situational) drop a hidden/dead path. SVGO sometimes
#    leaves a fill="none" sibling with no stroke — invisible but
#    1–2 KiB. grep for it and remove by hand:
grep -n 'fill="none"' "$WORK"

# 3. Manual edits in any text editor:
#    - On the root <svg>, add: version="1.2" baseProfile="tiny-ps"
#    - Insert as the first child:  <title>Klubbnavn</title>

# 4. Confirm size
wc -c "$WORK"   # < 32768

# 5. Re-run the validator at https://bimigroup.org/svg-validator/
```

If the file is still over 32 KiB after step 1:

- Strip `style="paint-order:..."` attributes (`sed -i 's/ style="[^"]*"//g' "$WORK"`) — visually identical for solid fills.
- Run another SVGO pass with `--precision=0`. This snaps anchor points to integer coordinates and is *very* aggressive (paths can become visibly jagged), so eyeball the result before committing to it.
- Ungroup transforms and apply them to coords — shaves a few hundred bytes if the source has many nested `<g transform="…">` wrappers.

---

## DNS

The BIMI record is published by `terraform/dns.nix`:

```nix
bimi = {
  zone    = "${var.domain}";
  name    = "default._bimi";
  type    = "TXT";
  ttl     = 300;
  records = [{ value = "\"v=BIMI1; l=https://${var.domain}/api/v1/club/logo.svg\""; }];
};
```

Two notes:

- The record **must** be at `default._bimi.<domain>`. The `default` selector is what receivers query unless the From: header carries an explicit `BIMI-Selector:` field (we don't set one).
- The `l=` URL **must end in `.svg`**. Many validators reject any other extension regardless of the response's Content-Type. The path `/api/v1/club/logo.svg` is an alias mounted in the backend (see [next section](#backend-routing)) over the same handler that serves the navbar's site logo.

---

## Backend routing

Two routes serve identical bytes for the **site logo** stored in the DB (set via *Admin → Accounting → Settings → Side-logo*):

| Path                          | Used by                                    |
|-------------------------------|--------------------------------------------|
| `/api/v1/club/logo`           | Frontend navbar (`<img>` in `NavBar.vue`)  |
| `/api/v1/club/logo.svg`       | BIMI indicator URL                         |

Both resolve to `clubSettingsHandler.HandleGetPublicClubLogo` in `backend/cmd/api/main.go`. The handler returns the raw SVG bytes with `Content-Type: image/svg+xml` and `Cache-Control: public, max-age=300` so receivers refresh within 5 minutes when the admin replaces the logo.

There is no separate file for the BIMI logo — the same SVG that admins see in their navbar **is** the BIMI indicator. Keep that in mind: a logo too "designed" for a navbar (gradients, fine detail) may exceed BIMI's 32 KiB and SVG-Tiny constraints. Aim for a flat, high-contrast mark that works at 32×32 pixels.

---

## Upload

After the SVG validates clean:

1. Sign in to `https://<domain>/admin/accounting/settings`.
2. Scroll to **Logoer → Side-logo**.
3. Click **Bytt logo** (or **Last opp logo** for first-time setup) and pick the optimized file.
4. The site logo storage column (`clubs.site_logo_data`, BYTEA) is updated atomically. Both the navbar and `/api/v1/club/logo.svg` start serving the new bytes immediately.

The 5-minute `Cache-Control` window is the longest you should have to wait for an external receiver to refetch.

---

## Deploy ordering

When changing both the BIMI URL pattern and the backend route — for example, the first-time switch from `/api/v1/club/logo` to `/api/v1/club/logo.svg` — push them in this order:

1. `nix run .#deploy` — backend ships first so the new URL is live.
2. `nix run .#tf-apply` — DNS record updated to the new URL.

Going DNS-first leaves a brief window where validators see the new BIMI record but get a 404 from the still-old backend.

---

## Verification

Three concentric checks, run in order:

1. **DNS lookup**

   ```
   https://mxtoolbox.com/SuperTool.aspx?action=bimi%3a<domain>
   ```

   Should report a single TXT record with the expected `v=BIMI1; l=…`. Look for "Logo retrieved" — that confirms the URL is reachable and returns SVG.

2. **SVG validator** at <https://bimigroup.org/svg-validator/>. All ten checks must show green ticks.

3. **End-to-end inbox test.** Send a real message from the club's outbound mailbox to a Yahoo or iCloud address. Yahoo's web UI displays BIMI logos within minutes once the record is valid; iCloud is similar.

   ```
   echo "Test" | mail -s "BIMI test" yourself@yahoo.com
   ```

   Or just reply to a thread. After the message lands, the logo should appear next to the sender name in the inbox list.

---

## Reputation lag

Even after every check passes, **the logo may not render immediately on a brand-new sending domain**. Receivers want to see consistent DMARC-aligned mail volume from the domain before they trust the BIMI association. For a club just standing up its mail server, this can take days to a few weeks. There's no workaround — keep sending legitimate mail and the logo eventually appears.

---

## Gmail caveat

Gmail's BIMI implementation gates inbox rendering on a **Verified Mark Certificate (VMC)**, issued by DigiCert or Entrust. A VMC requires a registered trademark plus annual renewal at roughly USD 1,500/year. Without one, Gmail will validate the BIMI record and quietly *not* display the logo — no error, no fallback.

For most small clubs the math doesn't work. The pragmatic stance is:

- Accept that Gmail recipients won't see the logo.
- The BIMI record is still worthwhile because Yahoo, Apple Mail, and Fastmail render without VMC, and any future shift in Gmail policy automatically picks up the existing record.

If the club later acquires a trademark and decides to pursue a VMC, the additional record format is:

```
v=BIMI1; l=https://<domain>/api/v1/club/logo.svg; a=https://<domain>/path/to/vmc.pem
```

The PEM file is hosted on the same server at any path you like; nothing else changes on the operator side.

---

## DKIM provisioning

Without a fixed-selector DKIM signature pinned in DNS, Stalwart can sign outbound mail with a selector that has no DNS counterpart. Gmail / Yahoo / Apple then see `dkim=permerror`, DMARC passes only via SPF (the weaker leg), and inbox placement plus BIMI eligibility both suffer.

The fix pins a single fixed signature with selector `mail`, whose public key is published at `mail._domainkey.<domain>` (wired in `terraform/dns.nix`).

Under **Stalwart 0.15.x** the 0.15 CLI has no way to import an externally-managed private key — `stalwart-cli dkim create` always generates and stores a fresh keypair internally. So the data flow inverts from a typical IaC pattern:

- **Stalwart owns the keypair.** `nix/host.nix` → `systemd.services.stalwart-dkim-config` runs after every boot and idempotently calls `stalwart-cli dkim create rsa <domain> mail mail`. The "already exists" failure on subsequent boots is swallowed.
- **DNS mirrors the public half.** The operator copies the generated public key out of Stalwart and pastes it into `tfvars.dkim_public_value`, which `terraform/dns.nix` publishes at `mail._domainkey.<domain>`.

### One-time operator setup per club

```bash
# 1. Deploy. The unit creates the DKIM signature in Stalwart on first
#    run. 1024-bit RSA by default (fits Hetzner DNS's 255-byte TXT
#    limit; 2048-bit would not).
nix run .#deploy

# 2. Pull the generated public key.
ssh root@mail.<domain> -- stalwart-cli dkim get-public-key mail
# → v=DKIM1; k=rsa; p=MIG...

# 3. Paste into tfvars.dkim_public_value, JSON-quoted:
#      "dkim_public_value": "\"v=DKIM1; k=rsa; p=...\""
$EDITOR terraform/terraform.tfvars.json

# 4. Publish.
nix run .#tf-apply
```

On any subsequent re-install / machine move, repeating step 1 is enough — the DB carries the signature with the host. Step 2 lets you confirm the public key matches what's in DNS.

### Verification after deploy

```bash
ssh root@mail.<domain> -- systemctl status stalwart-dkim-config
ssh root@mail.<domain> -- journalctl -u stalwart-dkim-config -n 50 --no-pager
```

First run: `stalwart-cli dkim create rsa ... mail mail` exits 0. Subsequent runs log `DKIM signature already present — nothing to do.`

Cross-check the keypair halves agree:

```bash
ssh root@mail.<domain> -- stalwart-cli dkim get-public-key mail
dig +short TXT mail._domainkey.<domain> @hydrogen.ns.hetzner.com
# The p= value in DNS must contain the bare base64 the CLI returned.
```

Send a test message and check the recipient's `Authentication-Results` header — both DKIM lines should read `dkim=pass header.s=mail`.

If the service fails on first run, the most likely causes (in rough order of likelihood):

- `/etc/stalwart/admin-password` missing → finish § 2 admin bootstrap in `setup.md`.
- `stalwart-cli` rejects the `--credentials admin:<pw>` syntax — check `stalwart-cli --help` for the version on the box and adjust the unit. The CLI's auth conventions have shifted between Stalwart 0.10 / 0.11 / 0.12 / 0.15 releases.
- Domain not yet created in Stalwart → log into the admin UI, add the domain, redeploy.

---

## Reference

- BIMI Group: <https://bimigroup.org/>
- DMARC walkthrough: [docs/mail/setup.md](setup.md)
- Backend route: `backend/cmd/api/main.go` — `r.Get("/club/logo.svg", …)`
- DNS record: `terraform/dns.nix` — `bimi`
- DKIM systemd unit: `nix/host.nix` — `systemd.services.stalwart-dkim-config`
- SVG storage column: migration `000032_split_club_logos`
