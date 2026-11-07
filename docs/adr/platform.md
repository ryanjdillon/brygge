# Platform decisions

Cross-cutting product rules. See [README.md](README.md) for the format.

### PLAT-1: Every feature is configurable per club, with no defaults copied from one club

- **Status:** accepted · 2026-10-16 · BRY-351
- **Decision:** Brygge is built to be deployed by any club. A choice that differs between clubs is per-club configuration that starts empty (or at a neutral default), never a seeded value copied from the reference club's practice. Where it's cheap, the configuration is made general (role × any price item, rather than "free slip for the board").
- **Consequences:** a fix that comes out of one club's data is split into a general feature and a separate operator step for that club.

### PLAT-2: Every view works on phones and tablets (ratchet)

- **Status:** accepted · 2026-10-28 · BRY-464
- **Decision:** every view that is added or changed must work at 375px, 768px and desktop widths: no horizontal page scroll, wide tables become cards on small screens, touch targets of at least 44px, single-column forms and full-screen dialogs on phones. A view that is touched is left no worse on mobile than it was. Remaining gaps are tracked on BRY-464.
- **Why:** board members do much of their work from a phone, at the harbour.
- **Consequences:** the detailed rule is in `AGENTS.md` under Code Conventions.

### PLAT-3: Login links name their account, and cross-site writes are refused by the standard-library guard

- **Status:** accepted · 2026-11-07 · BRY-660 (recommended by the security review, adopted)
- **Decision:** the login-link page shows the full address the link logs in as before it offers the login button, from a peek that never consumes the token; the join confirm page names the applicant's address the same way. Every unsafe request under `/api/v1` passes Go's standard-library `http.CrossOriginProtection`, applied globally, trusting only the configured frontend origin, with four exemptions: the Vipps and Stripe webhooks, the one-click unsubscribe POST and the bearer-token dev query.
- **Why:**
  - **Full address, not a mask.** A masked address (`m***@e***.test`) can be matched by a lookalike address an attacker registers and requests a link for, so it does not tell the victim whose account the link is for. The full address is unique in the club, and whoever holds the token already has it: the link was mailed to that inbox.
  - **Binding the link to the browser that requested it** (a cookie nonce set on request and checked on verify) was considered and deferred. It would stop a forwarded link outright, but it also breaks the common case of requesting a link on a computer and opening the mail on a phone. The account line covers the attack without that cost; the nonce can be revisited if forwarded-link abuse appears.
  - **The standard-library guard over a custom middleware.** `http.CrossOriginProtection` (Go 1.25.1+; 1.25.0 had CVE-2025-47910, where a bypass pattern could exempt more paths than it named, fixed in 1.25.1; the module builds with 1.26) implements the Sec-Fetch-Site and Origin rules that browsers are specified to send, is maintained with the language, and is small to configure (trusted origin, bypass patterns, deny handler). A hand-written check would be another thing to get subtly wrong.
  - **Global, with listed exemptions.** Guarding only the routes known to matter would leave every new route unguarded by default. Global protection makes an exemption the deliberate act: each exempt route authenticates by signature or token, not by cookie. Same-site requests (the webmail and matrix subdomains) are refused too, since they are other applications. JSON-only bodies with a single-origin CORS allow-list and the `SameSite=Lax` cookie stay in place as further layers against other sites; against a same-site sibling on a cookie-authenticated route the guard is the only layer, so such a route is never exempted.
- **Consequences:** a new route whose caller sends a browser-like `Origin` or `Sec-Fetch-Site` (another site's page, a mail client; a server that sends neither passes anyway) needs an entry in `crossOriginBypass` (`backend/cmd/api/crossorigin.go`) and its own authentication; see [add-route.md](../developer/checklists/add-route.md). Rules: [invariants.md](../developer/reference/invariants.md) "The login page shows the account before it offers the login, and the peek consumes nothing" and "Cross-site requests are refused in layers".
