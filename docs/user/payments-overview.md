# The Payments page and its filters

For the harbor master, treasurer and board. **Admin → Economy → Payments** (`/admin/harbor/recent-payments`) lists what has been paid online within seconds, from the QR posters, the checkout and the pay link on a faktura: who paid, how much, with Vipps or card, and whether a payment was refunded or disputed. Posters are set up as described in [harbor-qr.md](harbor-qr.md).

## Reading the page

- The list shows the last 7 days, split into **I dag** and **Siste 7 dager**. Fill in **Fra** and **Til** to see a period instead (both days count; Fra = Til is one day). At most 200 payments are shown, the newest first; if there are more the page says so.
- Search finds a payer by name.
- Each payment has an icon and a name on its second line. That is the **filter group** the payment belongs to, and the chips above the list filter by those groups.
- The chips add up: press **Gjesteplass** and **Bobil** to see both. Press a pressed chip to take it off. **Alle** shows everything. The page remembers your choice on this device, and the address (`?kind=…`) can be shared.
- Treasurer and admin also see the refund button, refund and dispute chips and links to Stripe and Vipps.

## Filter groups

A filter group is a chip. It holds **prices** from the price catalogue, and a payment is shown under the group that holds the price it paid for. The groups are the club's own: you choose their names, icons, order and prices. **Filtre** at the top of the page opens the settings (**Admin → Economy → Payments → Filtre**); everyone who can see the Payments page can read them, and the harbor master, board, treasurer and admin can change them. For anyone else the page is read-only.

Two groups are always there:

- **Faktura**: payments of a faktura through its pay link.
- **Annet**: everything that is in no other group. It is always last and cannot be turned off or deleted. You can rename it and change its icon.

### What a club starts with

A club that already had guest berths, motorhome spots or prices called "slipping" got a group for each, holding those prices, so the chips look as before. A club that has none of them starts with just Faktura and Annet. Add the rest yourself.

### Making a group

1. **Ny gruppe**.
2. Give it a **name** (up to 40 characters). It is shown as typed, in every language of the app. Two groups cannot read the same.
3. Choose an **icon**. Each icon has its own colour.
4. Tick the **prices** that belong in the group. Search helps; **Alle** ticks every price shown that is in no group yet.
5. **Lagre**. The chip is there at once. A new group is placed just above **Faktura** (and below the groups that are already above it), so a faktura payment that also holds one of its prices is shown as the new group. Use the arrows to move it.

Example: to see slipping payments, make a group "Slipping", icon fork-lift, and tick the slipping prices. The chip then shows those payments, and nothing is changed in the code.

### Order matters

A price is in **one** group at a time. If you tick a price that is already in another group, it moves, and the page asks you first and lists what moves.

A payment can hold prices from several groups (a guest berth and a crane lift on one order, or a faktura paid with a slipping add-on). It is shown under the group that is **highest in the list**; **Faktura** counts as a group in that list too. Move groups up and down with the arrows. Annet is always last.

### Turning a group off, renaming, deleting

- The switch on a group hides its chip. Its prices stay in it, so turning it on again brings it back. Payments of a hidden group show under the next group that holds one of their prices, or under Annet.
- **Rediger** changes the name, icon and prices. A group the app made keeps a translated name until you type your own; **Bruk standardnavn** goes back to the translation.
- **Slett** removes a group. Its prices end up in no group.

Moving a price, hiding a group and deleting one apply to **all** payments, also the old ones: the group is decided when the page is shown, not when the payment is made.

### New prices

A new price is in no group, so its payments show under Annet until someone puts it in a group. The Payments page tells the people who can change the groups when sold prices are in no group, with a link to the settings, and the settings page lists them under **Ikke i noen gruppe**. Only active prices that have been sold count: a QR poster sells them, or a payment already holds them. A price nobody has sold is not listed, but you can still put it in a group in advance.

### Prices that cannot get a group

A payment of a faktura through its pay link is shown as **Faktura**, whatever the faktura bills; only the prices added to the payment itself (an add-on in the checkout, or a QR poster) can put it in another group. Prices that are only ever billed on a faktura, such as membership dues, harbor membership and the yearly slip fee, are therefore of no use in a group. The page works this out from how the price is sold: it can be an add-on in the checkout, a QR poster sells it, or a payment already holds it. A price that is none of these is marked "Bare faktura" in the list and **Alle** skips it. A membership price that is offered as an add-on is not marked.

## Related

- [harbor-qr.md](harbor-qr.md): QR posters and cards, which send guests to the pay page.
- [faktura.md](faktura.md): the price catalogue, fakturas, and card refunds and disputes.
