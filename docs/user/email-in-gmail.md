# Reading club mail in Gmail

> Audience: members and board members who already use Gmail and want to read or send mail from their club address (`navn@dittklubb.no` for a personal mailbox, or `kasserar@dittklubb.no` / `styre@dittklubb.no` for a shared role mailbox) without learning a second mail app.

The club runs its own mail server (Stalwart), which speaks the standard mail protocols **IMAP** and **SMTP**. Gmail can talk to those — but with two limits worth knowing up front:

1. **Gmail's web (gmail.com) interface does NOT fetch from external IMAP accounts.** Google removed that feature in 2018. Only POP3 is supported as a "Check mail from other accounts" option, and the club's server does not expose POP3.
2. **The Gmail mobile app DOES support adding external IMAP accounts** as separate inboxes. That's the route to use if you want to read club mail on your phone in the Gmail app.

So depending on what you want, pick one of the three options below.

## Three realistic options

| What you want | The setup | Works for shared mailboxes? |
|---|---|---|
| **Club mail shows up in your normal Gmail inbox** (gmail.com web + app, all together) | **Option A**: forward from Stalwart → your Gmail + Gmail "Send mail as" the club address | Not safely — forwarding strips shared-inbox context |
| **Club mail in the Gmail mobile app, side-by-side with your gmail.com inbox** | **Option B**: add the club address as a separate IMAP account in the Gmail mobile app | Yes |
| **Club mail in your normal Gmail inbox, but also keep the originals on the club server so the shared inbox in Brygge still works** | **Option C**: combine Option B (mobile) with Brygge's built-in shared-inbox at `/admin/inbox` for desktop | Yes (this is what we recommend for board roles) |

The rest of this doc walks through each option step-by-step.

---

## Option A — forward to Gmail + send-as (personal mailboxes)

Best for: a member who has a personal club address (e.g. `kari@dittklubb.no`) and wants everything in one Gmail inbox. **Do NOT use this for shared role mailboxes** — see "Why not for role mailboxes" below.

### Step 1 — forward incoming mail to Gmail

1. Log in to **webmail** at `https://webmail.dittklubb.no` with your club address.
2. Open **Settings** (gear icon) → **Filters** (or **Forwarding**, depending on the webmail version).
3. Add a rule: **forward all incoming mail to `<your-gmail-address>`**.
4. Tick **keep a copy on the server** so you don't lose mail if Gmail rejects a forward.
5. Save.

Send yourself a test mail to confirm it lands in your Gmail.

### Step 2 — let Gmail send "from" your club address

So replies don't show `<your-gmail>@gmail.com` and confuse recipients:

1. In Gmail (web), open **Settings** → **Accounts and Import** → **Send mail as** → **Add another email address**.
2. Name: your full name. Email address: `kari@dittklubb.no`. **Untick** "Treat as an alias" (use it as a separate identity).
3. SMTP Server: `mail.dittklubb.no`
4. Port: `465`
5. Username: your full club address (e.g. `kari@dittklubb.no`).
6. Password: your club mailbox password (the one you use to log in to webmail).
7. Secure connection: **SSL/TLS** (port 465 uses implicit TLS).
8. Click **Add Account**.
9. Gmail sends a verification mail to your club address. Because of Step 1, that mail forwards back to your Gmail. Open it, click the confirmation link.
10. Optional but recommended: in the same settings, set **When replying to a message, reply from the address it was sent to**.

You're done. Mail to `kari@dittklubb.no` arrives in your Gmail, and clicking Reply sends from the club address using the club server.

### Why not for role mailboxes

Forwarding a shared mailbox (`kasserar@`, `styre@`) means every board member ends up with a personal copy in their Gmail, and nobody can tell which messages are still "open" / "answered" / "needs follow-up" across the group. Brygge's built-in shared inbox at **`/admin/inbox`** already solves that — it shows the whole team the same view, with read/unread state shared. Use it instead.

---

