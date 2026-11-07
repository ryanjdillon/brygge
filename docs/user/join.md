# Bli medlem (self-service join)

> Audience: board members and site admins who open joining to the public and answer questions from people joining. Members joining follow the pages themselves.

Anyone can join the club from the website without a board member doing anything, as long as joining is open. The flow is three short pages and one email, and works on a phone.

UI names below are the Nynorsk labels (the club default), with the Bokmål label in brackets where it differs.

## When joining is open

The **Bli medlem** page (`/bli-medlem`, also `/join`) shows the form only when the club is ready to take joins:

- the club has a join price item set (Økonomi settings): an active, flat price item of the club's own; and
- the statutes, harbour rules, privacy policy and terms of sale are all published (GDPR → legal documents).

Until then the page says **Innmelding er ikkje opna enno** (*Innmelding er ikke åpnet ennå*) and links to the contact page. Nothing a visitor types is stored.

### Opening joining (Innmeldingspris)

1. **The joining price.** In **Økonomi-innstillingar**, under **Innmeldingspris**, choose the price item a new member pays (treasurer or admin, with a fresh two-factor code). Only active price items with a fixed amount can be chosen; items in the **Medlemskap** category are listed first. Create the item under **Prisar** first if it does not exist. Choosing **Ingen (innmelding stengd)** closes joining.
2. **The legal documents.** Personvernerklæring, Vedtekter, Hamnereglement and Salsvilkår must each have a published version, under **GDPR-administrasjon** → **Juridiske dokument**. Only a site admin can publish them; a treasurer sees a note asking an administrator to do it.

Below the selector Brygge shows whether joining is open. When it is closed, it lists what is missing (no price chosen, the price archived or not a fixed amount, or a document not published) with a link to **Prisar** and, for an admin, to the legal documents. Archiving the chosen price item, or deleting it, closes joining until you choose another.

The board address gets an email when someone joins and pays, and when a join payment is refunded or held back; each email opens the person in **Medlemmer**. Applicants and the members who got their membership this year have their own filters there; see [members.md](members.md#applicants-and-new-members).

## What the person joining does

1. **Fill in the form.** Name, email, phone and home address are required. They tick one box accepting the statutes and harbour rules, the privacy policy and the terms of sale. Each document opens in a new tab (`/vilkar/...`). The newsletter box is optional and starts unticked. The page shows the membership fee for the current year. See [Home address and residency](#home-address-and-residency) for the address.
2. **Check their email.** The page always says **Sjekk e-posten din**, whatever the address: it never tells a visitor whether an address is already known. The email holds a confirmation link that works for 24 hours.
3. **Confirm.** The link opens **Hei {namn}, stadfest innmeldinga** (*bekreft innmeldingen*) with one button. Opening the link changes nothing (so mail scanners cannot use it up); pressing the button creates their account as an applicant, records their consents, signs them in on that device and takes them to the payment page.
4. **Pay online.** Membership starts as soon as the online payment is through. When they pay on the device where they confirmed (so they are signed in), the receipt page says **Velkomen som medlem!** (*Velkommen som medlem!*) and links to the member portal; paid while signed out, it shows the plain receipt.

If they leave without paying, signing in later takes them to **Fullfør innmeldinga** (*Fullfør innmeldingen*, `/bli-medlem/fullfor`). Until they have paid, an applicant can open only that page, their profile, their fakturas and the privacy pages (**Personvern og data** and the privacy policy).

## Home address and residency

The form no longer asks whether the person lives in the municipality. It is worked out from the address instead.

- **Searching.** Under **Heimeadresse** (*Hjemmeadresse*) they start typing the street and number; matching addresses from Kartverket's address register appear in a list, and small typos are forgiven. Picking one shows the address with an **Endre** button to search again. The register is credited under the field (**Adressedata: © Kartverket**).
- **Residency.** For a picked address, Brygge looks it up in the register again when the form is sent, stores the register's spelling of the address, and records the member as living in the municipality when the address is in one of the club's municipalities (the municipality codes in the club settings). The board can still change **Lokal** on the member afterwards.
- **Typing it by hand.** **Finn du ikkje adressa? Skriv ho inn manuelt** (*Finner du ikke adressen? Skriv den inn manuelt*) shows the street, postal code and town fields, all required. If the address search is down, the form switches to these fields by itself with a short notice. Joining never waits for the register.
- **Unverified addresses.** An address typed by hand, or one the register could not confirm when the form was sent, is saved as **not verified**, and the member is not counted as living in the municipality until the board says so. A verified address shows that the address exists; it does not prove the person lives there.
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

An unpaid join ends by itself after 60 days.

A member who later wants to leave follows [membership.md](membership.md).
