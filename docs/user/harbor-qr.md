# Harbor QR posters and cards

For the harbor master, treasurer and board. A QR target is a printed QR code that guests scan to pay, for example for a guest berth, a motorhome spot or a shower. A scan opens a pay page; the payment then shows up under Harbor → Payments.

On the pay page the guest chooses the quantity and pays online. When the club has both Vipps and card payment set up, the page offers both, with **Kort · Apple Pay · Google Pay** first because many guests are visitors without Vipps; with only one set up there is a single button for it.

Manage targets under **Admin → Harbor → QR posters** (`/admin/harbor/payment-targets`).

## Creating a target

**New poster** asks for:

- **Label**: the title printed on the poster or card, e.g. "Gjesteplass". Write it in the club's language (the default language under Admin → Settings).
- **Name in other languages**: optional, folded away under the label. One field per app language except the club's, each up to 80 characters. A card prints these under the label for the languages you pick when printing, and the pay page shows a guest the name in the language their phone or app is set to, with the club-language label under it. Leave a field blank to skip that language.
- **Location**: optional. It is printed on the full-page poster only, not on cards.
- **Type**: guest berth, motorhome or service.
- **Price item**: the price comes from this item in the price catalogue. Change the price there, not on the target.
- **Fixed price**: tick it for a one-off price, e.g. a shower. Otherwise the guest picks a quantity (nights, days) between **Min** and **Max**, and the price is per **Unit**.
- **Short address**: a short web address printed under the QR code, such as `klokkarvikbaatlag.no/q/gjest`, for a guest whose phone won't scan. Leave it blank and a random five-character one is made for you. You can choose your own instead: 2 to 24 lowercase letters, digits or hyphens, and unique within the club. Capital letters are accepted and turned into lowercase. The address is shown on the target's row, under the pay-page link.

Archiving a target stops its QR code from working; its payment history is kept. Archiving the price item it uses also stops it taking payments.

### Changing the short address

Edit the target and type a new **Short address**. The QR code does not change, so cards and posters already printed keep working when scanned. The short address printed on them under the code stops working, though: a guest who types it lands on a "not found" page. Reprint the cards after changing it. The editor reminds you of this next to the field.

A short address that belongs to an archived target, or to a target whose price item is archived, also shows "not found".

## Printing

There are two ways to print.

### One full-page poster

**Poster** on a target's row downloads an A4 portrait PDF with the club name, a large "Betal her" headline, the label, location, a large QR code with the short address under it, the price and a scan instruction. The poster is in Norwegian only; for other languages, print cards (below).

### Cards: 1, 2, 3, 4 or 6 per A4 page

For several codes on one sheet, or one smaller card:

1. Tick the targets to print, or **Select all**. Archived targets can't be selected.
2. Choose **Cards per page**: 1, 2, 3, 4 or 6. The page is A4 landscape; 1 per page is one large sign filling the page, and 3 per page is one row of tall cards.
3. Click **Print selected**. To print a single card, use the printer button on its row instead.

A print page opens in a new tab. Each page has, from the top:

- a small line with the club logo (when one is uploaded under Admin → Site content) and the club name;
- a headline across the page, "Betal her" in the club's language, followed by "· Pay here" when English is ticked. It is the largest text on the page: as large as fits across the page on one line. With 4 or 6 cards a page it is kept smaller, so the cards keep their height;
- a short scan instruction, one line per chosen language. It names no payment app and no quantity: guests choose those on the pay page.

Each card has a border, the label, the target's names in the chosen languages in a smaller line, the price, the QR code and the short address under it. You can switch between 1, 2, 3, 4 and 6 per page there before printing.

The card size depends on cards per page, and a little on the header (see below). A page with fewer cards, such as a single card or the last page of a selection, keeps the cards at that size and centres them. So to print one card at the size it would have on a 6-up sheet, print it with **6 per page**.

### Long names and many languages

A card's label, translations and price shrink together to make room, down to about 60% of their normal size, so the QR code keeps a size phones scan reliably: at least 35 mm on 4- and 6-up cards, and 60 mm on 1-, 2- and 3-up cards. Nothing is cut off. The card size also depends on the header, so ticking more languages makes the cards a little shorter and their text a little smaller. If the browser has a minimum font size set, small text can't shrink that far, so on a crowded card the QR code may end up a little smaller than intended without being flagged; check the preview, or print fewer per page.

If even the smallest text leaves too little room for the QR code, the card gets a red outline and the note "Too much text for this size", and the toolbar says how many cards have too much text. The outline and note show on screen only; they are not printed. To fix it, do one of:

- shorten the label or its names in other languages (edit the target);
- untick some languages;
- print fewer cards per page.

### Languages

The **Languages** checkboxes on the print page choose which languages the sheet is printed in. Norwegian (bokmål), English, German and Dutch are ticked to start with. The club's language is always ticked and always comes first, a little larger; the others follow in the order listed, smaller. The language you use the app in does not matter.

- The instructions get one line per ticked language.
- A card's second line lists the target's names in the ticked languages it has a name for. A language without a name for that target is left out; a target with no other names has no second line.
- With more than two languages ticked, the instruction lines go in two columns so the header stays short.
- Only the club language and English appear in the headline; a longer headline would no longer read from a distance.

The browser remembers your choice for next time, and the page address carries it (`?langs=nb,en,de,nl`), so a bookmarked or shared print link prints the same languages. In a private window the choice is not remembered, but the address still works.

### Style

The **Standard** and **Høy synlighet** (high visibility) buttons choose the look.

- **Standard** is for signs read up close indoors: a black headline between two rules, dark text.
- **High visibility** is for outdoor signs read from a distance: a yellow headline band, black text everywhere, heavier type, and thick black card borders. It is designed to read just as well from a black-and-white printer; the yellow prints as a light grey behind black text.

Like the languages, the browser remembers the style and the page address carries it (`?style=standard` or `?style=highvis`).

High visibility at 6 per page isn't supported: the larger text leaves too little room for the QR code, so the cards get flagged. Use 1 to 3 per page for outdoor signs.

### Which setup for which sign

| Sign | Style | Cards per page | Languages |
|------|-------|----------------|-----------|
| Guest berth, motorhome spot (outdoors) | High visibility | 1 or 2 | Norwegian, English, German, Dutch |
| Slipping (in the clubhouse) | Standard | any | Norwegian only |
| Fridge (small drinks and food) | Standard | 4 or 6 | Norwegian, English |

### Printer settings

In the print dialog choose:

- **A4, landscape**;
- **scale 100%** (not "fit to page");
- **default margins**.

To keep a PDF, choose "Save as PDF" as the printer. Cards print best on matte paper; laminate them for outdoor use.

## Related

- [faktura.md](faktura.md): the price catalogue that sets each target's price.
