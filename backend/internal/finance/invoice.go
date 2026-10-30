package finance

import (
	"bytes"
	"fmt"
	"math"
	"time"

	"github.com/go-pdf/fpdf"
)

// Invoice holds all data needed to generate a Norwegian faktura PDF.
type Invoice struct {
	// Seller
	ClubName       string
	OrgNumber      string
	ClubAddress    string
	Website        string
	TreasurerEmail string
	// LogoData is raw PNG or JPEG bytes; LogoMIME is "image/png" or
	// "image/jpeg". Both empty = no logo.
	LogoData []byte
	LogoMIME string

	// Buyer (private). When OrgRecipient is non-nil the PDF renders
	// the organisation block in place of the member's personal name +
	// address, but MemberName / MemberAddress are kept around so the
	// fallback case (private fakturas) still works without an extra
	// nil check on every render path.
	MemberName    string
	MemberAddress string

	// OrgRecipient, when set, addresses the faktura to an external
	// organisation (frivillig org or bedrift). The contact person
	// renders as "Att: <name>" under the org name; TheirRef shows up
	// in the yellow info box as "Deres ref." when present.
	OrgRecipient *OrgRecipient

	// Invoice details
	InvoiceNumber int
	IssueDate     time.Time
	DueDate       time.Time
	KID           string
	BankAccount   string

	// CreditNote renders the document as a Kreditnota: it asks nothing of the
	// recipient, so there is no account number, KID, due date or "amount to
	// pay", and its amount is shown as the credited amount. A credit note
	// shares the faktura number series, so InvoiceNumber is its number.
	// Callers set it for a document with a negative total; GeneratePDF also
	// treats any negative total as a credit note.
	CreditNote bool
	// CardRefund, for a CreditNote, says the amount is refunded to the card
	// the payer paid with. Set only for a credit note a card refund issued.
	CardRefund bool
	// Settled marks a faktura that is already paid when it is issued: the
	// replacement a failed card refund issues, settled by the money that came
	// back. It keeps the heading and the number and date rows but carries no
	// payment details (account number, KID, due date, "amount to pay") and
	// says "Betalt" instead, so the payer is not asked to pay it twice. It is
	// never inferred from a payment: an ordinary faktura that has been paid
	// still prints how to pay when a copy is sent. A credit note or a faktura
	// that nets to zero is rendered as such whether or not it is set.
	Settled bool

	// Line items
	Lines []InvoiceLine
}

type OrgRecipient struct {
	Name          string
	OrgNumber     string
	Address       string
	ContactPerson string
	TheirRef      string
}

type InvoiceLine struct {
	Description    string
	SubDescription string
	Quantity       int
	UnitPrice      float64
}

