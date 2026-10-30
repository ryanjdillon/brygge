package accounting

import (
	"bytes"
	"fmt"
	"time"

	"github.com/go-pdf/fpdf"
)

// reportPDF is an A4 PDF whose text goes through the core font's translator, so
// Norwegian letters in labels, club names and account names draw as themselves
// and not as the bytes of their UTF-8 encoding.
type reportPDF struct {
	*fpdf.Fpdf
	tr func(string) string
}

func newReportPDF() *reportPDF {
	pdf := fpdf.New("P", "mm", "A4", "")
	return &reportPDF{Fpdf: pdf, tr: pdf.UnicodeTranslatorFromDescriptor("")}
}

func (p *reportPDF) Cell(w, h float64, txt string) { p.Fpdf.Cell(w, h, p.tr(txt)) }

func (p *reportPDF) CellFormat(w, h float64, txt, border string, ln int, align string, fill bool, link int, linkStr string) {
	p.Fpdf.CellFormat(w, h, p.tr(txt), border, ln, align, fill, link, linkStr)
}

// ReportHeader holds club info for PDF headers.
type ReportHeader struct {
	ClubName  string
	OrgNumber string
	Year      int
}

// IncomeStatementPDF renders the resultatregnskap as an A4 PDF.
func IncomeStatementPDF(header ReportHeader, stmt *IncomeStatement) ([]byte, error) {
	pdf := newReportPDF()
	pdf.SetAutoPageBreak(true, 20)
	pdf.AddPage()

	// Title
	pdf.SetFont("Helvetica", "B", 16)
	pdf.Cell(0, 10, "Resultatregnskap")
	pdf.Ln(12)

	// Header info
	pdf.SetFont("Helvetica", "", 9)
	pdf.Cell(95, 5, header.ClubName)
	pdf.Cell(95, 5, fmt.Sprintf("Regnskapsår %d", header.Year))
	pdf.Ln(5)
	if header.OrgNumber != "" {
		pdf.Cell(95, 5, fmt.Sprintf("Org.nr: %s", header.OrgNumber))
	}
	pdf.Ln(5)
	if stmt.ReferenceOnly {
		pdf.Cell(190, 5, "Referanseår - ikke klubbens regnskap i Brygge")
		pdf.Ln(5)
	}
	pdf.Ln(5)

	// Section A: Anskaffede midler (Revenue)
	pdf.SetFont("Helvetica", "B", 11)
	pdf.Cell(0, 7, "A. Anskaffede midler")
	pdf.Ln(8)

	reportTable(pdf, stmt.Revenue)

	pdf.SetFont("Helvetica", "B", 10)
	pdf.CellFormat(140, 7, "Sum anskaffede midler", "T", 0, "R", false, 0, "")
	pdf.CellFormat(50, 7, formatKr(stmt.TotalRevenue), "T", 0, "R", false, 0, "")
	pdf.Ln(12)

	// Section B: Forbrukte midler (Expenses)
	pdf.SetFont("Helvetica", "B", 11)
	pdf.Cell(0, 7, "B. Forbrukte midler")
	pdf.Ln(8)

	reportTable(pdf, stmt.Expenses)

	pdf.SetFont("Helvetica", "B", 10)
	pdf.CellFormat(140, 7, "Sum forbrukte midler", "T", 0, "R", false, 0, "")
	pdf.CellFormat(50, 7, formatKr(stmt.TotalExpenses), "T", 0, "R", false, 0, "")
	pdf.Ln(12)

	// Result
	pdf.SetFont("Helvetica", "B", 12)
	pdf.CellFormat(140, 8, "Resultat (A - B)", "TB", 0, "R", false, 0, "")
	pdf.CellFormat(50, 8, formatKr(stmt.Result), "TB", 0, "R", false, 0, "")
	pdf.Ln(15)

	// Footer
	pdfFooter(pdf, header)

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("generating PDF: %w", err)
	}
	return buf.Bytes(), nil
}

