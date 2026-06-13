# Stalwart 0.15 — protocol quirks reference

This is a focused reference for the Stalwart-specific behaviours Brygge's code depends on. **Read this when something at the JMAP/admin-REST layer fails unexpectedly**, not for first-time setup (see [setup.md](setup.md)) or feature design (see [inbox.md](inbox.md)).

Everything below was discovered by probing the running 0.15.x build on `mail.klokkarvikbaatlag.no`. None of it is in Stalwart's official docs at the time of writing — when you upgrade Stalwart, re-verify the same probes.

---

## Auth model

### Admin REST

Authenticate with HTTP Basic against the bootstrap admin (`/etc/stalwart/admin-password`) or any principal that holds the `admin` role. The base URL is `http://127.0.0.1:8088`. Useful endpoints:

- `GET /api/principal/<name>` — read a principal. Returns 200 with `{"data": {...}}` on hit, 200 with `{"data": null}` on miss (NOT 404). Brygge's `LookupPrincipal` handles both shapes.
- `POST /api/principal` — create.
- `PATCH /api/principal/<name>` — update via patch-action array: `[{"action": "set", "field": "secrets", "value": [...]}]`.
- `DELETE /api/principal/<name>` — delete.
- `GET /api/principal` — list all.

### JMAP

Path: **`/jmap/`** (trailing slash, no `/api` suffix). The standard discovery endpoint is `/jmap/session` and its `apiUrl` field points to `/jmap/`. Use HTTP Basic; bearer tokens are not supported on this build (see [§ no OAuth grant](#no-oauth-grant)).

A JMAP session is scoped to the authenticated principal: the `session.accounts` map only includes that principal's own account, plus any accounts that have granted the principal `shareWith` access. **Admin auth does not enumerate other accounts** — admin's session lists only the admin account.

This is the central constraint that drives Brygge's auth design: to act on `kasserar@`'s mailbox, you have to authenticate as `kasserar` (or as a principal that's been `shareWith`'d on `kasserar`'s mailboxes).

### No OAuth grant

Neither standard OAuth flow works for admin-as-other-principal token mint on this build:

```bash
# Both return: {"error":"invalid_grant"}
curl -isS -X POST -H 'Content-Type: application/x-www-form-urlencoded' \
  http://127.0.0.1:8088/auth/token \
  -d 'grant_type=password&username=<other>&password=<other-pw>'

curl -isS -u "admin:$ADMIN" -X POST \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  http://127.0.0.1:8088/auth/token \
  -d 'grant_type=urn:ietf:params:oauth:grant-type:token-exchange&subject_token=<other>&subject_token_type=urn:ietf:params:oauth:token-type:access_token'
```

Stalwart 0.15 may support other OAuth shapes, but neither password-grant nor token-exchange of the above forms returns a token. Practical consequence: Brygge stores a service password per shared principal at `/etc/stalwart/board-mailbox-passwords.json` (root:brygge 0640) and authenticates as the principal directly. Same pattern as the long-existing `relay@` account.

---

## Principal-name constraints

Stalwart validates principal names. **Empirically allowed**: lowercase alphanumeric. **Empirically rejected**: dashes (probe showed silent-but-failed POST).

Brygge's user-principal slug is `bu<12hex>` (e.g. `bu7d674b4b002c`) — alphanumeric, 14 chars, derived from the first 12 hex chars of the user's UUIDv4. Earlier versions used `bu-<12hex>` and the POST appeared to succeed but the principal never materialised because of the silent envelope error pattern (see § next section).

Other characters not verified — when in doubt, stick to `[a-z0-9]`.

---

## Error envelope inside 200 OK

The admin REST API sometimes returns `200 OK` with a JSON body of the shape:

```json
{"error": "notFound", "item": "example.invalid"}
```

This happens when the request was well-formed but Stalwart rejected the operation — e.g. POSTing a principal whose email isn't on a domain Stalwart serves. **Status-code-only checks miss this entirely.**

Brygge's `AdminClient.doJSON` parses the envelope:

```go
if len(raw) > 0 {
    var env struct {
        Error string `json:"error"`
        Item  string `json:"item"`
    }
    if json.Unmarshal(raw, &env) == nil && env.Error != "" {
        return fmt.Errorf("stalwart %s %s: %s (%s)", method, path, env.Error, env.Item)
    }
}
```

Any future direct admin-REST call must do the same. The naïve `if resp.StatusCode >= 400 { ... }` pattern is silently broken.

---

## Email domain validation

Stalwart only accepts a principal's `emails` array containing addresses on a domain it actually serves. POSTing a principal with `emails: ["foo@gmail.com"]` returns the error envelope `{"error":"notFound","item":"gmail.com"}`.

For Brygge user provisioning, the user's real address (whatever they typed at signup) lives in `users.email` and is used by Brygge's auth flow (magic links). The Stalwart principal carries a synthetic local address `<slug>@<club_domain>` — see `UserProvisioner.createPrincipal`. The user authenticates JMAP by `name` (slug) + password; their real email plays no role on the Stalwart side.

---

## Secret hashing on create vs PATCH

When you POST a principal with `secrets: ["plaintext"]`, Stalwart persists the value **verbatim** in RocksDB. No server-side hashing.

When you PATCH the same field on an existing principal with `[{"action":"set","field":"secrets","value":["plaintext"]}]`, Stalwart **does** hash server-side (to `$6$...$...` SHA-512-crypt format).

This is asymmetric and undocumented. Two principal-creation paths in the repo handled it differently before it was noticed:

- `stalwart-relay-account.service` pre-hashes with `mkpasswd -sm bcrypt` before its POST. Survived fine.
- `UserProvisioner` initially POSTed plaintext for principal creation, then PATCHed plaintext on rotation. The first-time-created principals had plaintext-in-RocksDB until the next rotation hashed it. DIL-322 fixed this — both `createPrincipal` and `setSecret` now bcrypt the password with `golang.org/x/crypto/bcrypt` before sending. Stalwart accepts the `$2a$10$…` bcrypt format alongside SHA-512-crypt.

---

## Folder auto-creation

When a principal is first created, Stalwart only auto-creates the **Inbox** folder. Drafts, Sent, Trash, etc. are created lazily as JMAP clients write to them — Bulwark and Thunderbird do this on first compose. A server-side flow like Brygge's send path has to create them itself via `Mailbox/set { create: { mboxid1: { name: "Drafts", role: "drafts" }}}` (`JMAPClient.CreateMailbox` does this idempotently — re-runs find the existing folder and skip).

This bit DIL-278's send path on first reply — the `Email/set` was rejecting with `invalidProperties` because the resolved `Drafts`/`Sent` IDs were empty.

---

## JMAP / RFC 8621 — body shape gotchas

### `Email/set` create body shape

Stalwart's enforcement of RFC 8621 §4.1 is strict. Things that tripped DIL-278:

1. **Custom headers go via property-name patches, not a `headers` array.**

   ```json
   // ❌ Wrong — `headers` is read-only on Email
   { "headers": [{"name": "X-Brygge-Actor", "value": "<uid>"}] }

   // ✅ Right — property-name patch syntax
   { "header:X-Brygge-Actor:asText": "<uid>" }
   ```

   The form suffixes (`asText`, `asRaw`, `asAddresses`, `asMessageIds`, `asDate`, `asURLs`) come from RFC 8621 §4.1.2.3. Use `asText` for arbitrary free-form custom headers.

2. **Reply-To is a proper property**, not a custom header.

   ```json
   { "replyTo": [{"email": "kasserar@…"}] }
   ```

3. **`charset` belongs on the BodyPart**, not the EmailBodyValue. AND **must not appear on inline parts** (those that reference `partId` + `bodyValues`). Stalwart's exact rejection:

   ```
   Email/set: invalidProperties: Cannot specify a character set
   when providing a "partId". [textBody/charset]
   ```

   The charset of inline bodies is implicit UTF-8 from the JSON string in `bodyValues`. `charset` is only valid on parts that reference a `blobId` (uploaded attachments).

   ```json
   // ❌ Wrong on both sides
   {
     "bodyValues": { "text": { "value": "...", "charset": "utf-8" } },
     "textBody":   [{ "partId": "text", "type": "text/plain", "charset": "utf-8" }]
   }

   // ✅ Right
   {
     "bodyValues": { "text": { "value": "..." } },
     "textBody":   [{ "partId": "text", "type": "text/plain" }]
   }
   ```

4. **Mailbox role values are lowercase strings**: `"inbox"`, `"drafts"`, `"sent"`, `"archive"`, `"trash"`, `"junk"`. The id strings themselves are alphabetic (see [§ ID encoding](#id-encoding)).

5. **The error envelope on `Email/set`** includes a `notCreated.<id>.properties` array listing exactly which fields the server rejected. Surface it in your error messages — otherwise every iteration costs a journald round-trip:

   ```go
   var emailSet struct {
       NotCreated map[string]struct {
           Type        string   `json:"type"`
           Description string   `json:"description"`
           Properties  []string `json:"properties"`
       } `json:"notCreated"`
   }
   ```

### `EmailSubmission/set` requirements

Both `emailId` and `identityId` are required (RFC 8621 §7.2). Missing identityId is the most common omission — the JMAP `Identity` object represents a "valid sender" for the account, and Stalwart auto-creates a default one per principal but you still have to look it up:

```go
identities, _ := jmap.ListIdentities(ctx, accountID)  // Identity/get with submission capability
// Pick the one whose email matches the From address, else the first available.
```

The `emailId` can use JMAP's creation-id back-reference (`"#tmp"`) when chained after an `Email/set { create: { tmp: ... } }` in the same request — Stalwart honours this correctly.

### `Mailbox/set` shareWith

Per RFC 8621 §2.5, rights are named **with** the `Items` suffix where applicable:

```json
{
  "shareWith": {
    "<jmap-account-id>": {
      "mayReadItems": true,
      "mayAddItems": true,
      "mayRemoveItems": true,
      "maySetSeen": true,
      "maySetKeywords": true,
      "mayCreateChild": false,
      "mayRename": false,
      "mayDelete": false,
      "maySubmit": false
    }
  }
}
```

`mayRead` (without `Items`) is invalid — DIL-276 caught this with the verbatim error `Invalid permission "mayRead"`.

`maySubmit` is per-mailbox right, NOT the same as account-level send delegation. There's no per-mailbox `send_as` in RFC 8621; the closest equivalent is to authenticate as the shared principal directly when sending. The spec's `send_as` flag is currently unenforced — it'll be re-examined for DIL-279.

---

## ID encoding

Stalwart uses **two different ID encodings** for principals depending on which surface you ask:

- **Admin REST** returns numeric IDs: `"id": 5`, `"id": 7`.
- **JMAP** returns short alphabetic IDs: `"f"`, `"i"`, `"m"`.

These are different representations of the same principal. `shareWith` and any JMAP method's `accountId` must use the alphabetic form. The admin REST `LookupPrincipal` returns the numeric form, which is useless for JMAP — Brygge resolves both via JMAP `Principal/get` (which returns alphabetic) and discards the admin numeric.

Same principle: `Mailbox.id` is alphabetic too (e.g. Inbox is `"a"` typically).

---

## Probes for verification

When something at the protocol layer fails, run these from the server to triangulate:

```bash
ADMIN=$(cat /etc/stalwart/admin-password)

# Discovery: what does the JMAP session look like for admin?
curl -fsS -u "admin:$ADMIN" http://127.0.0.1:8088/jmap/session | jq '{apiUrl, accounts: (.accounts | keys), primaryAccounts}'

# Probe a principal directly (replace <slug>)
curl -fsS -u "admin:$ADMIN" http://127.0.0.1:8088/api/principal/<slug> | jq

# Enumerate all principals via JMAP (admin can see them even though it can't act on them)
curl -fsS -u "admin:$ADMIN" -X POST -H 'Content-Type: application/json' \
  http://127.0.0.1:8088/jmap/ \
  -d '{"using":["urn:ietf:params:jmap:core","urn:ietf:params:jmap:principals"],
       "methodCalls":[["Principal/get",{"accountId":"<admin-account-id>","properties":["id","name","email","type"]},"0"]]}' \
  | jq '.methodResponses[0][1].list'

# Authenticate AS a shared principal and check its mailboxes
PW=$(sudo -u brygge jq -r '."kasserar@klokkarvikbaatlag.no"' /etc/stalwart/board-mailbox-passwords.json)
curl -fsS -u "kasserar:$PW" http://127.0.0.1:8088/jmap/session | jq '.accounts'
curl -fsS -u "kasserar:$PW" -X POST -H 'Content-Type: application/json' \
  http://127.0.0.1:8088/jmap/ \
  -d '{"using":["urn:ietf:params:jmap:core","urn:ietf:params:jmap:mail"],
       "methodCalls":[["Mailbox/get",{"accountId":"<kasserar-jmap-id>","properties":["id","name","role","shareWith"]},"0"]]}' \
  | jq
```

---

## Upgrading Stalwart

When bumping Stalwart in nixpkgs / the flake input:

1. Re-run the probes in [§ probes for verification](#probes-for-verification) and confirm responses haven't changed shape.
2. Check the error-envelope behaviour (§ error envelope) still applies — a future Stalwart might use proper status codes.
3. Re-test `Email/set` charset placement (§ Email/set § 3) — the strictness might loosen.
4. Re-test the OAuth grants (§ no OAuth grant) — if any grant starts returning tokens, replace the service-password approach with per-user token mint per the DIL-322 follow-up.
5. Verify principal-name validation hasn't changed (§ principal-name constraints) — if dashes become allowed, no code change needed but the next slug-encoder author should know.

The session-context comment on [DIL-275](https://linear.app/dillonteknisk/issue/DIL-275) captures the specific probes + their results at the time of writing. Use that as the regression baseline.
