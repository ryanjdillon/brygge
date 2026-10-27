# Harbor QR posters and cards

For the harbor master, treasurer and board. A QR target is a printed QR code that guests scan to pay with Vipps, for example for a guest berth, a motorhome spot or a shower. A scan opens a pay page; the payment then shows up under Harbor → Payments.

Manage targets under **Admin → Harbor → QR posters** (`/admin/harbor/payment-targets`).

## Creating a target

**New poster** asks for:

- **Label**: the title printed on the poster or card, e.g. "Gjesteplass".
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

**Poster** on a target's row downloads an A4 portrait PDF with the club name, label, location, a large QR code with the short address under it, the price and scan instructions.

### Cards: 2, 4 or 6 per A4 page

For several codes on one sheet, or one smaller card:

1. Tick the targets to print, or **Select all**. Archived targets can't be selected.
2. Choose **Cards per page**: 2, 4 or 6. The page is A4 landscape.
3. Click **Print selected**. To print a single card, use the printer button on its row instead.

A print page opens in a new tab. Each page has the club logo (when one is uploaded under Admin → Site content) and the club name at the top, with one line of scan-and-pay instructions beneath. Each card has a border, the label, the price, the QR code and the short address under it. You can switch between 2, 4 and 6 per page there before printing.

The card size depends only on cards per page. A page with fewer cards, such as a single card or the last page of a selection, keeps the cards at that size and centres them. So to print one card at the size it would have on a 6-up sheet, print it with **6 per page**.

The instruction line mentions choosing a quantity unless every card on the page has a fixed price. It is printed in the language you are using the app in.

### Printer settings

In the print dialog choose:

- **A4, landscape**;
- **scale 100%** (not "fit to page");
- **default margins**.

To keep a PDF, choose "Save as PDF" as the printer. Cards print best on matte paper; laminate them for outdoor use.

## Related

- [faktura.md](faktura.md): the price catalogue that sets each target's price.
