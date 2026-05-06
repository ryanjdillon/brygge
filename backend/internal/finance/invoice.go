package finance

import (
	"bytes"
	"fmt"
	"time"

	"github.com/go-pdf/fpdf"
)

// Invoice holds all data needed to generate a Norwegian faktura PDF.
type Invoice struct {
	// Seller
	ClubName    string
	OrgNumber   string
	ClubAddress string
	Website     string
	TreasurerEmail string
	// LogoData is raw PNG or JPEG bytes; LogoMIME is "image/png" or
	// "image/jpeg". Both empty = no logo.
	LogoData []byte
	LogoMIME string

	// Buyer
	MemberName    string
	MemberAddress string

	// Invoice details
	InvoiceNumber int
	IssueDate     time.Time
	DueDate       time.Time
	KID           string
	BankAccount   string

	// Line items
	Lines []InvoiceLine
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
func GeneratePDF(inv Invoice) ([]byte, error) {
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
	const pageW = 190.0 // A4 - 10mm margins on each side

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

	// Buyer block — top-right of page header
	buyerY := logoBottomY
	pdf.SetXY(110, buyerY)
	pdf.SetFont("Helvetica", "B", 11)
	pdf.CellFormat(90, 5, tr(inv.MemberName), "", 0, "R", false, 0, "")
	pdf.Ln(5)
	if inv.MemberAddress != "" {
		pdf.SetX(110)
		pdf.SetFont("Helvetica", "", 9)
		pdf.CellFormat(90, 4.5, tr(inv.MemberAddress), "", 0, "R", false, 0, "")
		pdf.Ln(4.5)
	}

	// Move below the taller of seller/buyer blocks
	pdf.Ln(8)
	if pdf.GetY() < buyerY+22 {
		pdf.SetY(buyerY + 22)
	}

	// "Faktura" title above the info box
	pdf.SetX(leftMargin)
	pdf.SetFont("Helvetica", "B", 18)
	pdf.Cell(0, 9, tr("Faktura"))
	pdf.Ln(11)

	// Invoice info box: yellow fill + border, hugs only the info rows
	// with an even inset on all sides matching the inside left margin.
	var total float64
	for _, line := range inv.Lines {
		total += float64(line.Quantity) * line.UnitPrice
	}
	const infoPad = 3.0
	const infoRowH = 6.0
	const infoLabelW = 38.0
	type row struct {
		k, v string
		bold bool
	}
	rs := []row{
		{tr("Fakturanummer:"), fmt.Sprintf("%d", inv.InvoiceNumber), false},
		{tr("Fakturadato:"), inv.IssueDate.Format("02.01.2006"), false},
		{tr("Kontonummer:"), tr(inv.BankAccount), true},
		{"KID:", inv.KID, true},
		{tr("Beløp å betale:"), tr(formatNOK(total)), true},
		{tr("Forfallsdato:"), inv.DueDate.Format("02.01.2006"), true},
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
	pdf.MultiCell(0, 5, tr(fmt.Sprintf(
		"Vennligst betal %s til kontonummer %s med KID %s innen %s.",
		formatNOK(total), inv.BankAccount, inv.KID, inv.DueDate.Format("02.01.2006"))), "", "L", false)
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