// BalanceSheetPDF renders the balanse as an A4 PDF.
func BalanceSheetPDF(header ReportHeader, bs *BalanceSheet) ([]byte, error) {
	pdf := newReportPDF()
	pdf.SetAutoPageBreak(true, 20)
	pdf.AddPage()

	// Title
	pdf.SetFont("Helvetica", "B", 16)
	pdf.Cell(0, 10, "Balanse")
	pdf.Ln(12)

	// Header info
	pdf.SetFont("Helvetica", "", 9)
	pdf.Cell(95, 5, header.ClubName)
	pdf.Cell(95, 5, fmt.Sprintf("Per 31.12.%d", header.Year))
	pdf.Ln(5)
	if header.OrgNumber != "" {
		pdf.Cell(95, 5, fmt.Sprintf("Org.nr: %s", header.OrgNumber))
	}
	pdf.Ln(5)
	if bs.ReferenceOnly {
		pdf.Cell(190, 5, "Referanseår - ikke klubbens regnskap i Brygge")
		pdf.Ln(5)
	}
	pdf.Ln(5)

	// Eiendeler (Assets)
	pdf.SetFont("Helvetica", "B", 11)
	pdf.Cell(0, 7, "Eiendeler")
	pdf.Ln(8)

	reportTable(pdf, bs.Assets)

	pdf.SetFont("Helvetica", "B", 10)
	pdf.CellFormat(140, 7, "Sum eiendeler", "T", 0, "R", false, 0, "")
	pdf.CellFormat(50, 7, formatKr(bs.TotalAssets), "T", 0, "R", false, 0, "")
	pdf.Ln(12)

	// Gjeld og egenkapital (Liabilities + Equity)
	pdf.SetFont("Helvetica", "B", 11)
	pdf.Cell(0, 7, "Gjeld og egenkapital")
	pdf.Ln(8)

	reportTable(pdf, bs.Liabilities)

	pdf.SetFont("Helvetica", "B", 10)
	pdf.CellFormat(140, 7, "Sum gjeld og egenkapital", "T", 0, "R", false, 0, "")
	pdf.CellFormat(50, 7, formatKr(bs.TotalLiabilities), "T", 0, "R", false, 0, "")
	pdf.Ln(12)

	if !bs.IsBalanced {
		pdf.SetFont("Helvetica", "B", 10)
		pdf.SetTextColor(200, 0, 0)
		pdf.Cell(0, 7, fmt.Sprintf("ADVARSEL: Balansen stemmer ikke (differanse: %s)", formatKr(bs.TotalAssets-bs.TotalLiabilities)))
		pdf.SetTextColor(0, 0, 0)
		pdf.Ln(10)
	}

	pdfFooter(pdf, header)

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("generating PDF: %w", err)
	}
	return buf.Bytes(), nil
}

func reportTable(pdf *reportPDF, lines []ReportLine) {
	pdf.SetFont("Helvetica", "B", 9)
	pdf.SetFillColor(240, 240, 240)
	pdf.CellFormat(25, 6, "Konto", "1", 0, "", true, 0, "")
	pdf.CellFormat(115, 6, "Beskrivelse", "1", 0, "", true, 0, "")
	pdf.CellFormat(50, 6, "Beløp", "1", 0, "R", true, 0, "")
	pdf.Ln(6)

	pdf.SetFont("Helvetica", "", 9)
	for _, line := range lines {
		pdf.CellFormat(25, 6, line.AccountCode, "1", 0, "", false, 0, "")
		pdf.CellFormat(115, 6, line.AccountName, "1", 0, "", false, 0, "")
		pdf.CellFormat(50, 6, formatKr(line.Amount), "1", 0, "R", false, 0, "")
		pdf.Ln(6)
	}
}

func pdfFooter(pdf *reportPDF, header ReportHeader) {
	pdf.SetFont("Helvetica", "I", 8)
	pdf.SetTextColor(128, 128, 128)
	pdf.Cell(0, 5, fmt.Sprintf("%s | Org.nr: %s | Generert %s",
		header.ClubName, header.OrgNumber, time.Now().Format("02.01.2006")))
}

func formatKr(amount float64) string {
	return fmt.Sprintf("kr %.2f", amount)
}