// GeneratePDF renders a Norwegian faktura as an A4 PDF and returns the bytes.
//
// The core PDF fonts (Helvetica/Arial) are encoded in WinAnsi (cp1252),
// not UTF-8 — passing UTF-8 bytes directly to Cell/CellFormat produces
// mojibake on Norwegian characters (e.g. "Båtlag" → "BÃ¥tlag"). We
// route every string through fpdf's WinAnsi translator so å/æ/ø/Å/Æ/Ø
// render correctly with the built-in fonts.
func GeneratePDF(inv Invoice) ([]byte, error) { //nolint:gocognit,cyclop // linear top-to-bottom PDF layout with optional sections; split tracked in BRY-424
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetAutoPageBreak(true, 20)
	tr := pdf.UnicodeTranslatorFromDescriptor("")

	// Footer pinned to the bottom of every page. Must be registered
	// before AddPage so it fires for page 1.
	pdf.SetFooterFunc(func() {
		pdf.SetY(-15)
		pdf.SetFont("Helvetica", "I", 8)
		pdf.SetTextColor(128, 128, 128)
		footer := inv.ClubName
		if inv.OrgNumber != "" {
			footer += fmt.Sprintf(" | Org.nr: %s", inv.OrgNumber)
		}
		if inv.ClubAddress != "" {
			footer += " | " + inv.ClubAddress
		}
		if inv.Website != "" {
			footer += " | " + inv.Website
		}
		pdf.CellFormat(0, 5, tr(footer), "", 0, "C", false, 0, "")
	})

	pdf.AddPage()

	const leftMargin = 10.0

	// Logo (top-left, optional)
	logoBottomY := pdf.GetY()
	if len(inv.LogoData) > 0 && inv.LogoMIME != "" {
		if err := embedLogo(pdf, inv.LogoData, inv.LogoMIME, leftMargin, logoBottomY, 22); err == nil {
			logoBottomY += 24
			pdf.SetXY(leftMargin, logoBottomY)
		}
	}

	// Seller block (left) — under logo if any
	pdf.SetXY(leftMargin, logoBottomY)
	pdf.SetFont("Helvetica", "B", 11)
	pdf.Cell(95, 5, tr(inv.ClubName))
	pdf.Ln(5)
	pdf.SetFont("Helvetica", "", 9)
	if inv.OrgNumber != "" {
		pdf.Cell(95, 4.5, tr(fmt.Sprintf("Org.nr: %s", inv.OrgNumber)))
		pdf.Ln(4.5)
	}
	if inv.ClubAddress != "" {
		pdf.Cell(95, 4.5, tr(inv.ClubAddress))
		pdf.Ln(4.5)
	}

	// Buyer block — top-right of page header. For organisation
	// fakturas the org name replaces the personal name and an
	// "Att: <contact>" line routes the invoice to the right desk
	// inside the recipient's accounting flow. Org number and address
	// follow if set.
	buyerY := logoBottomY
	pdf.SetXY(110, buyerY)
	if inv.OrgRecipient != nil {
		pdf.SetFont("Helvetica", "B", 11)
		pdf.CellFormat(90, 5, tr(inv.OrgRecipient.Name), "", 0, "R", false, 0, "")
		pdf.Ln(5)
		pdf.SetFont("Helvetica", "", 9)
		if inv.OrgRecipient.ContactPerson != "" {
			pdf.SetX(110)
			pdf.CellFormat(90, 4.5, tr("Att: "+inv.OrgRecipient.ContactPerson), "", 0, "R", false, 0, "")
			pdf.Ln(4.5)
		}
		if inv.OrgRecipient.OrgNumber != "" {
			pdf.SetX(110)
			pdf.CellFormat(90, 4.5, tr("Org.nr: "+inv.OrgRecipient.OrgNumber), "", 0, "R", false, 0, "")
			pdf.Ln(4.5)
		}
		if inv.OrgRecipient.Address != "" {
			pdf.SetX(110)
			pdf.CellFormat(90, 4.5, tr(inv.OrgRecipient.Address), "", 0, "R", false, 0, "")
			pdf.Ln(4.5)
		}
	} else {
		pdf.SetFont("Helvetica", "B", 11)
		pdf.CellFormat(90, 5, tr(inv.MemberName), "", 0, "R", false, 0, "")
		pdf.Ln(5)
		if inv.MemberAddress != "" {
			pdf.SetX(110)
			pdf.SetFont("Helvetica", "", 9)
			pdf.CellFormat(90, 4.5, tr(inv.MemberAddress), "", 0, "R", false, 0, "")
			pdf.Ln(4.5)
		}
	}

	// Move below the taller of seller/buyer blocks
	pdf.Ln(8)
	if pdf.GetY() < buyerY+22 {
		pdf.SetY(buyerY + 22)
	}

	var total float64
	for _, line := range inv.Lines {
		total += float64(line.Quantity) * line.UnitPrice
	}
	// A negative total is a credit note whether or not the caller said so, so
	// a caller that forgets the flag can never print "Vennligst betal kr -X".
	creditNote := inv.CreditNote || roundOre(total) < 0

	// "Faktura" (or "Kreditnota") title above the info box
	pdf.SetX(leftMargin)
	pdf.SetFont("Helvetica", "B", 18)
	if creditNote {
		pdf.Cell(0, 9, tr("Kreditnota"))
	} else {
		pdf.Cell(0, 9, tr("Faktura"))
	}
	pdf.Ln(11)

	// Invoice info box: yellow fill + border, hugs only the info rows
	// with an even inset on all sides matching the inside left margin.
	const infoPad = 3.0
	const infoRowH = 6.0
	const infoLabelW = 38.0
	type row struct {
		k, v string
		bold bool
	}
	numberLabel, dateLabel := "Fakturanummer:", "Fakturadato:"
	if creditNote {
		numberLabel, dateLabel = "Kreditnotanummer:", "Kreditnotadato:"
	}
	rs := []row{
		{tr(numberLabel), fmt.Sprintf("%d", inv.InvoiceNumber), false},
		{tr(dateLabel), inv.IssueDate.Format("02.01.2006"), false},
	}
	// "Deres ref." sits between the dates and the payment block when
	// the recipient supplied one — that's where Norwegian B2B
	// recipients expect to see their PO/internal reference.
	if inv.OrgRecipient != nil && inv.OrgRecipient.TheirRef != "" {
		rs = append(rs, row{tr("Deres ref.:"), tr(inv.OrgRecipient.TheirRef), false})
	}
	// A credit note, and a faktura that nets to zero (every line exempted),
	// ask for nothing, so they carry no payment details a member could act on
	// by mistake.
	switch {
	case creditNote:
		rs = append(rs, row{tr("Kreditbeløp:"), tr(formatNOK(math.Abs(total))), true})
	case roundOre(total) == 0:
		rs = append(rs, row{tr("Beløp å betale:"), tr(formatNOK(0)), true})
	case inv.Settled:
		rs = append(rs, row{"Status:", "Betalt", true})
	default:
		rs = append(rs,
			row{tr("Kontonummer:"), tr(inv.BankAccount), true},
			row{"KID:", inv.KID, true},
			row{tr("Beløp å betale:"), tr(formatNOK(total)), true},
			row{tr("Forfallsdato:"), inv.DueDate.Format("02.01.2006"), true},
		)
	}

	// Box width = label column + the widest value rendered + symmetric
	// padding on both sides. fpdf's GetStringWidth measures the current
	// font, so switch to bold first (the wider face) for the worst-case
	// measurement.
	pdf.SetFont("Helvetica", "B", 10)
	maxValW := 0.0
	for _, r := range rs {
		if w := pdf.GetStringWidth(r.v); w > maxValW {
			maxValW = w
		}
	}
	infoX := leftMargin
	infoY := pdf.GetY()
	infoW := infoPad*2 + infoLabelW + maxValW + 2 // tiny slack for cursor placement
	infoH := infoPad*2 + float64(len(rs))*infoRowH

	pdf.SetFillColor(255, 251, 220) // light yellow
	pdf.SetDrawColor(180, 170, 80)
	pdf.Rect(infoX, infoY, infoW, infoH, "FD")

	for i, r := range rs {
		pdf.SetXY(infoX+infoPad, infoY+infoPad+float64(i)*infoRowH)
		if r.bold {
			pdf.SetFont("Helvetica", "B", 10)
		} else {
			pdf.SetFont("Helvetica", "", 10)
		}
		pdf.CellFormat(infoLabelW, infoRowH, r.k, "", 0, "L", false, 0, "")
		pdf.CellFormat(infoW-2*infoPad-infoLabelW, infoRowH, r.v, "", 0, "L", false, 0, "")
	}

	pdf.SetXY(leftMargin, infoY+infoH+8)
	pdf.SetDrawColor(0, 0, 0)
	pdf.SetTextColor(0, 0, 0)

	// Line items table
	const cNum = 10.0
	const cDesc = 110.0
	const cQty = 15.0
	const cUnit = 27.5
	const cSum = 27.5

	pdf.SetFont("Helvetica", "B", 9)
	pdf.CellFormat(cNum, 7, "#", "", 0, "C", false, 0, "")
	pdf.CellFormat(cDesc, 7, tr("Beskrivelse"), "", 0, "L", false, 0, "")
	pdf.CellFormat(cQty, 7, tr("Antall"), "", 0, "C", false, 0, "")
	pdf.CellFormat(cUnit, 7, tr("Enhetspris"), "", 0, "R", false, 0, "")
	pdf.CellFormat(cSum, 7, "Sum", "", 0, "R", false, 0, "")
	pdf.Ln(7)

	pdf.SetFont("Helvetica", "", 9)
	for i, line := range inv.Lines {
		lineTotal := float64(line.Quantity) * line.UnitPrice
		// Compute row height: base + sub-line if present.
		mainH := 5.5
		subH := 0.0
		if line.SubDescription != "" {
			subH = 4.5
		}
		rowH := mainH + subH + 2.0

		x, y := pdf.GetXY()
		// Index cell
		pdf.CellFormat(cNum, rowH, fmt.Sprintf("%d", i+1), "1", 0, "C", false, 0, "")
		// Description cell with sub-line drawn manually
		descX := x + cNum
		pdf.Rect(descX, y, cDesc, rowH, "D")
		pdf.SetXY(descX+1.5, y+1)
		pdf.SetFont("Helvetica", "", 9)
		pdf.MultiCell(cDesc-3, mainH, tr(line.Description), "", "L", false)
		if line.SubDescription != "" {
			pdf.SetXY(descX+1.5, y+1+mainH)
			pdf.SetFont("Helvetica", "I", 8)
			pdf.SetTextColor(90, 90, 90)
			pdf.MultiCell(cDesc-3, subH, tr(line.SubDescription), "", "L", false)
			pdf.SetTextColor(0, 0, 0)
		}
		// Numeric cells, vertically centered.
		pdf.SetXY(descX+cDesc, y)
		pdf.SetFont("Helvetica", "", 9)
		pdf.CellFormat(cQty, rowH, fmt.Sprintf("%d", line.Quantity), "1", 0, "C", false, 0, "")
		pdf.CellFormat(cUnit, rowH, tr(formatNOK(line.UnitPrice)), "1", 0, "R", false, 0, "")
		pdf.CellFormat(cSum, rowH, tr(formatNOK(lineTotal)), "1", 0, "R", false, 0, "")
		pdf.Ln(rowH)
	}

	// Total row: borderless, no fill — only the line items themselves
	// are framed.
	pdf.SetFont("Helvetica", "B", 10)
	pdf.CellFormat(cNum+cDesc+cQty+cUnit, 8, tr("Totalt"), "", 0, "R", false, 0, "")
	pdf.CellFormat(cSum, 8, tr(formatNOK(total)), "", 0, "R", false, 0, "")
	pdf.Ln(14)

	// Payment instruction
	pdf.SetFont("Helvetica", "", 9)
	switch {
	case creditNote:
		pdf.MultiCell(0, 5, tr("Ingen betaling kreves."), "", "L", false)
		if inv.CardRefund {
			pdf.MultiCell(0, 5, tr("Beløpet refunderes til kortet som ble brukt ved betalingen."), "", "L", false)
		}
	case roundOre(total) == 0:
		pdf.MultiCell(0, 5, tr("Ingen betaling – fakturaen er fullt fritatt. Den er kun til orientering."), "", "L", false)
	case inv.Settled:
		pdf.MultiCell(0, 5, tr("Ingen betaling kreves. Fakturaen er allerede betalt."), "", "L", false)
	default:
		pdf.MultiCell(0, 5, tr(fmt.Sprintf(
			"Vennligst betal %s til kontonummer %s med KID %s innen %s.",
			formatNOK(total), inv.BankAccount, inv.KID, inv.DueDate.Format("02.01.2006"))), "", "L", false)
	}
	if inv.TreasurerEmail != "" {
		pdf.Ln(1)
		pdf.SetFont("Helvetica", "I", 9)
		pdf.SetTextColor(80, 80, 80)
		pdf.MultiCell(0, 5, tr(fmt.Sprintf(
			"Eventuelle spørsmål kan sendes til kasserer på %s.",
			inv.TreasurerEmail)), "", "L", false)
		pdf.SetTextColor(0, 0, 0)
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("generating PDF: %w", err)
	}
	return buf.Bytes(), nil
}

// roundOre rounds to whole øre so float noise from summing lines can't make
// a fully exempt faktura look like it owes 0.0000001 kr.
func roundOre(v float64) float64 {
	return math.Round(v*100) / 100
}

func formatNOK(amount float64) string {
	return fmt.Sprintf("kr %.2f", amount)
}

// embedLogo registers an in-memory image with fpdf. Silently fails
// (caller falls back to no logo) on any error so a corrupt upload never
// blocks invoice generation.
func embedLogo(pdf *fpdf.Fpdf, data []byte, mime string, x, y, maxH float64) error {
	var imgType string
	switch mime {
	case "image/png":
		imgType = "PNG"
	case "image/jpeg", "image/jpg":
		imgType = "JPEG"
	default:
		return fmt.Errorf("unsupported logo mime: %s", mime)
	}
	name := "logo-" + imgType
	pdf.RegisterImageOptionsReader(name, fpdf.ImageOptions{ImageType: imgType, ReadDpi: false}, bytes.NewReader(data))
	pdf.ImageOptions(name, x, y, 0, maxH, false, fpdf.ImageOptions{ImageType: imgType}, 0, "")
	return nil
}
