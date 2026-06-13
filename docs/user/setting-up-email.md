# Setting up your club email in a mail app

> Audience: members and board members who want to read and send club mail (`navn@dittklubb.no` for a personal mailbox, or `kasserar@dittklubb.no` / `styre@dittklubb.no` for a shared role mailbox) using a mail app they already know — Apple Mail, Outlook, Gmail, Thunderbird, etc.

The club runs its own mail server (Stalwart), which speaks the two standard mail protocols every modern mail app supports:

- **IMAP** for reading mail (incoming)
- **SMTP** for sending mail (outgoing)

So in short: **almost any mail app on any device can connect.** The only exception is Gmail's web interface (gmail.com in a browser) and Outlook.com on the web — Google and Microsoft removed the option to add external IMAP accounts there. For those, use the [forwarding workaround](#forwarding-workaround-for-web-only-mailboxes).

## Connection settings — same for every client

Whatever app you use, these are the values you'll enter:

| Setting | Value |
|---|---|
| Incoming (IMAP) server | `mail.dittklubb.no` |
| Incoming port | **`993`** (SSL/TLS required) |
| Outgoing (SMTP) server | `mail.dittklubb.no` |
| Outgoing port | **`465`** (SSL/TLS required) |
| Username (both servers) | your full club address (e.g. `kari@dittklubb.no`) |
| Password (both servers) | your club mailbox password (the one you log in to webmail with) |
| Authentication | Normal password |

Replace `dittklubb.no` with your club's actual domain. The same domain is used for incoming and outgoing.

If your app asks for "STARTTLS" vs "SSL/TLS": always pick **SSL/TLS** for both servers.

## Pick your client

