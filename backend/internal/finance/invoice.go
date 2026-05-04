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
	Description string
	Quantity    int
	UnitPrice   float64
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
	pdf.AddPage()
	tr := pdf.UnicodeTranslatorFromDescriptor("")

	// Header
	pdf.SetFont("Helvetica", "B", 20)
	pdf.Cell(0, 12, tr("Faktura"))
	pdf.Ln(16)

	// Seller info (left)
	pdf.SetFont("Helvetica", "", 10)
	pdf.Cell(95, 5, tr(inv.ClubName))
	// Buyer info (right)
	pdf.Cell(95, 5, tr(inv.MemberName))
	pdf.Ln(5)

	pdf.Cell(95, 5, tr(fmt.Sprintf("Org.nr: %s", inv.OrgNumber)))
	pdf.Cell(95, 5, tr(inv.MemberAddress))
	pdf.Ln(5)

	pdf.Cell(95, 5, tr(inv.ClubAddress))
	pdf.Ln(12)

	// Invoice meta
	pdf.SetFont("Helvetica", "", 9)
	metaY := pdf.GetY()
	pdf.SetXY(10, metaY)
	pdf.CellFormat(45, 6, tr("Fakturanummer:"), "B", 0, "", false, 0, "")
	pdf.CellFormat(45, 6, fmt.Sprintf("%d", inv.InvoiceNumber), "B", 0, "", false, 0, "")
	pdf.Ln(6)
	pdf.CellFormat(45, 6, tr("Fakturadato:"), "", 0, "", false, 0, "")
	pdf.CellFormat(45, 6, inv.IssueDate.Format("02.01.2006"), "", 0, "", false, 0, "")
	pdf.Ln(6)
	pdf.CellFormat(45, 6, tr("Forfallsdato:"), "", 0, "", false, 0, "")
	pdf.CellFormat(45, 6, inv.DueDate.Format("02.01.2006"), "", 0, "", false, 0, "")
	pdf.Ln(6)
	pdf.CellFormat(45, 6, "KID:", "", 0, "", false, 0, "")
	pdf.CellFormat(45, 6, inv.KID, "", 0, "", false, 0, "")
	pdf.Ln(6)
	pdf.CellFormat(45, 6, "Kontonummer:", "", 0, "", false, 0, "")
	pdf.CellFormat(45, 6, tr(inv.BankAccount), "", 0, "", false, 0, "")
	pdf.Ln(12)

	// Line items table header
	pdf.SetFont("Helvetica", "B", 9)
	pdf.SetFillColor(240, 240, 240)
	pdf.CellFormat(95, 7, tr("Beskrivelse"), "1", 0, "", true, 0, "")
	pdf.CellFormat(20, 7, tr("Antall"), "1", 0, "C", true, 0, "")
	pdf.CellFormat(35, 7, tr("Enhetspris"), "1", 0, "R", true, 0, "")
	pdf.CellFormat(40, 7, "Sum", "1", 0, "R", true, 0, "")
	pdf.Ln(7)

	// Line items
	pdf.SetFont("Helvetica", "", 9)
	var total float64
	for _, line := range inv.Lines {
		lineTotal := float64(line.Quantity) * line.UnitPrice
		total += lineTotal
		// MultiCell-equivalent for long descriptions: render in a
		// fixed-width box and let fpdf wrap. For short, still works.
		x, y := pdf.GetXY()
		pdf.MultiCell(95, 7, tr(line.Description), "1", "", false)
		pdf.SetXY(x+95, y)
		pdf.CellFormat(20, 7, fmt.Sprintf("%d", line.Quantity), "1", 0, "C", false, 0, "")
		pdf.CellFormat(35, 7, tr(formatNOK(line.UnitPrice)), "1", 0, "R", false, 0, "")
		pdf.CellFormat(40, 7, tr(formatNOK(lineTotal)), "1", 0, "R", false, 0, "")
		pdf.Ln(7)
	}

	// Total row
	pdf.SetFont("Helvetica", "B", 10)
	pdf.CellFormat(150, 8, tr("Totalt"), "T", 0, "R", false, 0, "")
	pdf.CellFormat(40, 8, tr(formatNOK(total)), "T", 0, "R", false, 0, "")
	pdf.Ln(20)

	// Payment info
	pdf.SetFont("Helvetica", "", 9)
	pdf.Cell(0, 5, tr(fmt.Sprintf("Vennligst betal til kontonummer %s med KID %s innen %s.",
		inv.BankAccount, inv.KID, inv.DueDate.Format("02.01.2006"))))
	pdf.Ln(10)

	// Footer
	pdf.SetFont("Helvetica", "I", 8)
	pdf.SetTextColor(128, 128, 128)
	pdf.Cell(0, 5, tr(fmt.Sprintf("%s | Org.nr: %s", inv.ClubName, inv.OrgNumber)))

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("generating PDF: %w", err)
	}
	return buf.Bytes(), nil
}

func formatNOK(amount float64) string {
	return fmt.Sprintf("kr %.2f", amount)
}