## Option B — Gmail mobile app, separate IMAP account

Best for: reading any mailbox (personal or shared) on your phone, with the Gmail mobile app. Works on Android and iOS. The mailbox shows up as a separate inbox in the Gmail app's account switcher — it does **not** merge with your `@gmail.com` inbox.

### iPhone — Gmail iOS app

1. Open the Gmail app → tap your account avatar (top right) → **Add another account**.
2. Pick **Other (IMAP)**.
3. Enter your club address (`navn@dittklubb.no` or `kasserar@dittklubb.no`).
4. Tap **Next** → **Personal (IMAP)**.
5. Password: your club mailbox password.
6. **IMAP (incoming) server**:
   - Username: your full club address
   - Server: `mail.dittklubb.no`
   - Port: `993`
   - Security: `SSL/TLS`
7. **SMTP (outgoing) server**:
   - Username: your full club address
   - Server: `mail.dittklubb.no`
   - Port: `465`
   - Security: `SSL/TLS`
8. Finish the wizard. The Gmail app now shows your club address in the account switcher.

### Android — Gmail Android app

1. Open Gmail → tap your account avatar (top right) → **Add another account**.
2. Pick **Other**.
3. Enter your club address → **Manual setup** → **Personal (IMAP)**.
4. Password: your club mailbox password.
5. **Incoming server**:
   - Server: `mail.dittklubb.no`
   - Port: `993`
   - Security: `SSL/TLS`
6. **Outgoing server**:
   - Server: `mail.dittklubb.no`
   - Port: `465`
   - Security: `SSL/TLS`
   - Require sign-in: **on**
7. Finish. The mailbox appears in the Gmail Android app.

> Tip: on Android, after the account is added, you can sometimes pick **Gmailify** in the account settings, which folds the external IMAP inbox into your main Gmail view. Available on newer versions of the Gmail Android app only.

---

## Option C — recommended for board roles

For users with a board role (`kasserar@`, `styre@`, etc.), the cleanest setup is:

- **Desktop**: read and reply in Brygge's shared inbox at **`/admin/inbox`**. It already shows the role mailbox to everyone with that role, tracks read/unread across the team, and audit-logs every send.
- **Mobile**: use **Option B** to add the same role mailbox to the Gmail mobile app for read-only convenience. Replies sent from the Gmail app go via the club's SMTP server using your role-mailbox credentials, so they show the right `From:`.

This way the desktop view stays authoritative and the mobile app is the "quick glance" view.

---

## Settings cheat-sheet

| Setting | Value |
|---|---|
| IMAP server (incoming) | `mail.dittklubb.no` |
| IMAP port | `993` (SSL/TLS required) |
| SMTP server (outgoing) | `mail.dittklubb.no` |
| SMTP port | `465` (SSL/TLS required) |
| Username | your full club address |
| Password | your club mailbox password |

Replace `dittklubb.no` with your actual club's domain.

## Troubleshooting

- **"Couldn't connect"** — usually means the port number is off. Use `993` for incoming and `465` for outgoing, both with SSL/TLS.
- **"Authentication failed"** — your username must be the **full email address**, not just the part before `@`.
- **Gmail web "Check mail from other accounts" only lets me enter POP3** — correct. Gmail web does not support fetching via IMAP. Use Option A (forwarding) or Option B (mobile app) instead.
- **Verification email never arrives during "Send mail as" setup** — make sure forwarding (Step 1 of Option A) is active and pointing at the right Gmail address. The verification mail is sent **to** your club address, so it has to forward back.
- **Sent mail from Gmail shows the wrong "From:"** — in Gmail Settings → Accounts → Send mail as, set **Reply from the same address the message was sent to**.

## Related

- [`mail/setup.md`](../developer/mail/setup.md) — for administrators: how the mail server itself is set up (DNS, DKIM, SPF, DMARC).
- Brygge's shared inbox lives at `/admin/inbox` for any user with a board role.