- [Apple Mail (iPhone / iPad)](#apple-mail-iphone--ipad)
- [Apple Mail (Mac)](#apple-mail-mac)
- [Outlook (desktop — Windows / Mac)](#outlook-desktop--windows--mac)
- [Outlook.com / Outlook on the web](#outlookcom--outlook-on-the-web)
- [Gmail (mobile app — iOS / Android)](#gmail-mobile-app)
- [Gmail web (gmail.com)](#gmail-web-gmailcom)
- [Mozilla Thunderbird](#mozilla-thunderbird)

---

## Apple Mail (iPhone / iPad)

1. **Settings → Mail → Accounts → Add Account**.
2. Pick **Other**.
3. **Add Mail Account**.
4. Name: your full name. Email: your club address. Password: your club mailbox password. Description: e.g. "Club mail".
5. Pick **IMAP** at the top.
6. **Incoming Mail Server**:
   - Host Name: `mail.dittklubb.no`
   - User Name: your full club address
   - Password: your club mailbox password
7. **Outgoing Mail Server**:
   - Host Name: `mail.dittklubb.no`
   - User Name: your full club address
   - Password: your club mailbox password
8. Tap **Next** — iOS verifies the connection. Tap **Save**.

If iOS asks about security ("cannot verify server identity"), tap **Continue** the first time; it caches the certificate after that.

## Apple Mail (Mac)

1. **Mail → Settings → Accounts → +** (the plus button bottom-left).
2. Pick **Other Mail Account**.
3. Name: your full name. Email: your club address. Password: your club mailbox password. Click **Sign In**.
4. Mail can't auto-detect — you'll see manual fields. Fill in:
   - Account Type: **IMAP**
   - Incoming Mail Server: `mail.dittklubb.no`
   - Outgoing Mail Server: `mail.dittklubb.no`
   - User Name: your full club address
   - Password: your club mailbox password
5. Click **Sign In**. If macOS complains it can't verify, that's normal once — accept the certificate.
6. Pick what apps to use (Mail at minimum), click **Done**.

After it appears in the sidebar, **Mail → Settings → Accounts → [your club account] → Server Settings**:

- Incoming Port: `993`, **TLS/SSL on**.
- Outgoing Port: `465`, **TLS/SSL on**, Authentication: **Password**.

## Outlook (desktop — Windows / Mac)

> Tested with Outlook for Microsoft 365. Older standalone versions follow the same flow.

1. **File → Add Account** (Windows) or **Outlook → Settings → Accounts → +** (Mac).
2. Enter your club address. Outlook may attempt auto-discovery and fail — that's expected; pick **Advanced options → Let me set up my account manually** and continue.
3. Pick **IMAP**.
4. **Incoming server**:
   - Server: `mail.dittklubb.no`
   - Port: `993`
   - Encryption method: **SSL/TLS**
5. **Outgoing server**:
   - Server: `mail.dittklubb.no`
   - Port: `465`
   - Encryption method: **SSL/TLS**
   - Require sign-in: **on** (username + password — same credentials as incoming).
6. Click **Next** → enter your club mailbox password → **Connect**.

If Outlook's auto-discovery loop won't let you reach the manual step, on Windows you can use **Control Panel → Mail (Microsoft Outlook) → Email Accounts → New**, which goes straight to the manual setup wizard.

## Outlook.com / Outlook on the web

Same caveat as Gmail web: **Outlook.com's web interface does not support adding an external IMAP account at all.** If you want club mail to land in your Outlook.com inbox, use the [forwarding workaround](#forwarding-workaround-for-web-only-mailboxes) below.

## Gmail (mobile app)

The Gmail mobile app (Android and iOS) **does** support adding an external IMAP account. It shows up as a separate inbox in the account switcher — it does **not** merge into your `@gmail.com` inbox.

### iPhone — Gmail iOS app

1. Open Gmail → tap your account avatar (top right) → **Add another account**.
2. Pick **Other (IMAP)**.
3. Enter your club address.
4. Tap **Next** → **Personal (IMAP)**.
5. Password: your club mailbox password.
6. **Incoming server**:
   - Username: your full club address
   - Server: `mail.dittklubb.no`
   - Port: `993`, Security: **SSL/TLS**
7. **Outgoing server**:
   - Username: your full club address
   - Server: `mail.dittklubb.no`
   - Port: `465`, Security: **SSL/TLS**
8. Finish.

### Android — Gmail Android app

1. Open Gmail → tap your account avatar (top right) → **Add another account**.
2. Pick **Other**.
3. Enter your club address → **Manual setup** → **Personal (IMAP)**.
4. Password: your club mailbox password.
5. **Incoming server**: `mail.dittklubb.no`, port `993`, security **SSL/TLS**.
6. **Outgoing server**: `mail.dittklubb.no`, port `465`, security **SSL/TLS**, Require sign-in: **on**.
7. Finish.

> Tip: on newer Gmail for Android, after the account is added, you can pick **Gmailify** in the account settings, which folds the external inbox into the main Gmail view. Not available on iOS.

## Gmail web (gmail.com)

Google removed the option to fetch external mail via IMAP from the web interface back in 2018. **Only POP3 is supported** as a "Check mail from other accounts" option, and the club's server does not expose POP3. So you have two choices:

1. Use the **Gmail mobile app** instead (see above) and accept that gmail.com in the browser won't show club mail.
2. Use the **forwarding workaround** below — it makes club mail appear in your normal Gmail inbox at gmail.com.

### Forwarding workaround (for web-only mailboxes)

This works for Gmail web and Outlook.com — any web-only mailbox that won't accept an IMAP connection. **Only do this for a personal club mailbox** (e.g. `kari@dittklubb.no`); for a shared role mailbox like `kasserar@`, see ["Recommended setup for board roles"](#recommended-setup-for-board-roles) below.

#### Step 1 — forward incoming mail

1. Log in to **webmail** at `https://webmail.dittklubb.no` with your club address.
2. Open **Settings** → **Filters** (or **Forwarding**).
3. Add a rule: **forward all incoming mail to your Gmail/Outlook address**.
4. Tick **keep a copy on the server** so you don't lose mail if the destination rejects a forward.
5. Save.

Send yourself a test mail to confirm it lands in your Gmail/Outlook inbox.

#### Step 2 — let your web mail app send "from" your club address

So replies don't show `<your-personal>@gmail.com` and confuse recipients:

**Gmail web:**

1. **Settings → Accounts and Import → Send mail as → Add another email address**.
2. Name: your full name. Email: your club address. **Untick** "Treat as an alias".
3. SMTP Server: `mail.dittklubb.no`, Port: `465`, Username: your full club address, Password: your club mailbox password, secure connection **SSL/TLS**.
4. Click **Add Account**. Gmail sends a verification email to your club address — because of Step 1 it forwards back. Open the email, click the link.
5. Optional: in the same settings, set **When replying to a message, reply from the address it was sent to**.

**Outlook.com web:**

1. **Settings → Mail → Sync email → Connected accounts → Other email accounts**.
2. Email address: your club address. Display name: your full name. Password: your club mailbox password.
3. Pick **Advanced options** → set incoming/outgoing server names and ports (`mail.dittklubb.no`, `993` and `465`, SSL on both).
4. Outlook.com's "Send mail as" flow is similar to Gmail's — once added you can pick the From address per message.

## Mozilla Thunderbird

Thunderbird auto-detects most settings, but our server uses non-standard naming for the user, so manual entry is faster:

1. **File → New → Existing Mail Account**.
2. Name: your full name. Email: your club address. Password: your club mailbox password.
3. Click **Configure manually**.
4. **Incoming**:
   - Protocol: **IMAP**
   - Server: `mail.dittklubb.no`
   - Port: `993`
   - SSL: **SSL/TLS**
   - Authentication: **Normal password**
   - Username: your full club address
5. **Outgoing**:
   - Server: `mail.dittklubb.no`
   - Port: `465`
   - SSL: **SSL/TLS**
   - Authentication: **Normal password**
   - Username: your full club address
6. Click **Re-test** → **Done**.

---

## Recommended setup for board roles

For users with a board role (`kasserar@`, `styre@`, etc.), the cleanest setup is:

- **Desktop**: read and reply in Brygge's shared inbox at **`/admin/inbox`**. It already shows the role mailbox to everyone with that role, tracks read/unread across the team, and audit-logs every send.
- **Mobile**: add the role mailbox as a regular IMAP account in Apple Mail or the Gmail mobile app (the instructions above work for shared mailboxes too) for read-only convenience. Replies sent from the mobile app go via the club's SMTP server using the role-mailbox credentials, so the `From:` shows the correct shared address.

This way the desktop view stays authoritative and the mobile app is the "quick glance" view. **Don't use the forwarding workaround for role mailboxes** — forwarding scatters copies into every board member's personal inbox and you lose the shared "who has answered this" view.

---

## Troubleshooting

- **"Couldn't connect to server"** — usually the port is wrong. Use `993` for incoming and `465` for outgoing, both with SSL/TLS. Do **not** use 143 or 587.
- **"Authentication failed" / "Username or password incorrect"** — your username must be the **full email address**, not just the part before `@`.
- **"Cannot verify server identity"** (iOS / macOS, first time only) — accept it once; the certificate is then cached.
- **App offers STARTTLS or "TLS (start)"** — don't pick that. Always pick **SSL/TLS** (also called "implicit TLS").
- **Gmail web "Check mail from other accounts" only offers POP3** — correct, this is by design from Google's side. Use the [forwarding workaround](#forwarding-workaround-for-web-only-mailboxes) or the [Gmail mobile app](#gmail-mobile-app).
- **Forwarded test email never arrives** — check that **forwarding is enabled** in webmail's Filters/Forwarding settings, and that the destination address is correct. Some providers (especially Outlook.com) silently spam-filter forwarded mail from new servers; check the spam folder.
- **Sent mail shows the wrong "From:"** — in your client's account settings, make sure the outgoing identity uses your club address and that the SMTP server (`mail.dittklubb.no`) is being used to send.
- **"Mail from this account is going to spam at the recipient"** — that's a deliverability issue at the receiving server, separate from setup here. Ask your treasurer/admin to check the delivery log in Brygge (Faktura → Sent → mail-search icon).

## Related

- [`developer/mail/setup.md`](../developer/mail/setup.md) — for administrators: how the mail server itself is set up (DNS, DKIM, SPF, DMARC).
- Brygge's shared inbox lives at `/admin/inbox` for any user with a board role.
