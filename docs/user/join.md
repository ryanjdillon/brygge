# Bli medlem (self-service join)

> Audience: people joining the club, and the board members and site admins who open joining and answer questions about it. The board's part is [Opening joining](#opening-joining-innmeldingspris), [What the board is told](#what-the-board-is-told) and [When a join is never paid](#when-a-join-is-never-paid).

Anyone can join the club from the website without a board member doing anything, as long as joining is open. The flow is three short pages and one email, and works on a phone.

UI names below are the Nynorsk labels (the club default), with the Bokmål label in brackets where it differs.

## When joining is open

The **Bli medlem** page (`/bli-medlem`, also `/join`) shows the form only when the club is ready to take joins:

- the club has a join price item set (Økonomi settings): an active, flat price item of the club's own; and
- the statutes, harbour rules, privacy policy and terms of sale are all published (GDPR → legal documents).

Until then the page says **Innmelding er ikkje opna enno** (*Innmelding er ikke åpnet ennå*) and links to the contact page. Nothing a visitor types is stored.

### Opening joining (Innmeldingspris)

1. **The joining price.** In **Økonomi-innstillingar**, under **Innmeldingspris**, choose the price item a new member pays (treasurer or admin, with a fresh two-factor code). Only active price items with a fixed amount can be chosen; items in the **Medlemskap** category are listed first. Create the item under **Prisar** first if it does not exist. Choosing **Ingen (innmelding stengd)** closes joining.
2. **The legal documents.** Personvernerklæring, Vedtekter, Hamnereglement and Salsvilkår must each have a published version, under **GDPR-administrasjon** → **Juridiske dokument**. Only a site admin can publish them; a treasurer sees a note asking an administrator to do it. See [Publishing the legal documents](#publishing-the-legal-documents).
3. **The club's municipalities.** Residency is worked out from the joiner's address and the club's municipality numbers (kommunenummer; see [Home address and residency](#home-address-and-residency)). Set them in **Økonomi-innstillingar**, under **Kommunar som gjeld som lokale**, below the joining price (treasurer or admin, with a fresh two-factor code when you save): type a four-digit number and press **Legg til**, remove one with its ×, then **Lagre**. Each number shows the municipality's name from Kartverket; a number Kartverket does not know is marked **ukjent kommunenummer** but can still be saved, and if Kartverket cannot be reached the number shows without a name. At most 20 numbers. Check each number against the municipality name the setting shows next to it, and update the list after a municipal merger or split. Joining is open without them, but then nobody who joins is counted as living in the municipality, and the join status says **Lokal tilhøyrsle kan ikkje avgjerast før kommunar er sette**.

Below the selector Brygge shows whether joining is open. When it is closed, it lists what is missing (no price chosen, the price archived or not a fixed amount, or a document not published) with a link to **Prisar** and, for an admin, to the legal documents. Archiving the chosen price item, or deleting it, closes joining until you choose another.

### Publishing the legal documents

In the admin menu under **Nettstad**, open **GDPR-administrasjon** (`/admin/gdpr`, site admins only). Under **Juridiske dokument**, press **Opprett dokument** and fill in:

- **Dokumenttype**: Personvernerklæring, Vedtekter, Hamnereglement or Salsvilkår.
- **Versjon**: any short label members can recognise, such as the date the annual meeting adopted it (`2024-03-22`) or the month it was written (`2026-11`). The list shows it as `v2024-03-22`.
- **Innhald** (*Innhold*): the document as **plain text**. Separate paragraphs with a blank line; line breaks are kept exactly as typed. Formatting such as bold, headings, lists or links is not interpreted, so write headings as their own line (for example `§ 3 Medlemskap`) and lists as lines starting with `-` or a number. Paste from a PDF with care: check that line breaks did not split words or swap the order of lines.
- Tick **Publiser med ein gong** (*Publiser umiddelbart*), then **Lagre**. A document saved without the tick is not shown anywhere, and there is no later publish button, so tick it.

A new version replaces the old one for readers as soon as it is published; consents members already gave stay recorded against the version they saw. Where each document is read:

| Document | Public page |
|---|---|
| Personvernerklæring | `/privacy` (the footer's **Personvern**), `/vilkar/privacy`, and **Personvernerklæring** in the member portal |
| Vedtekter | `/vilkar/statutes` |
| Hamnereglement | `/vilkar/harbour_rules` |
| Salsvilkår | `/salgsvilkar` (the footer's **Salsvilkår**, linked from Vipps) and `/vilkar/terms_of_sale` |

Until a document is published its page says it is not published yet (and if the page cannot reach the server, that it could not load the document), except the terms of sale: with no published Salsvilkår, `/salgsvilkar` shows the terms built into Brygge, filled in with the club's name, org.nr., phone and email, so the page Vipps checks always works. It names the treasurer's email (else the chair's, else post@ the club's domain) and the club phone for cancelling; with neither, it points to the contact page. Publishing your own Salsvilkår replaces that text in full.

## What the board is told

The board gets an email, never a task to approve. Each one names the person; open them in **Medlemmer** (applicants and this year's new members have their own filters there, see [members.md](members.md#applicants-and-new-members)).

| Email (Nynorsk subject) | When |
|---|---|
| **Nytt medlem: {namn}** | A join was paid and the person is now a member. |
| **Innmeldingsbetaling refundert: {namn}** | A join payment was refunded or its faktura credited. The person stays a member; deactivate them by hand if they should not. |
| **Innmelding må kontrollerast: {namn}** | Money came in for a join but membership was held back: the amount or the faktura does not match the join, the person was deactivated meanwhile, or a join that ran out after 60 days had part of the fee paid. Check the payment and settle it by hand. |

Cancellations have their own emails; see [membership.md](membership.md#for-the-board-following-up-a-cancellation).

**Where they go.** Under **Nettstadsinnstillingar**, **Varsel om medlemskap til** sets the address (treasurer or admin). The cancellation emails in [membership.md](membership.md#for-the-board-following-up-a-cancellation) (a cancellation, an undone cancellation, **Utmelding må handsamast**) go to the same address. Left empty, the emails go to the club's shared mailbox (post@), or, without one, to the secretary, else the chair. If none is set, the emails wait and are sent once an address exists.

A person the board has marked **Kan ikkje melde seg inn att** gets an email asking them to contact the board instead of a confirmation link; the board is not emailed. See [members.md](members.md#cant-rejoin).

## What the person joining does

1. **Fill in the form.** Name, email, phone and home address are required. They tick one box accepting the statutes and harbour rules, the privacy policy and the terms of sale. Each document opens in a new tab (`/vilkar/...`). The newsletter box is optional and starts unticked. The page shows the membership fee for the current year. See [Home address and residency](#home-address-and-residency) for the address.
2. **Check their email.** The page always says **Sjekk e-posten din**, whatever the address: it never tells a visitor whether an address is already known. The email holds a confirmation link that works for 24 hours.
3. **Confirm.** The link opens **Hei {namn}, stadfest innmeldinga** (*bekreft innmeldingen*) with one button. Opening the link changes nothing (so mail scanners cannot use it up); pressing the button creates their account as an applicant, records their consents, signs them in on that device and takes them to the payment page.
4. **Pay online.** Membership starts as soon as the online payment is through, and a welcome email follows. When they pay on the device where they confirmed (so they are signed in), the receipt page says **Velkomen som medlem!** (*Velkommen som medlem!*) and links to the member portal; paid while signed out, it shows the plain receipt.

If they leave without paying, signing in later takes them to **Fullfør innmeldinga** (*Fullfør innmeldingen*, `/bli-medlem/fullfor`). Until they have paid, an applicant can open only that page, their profile, their fakturas and the privacy pages (**Personvern og data** and the privacy policy).

## Home address and residency

The form no longer asks whether the person lives in the municipality. It is worked out from the address instead.

- **Searching.** Under **Heimeadresse** (*Hjemmeadresse*) they start typing the street and number; matching addresses from Kartverket's address register appear in a list, and small typos are forgiven. Picking one shows the address with an **Endre** button to search again. The register is credited under the field (**Adressedata: © Kartverket**).
- **Residency.** For a picked address, Brygge looks it up in the register again when the form is sent, stores the register's spelling of the address, and records the member as living in the municipality when the address is in one of the club's municipalities (**Kommunar som gjeld som lokale** in Økonomi-innstillingar). The board can still change **Lokal** on the member afterwards.
- **Typing it by hand.** **Finn du ikkje adressa? Skriv ho inn manuelt** (*Finner du ikke adressen? Skriv den inn manuelt*) shows the street, postal code and town fields, all required. If the address search is down, the form switches to these fields by itself with a short notice. Joining never waits for the register.
- **Unverified addresses.** An address typed by hand, or one the register could not confirm when the form was sent, is saved as **not verified**, and the member is not counted as living in the municipality until the board says so. A verified address shows that the address exists; it does not prove the person lives there.
- **For the board.** **Medlemmer** does not yet mark which addresses are unverified. A new member without **Lokal** may live in the municipality with an address that could not be checked: look at their address and tick **Lokal** on the member if it is in the municipality.
- **Phone.** Eight digits are taken as a Norwegian number; a number from another country needs its country code (for example +46).

## Fullfør innmeldinga

The page shows what is left:

- **Betal no** (*Betal nå*) opens the online payment page again.
- **Betal med bankoverføring** turns the join into a faktura with a KID. The page then shows the KID, the club's account number, the amount and the due date, each with a copy button, and the faktura is also emailed. Membership starts when the KID payment is matched, which can take a few days. **Betal på nett i staden** (*i stedet*) stays available and pays the same faktura.
- If an online payment was started and not finished, the bank option asks them to finish or cancel it first, so the fee cannot be paid twice.
- Once paid, the page says **Velkomen som medlem!** and links to the portal.
- If nothing is waiting for payment (just paid, or the join ended), the page asks them to refresh in a minute and otherwise contact the board.

## When the confirmation link does not work

The confirm page explains each case and offers the next step:

| What they see | Why | Next step offered |
|---------------|-----|-------------------|
| The link has expired | More than 24 hours passed | Join again |
| The link does not work | The link is incomplete or unknown | Join again |
| The link is already used | They already confirmed | Log in |
| You already have an account | The address belongs to an existing account | Log in |
| Contact the board | The board has blocked this person from rejoining online | Contact page |
| Joining is closed | Joining was closed after they submitted | Contact page |

## When a join is never paid

A join ends by itself 60 days after the last step taken on it (confirming, or choosing bank transfer). Nobody needs to do anything:

- **Nothing billed** (they never chose bank transfer): the person is deactivated, and their details are erased shortly after.
- **A bank faktura issued and unpaid:** the faktura is cancelled with a credit note ("Innmelding ikkje fullført"), which is emailed to them, and the person is deactivated. The faktura and credit note stay in the books; nothing is deleted.
- **Part of the fee paid:** nothing is ended. The board gets **Innmelding må kontrollerast** and decides.

A person whose join ended can simply join again.

A member who later wants to leave follows [membership.md](membership.md).
