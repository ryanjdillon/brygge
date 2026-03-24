package accounting

import (
	"bytes"
	"fmt"

	"github.com/go-pdf/fpdf"
)

// MomskompPDF renders the momskompensasjon report as an A4 PDF.
func MomskompPDF(header ReportHeader, report *MomskompReport) ([]byte, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetAutoPageBreak(true, 20)
	pdf.AddPage()

	// Title
	pdf.SetFont("Helvetica", "B", 16)
	pdf.Cell(0, 10, "Momskompensasjon")
	pdf.Ln(12)

	// Header
	pdf.SetFont("Helvetica", "", 9)
	pdf.Cell(95, 5, header.ClubName)
	pdf.Cell(95, 5, fmt.Sprintf("Regnskapsår %d", header.Year))
	pdf.Ln(5)
	if header.OrgNumber != "" {
		pdf.Cell(95, 5, fmt.Sprintf("Org.nr: %s", header.OrgNumber))
	}
	pdf.Ln(10)

	// Model info
	pdf.SetFont("Helvetica", "B", 10)
	modelName := "Forenklet modell"
	if report.Model == "documented" {
		modelName = "Dokumentert modell"
	}
	pdf.Cell(0, 6, fmt.Sprintf("Beregningsmodell: %s", modelName))
	pdf.Ln(10)

	// Warning for draft entries
	if report.HasDraftEntries {
		pdf.SetFont("Helvetica", "B", 9)
		pdf.SetTextColor(200, 100, 0)
		pdf.Cell(0, 6, "ADVARSEL: Det finnes uposterte bilag i perioden. Rapporten kan være ufullstendig.")
		pdf.SetTextColor(0, 0, 0)
		pdf.Ln(8)
	}

	// Summary
	pdf.SetFont("Helvetica", "", 10)
	summaryRow(pdf, "Totale driftskostnader", report.TotalOperatingCosts)
	summaryRow(pdf, "Kompensasjonsberettigede kostnader", report.EligibleCosts)
	summaryRow(pdf, "Ikke-berettigede kostnader", report.IneligibleCosts)
	if report.PartialCosts > 0 {
		summaryRow(pdf, "Delvis berettigede kostnader (før fordeling)", report.PartialCosts)
	}
	pdf.Ln(4)
	pdf.SetFont("Helvetica", "B", 11)
	summaryRow(pdf, "Beregnet kompensasjon", report.CompensationAmount)
	pdf.Ln(10)

	// Breakdown table
	pdf.SetFont("Helvetica", "B", 10)
	pdf.Cell(0, 7, "Fordeling per konto")
	pdf.Ln(8)

	pdf.SetFont("Helvetica", "B", 8)
	pdf.SetFillColor(240, 240, 240)
	pdf.CellFormat(20, 6, "Konto", "1", 0, "", true, 0, "")
	pdf.CellFormat(60, 6, "Beskrivelse", "1", 0, "", true, 0, "")
	pdf.CellFormat(30, 6, "Beløp", "1", 0, "R", true, 0, "")
	pdf.CellFormat(25, 6, "MVA", "1", 0, "R", true, 0, "")
	pdf.CellFormat(25, 6, "Status", "1", 0, "C", true, 0, "")
	pdf.CellFormat(30, 6, "Berettiget", "1", 0, "R", true, 0, "")
	pdf.Ln(6)

	pdf.SetFont("Helvetica", "", 8)
	for _, ab := range report.BreakdownByAccount {
		statusLabel := eligibilityLabel(ab.Eligibility)
		pdf.CellFormat(20, 6, ab.AccountCode, "1", 0, "", false, 0, "")
		pdf.CellFormat(60, 6, ab.AccountName, "1", 0, "", false, 0, "")
		pdf.CellFormat(30, 6, formatKr(ab.TotalAmount), "1", 0, "R", false, 0, "")
		pdf.CellFormat(25, 6, formatKr(ab.MVAAmount), "1", 0, "R", false, 0, "")
		pdf.CellFormat(25, 6, statusLabel, "1", 0, "C", false, 0, "")
		pdf.CellFormat(30, 6, formatKr(ab.EligiblePart), "1", 0, "R", false, 0, "")
		pdf.Ln(6)
	}

	pdf.Ln(10)

	// Model explanation
	pdf.SetFont("Helvetica", "I", 8)
	pdf.SetTextColor(100, 100, 100)
	if report.Model == "simplified" {
		pdf.MultiCell(0, 4, "Forenklet modell: Kompensasjon beregnes som 8% av de første 7 millioner kroner i berettigede driftskostnader, pluss 6% av beløp over 7 millioner.", "", "", false)
	} else {
		pdf.MultiCell(0, 4, "Dokumentert modell: Kompensasjon basert på faktisk merverdiavgift betalt på berettigede kostnader, hentet fra bokførte bilag.", "", "", false)
	}
	pdf.SetTextColor(0, 0, 0)
	pdf.Ln(5)

	pdfFooter(pdf, header)

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("generating PDF: %w", err)
	}
	return buf.Bytes(), nil
}

func summaryRow(pdf *fpdf.Fpdf, label string, amount float64) {
	pdf.CellFormat(130, 6, label, "", 0, "", false, 0, "")
	pdf.CellFormat(60, 6, formatKr(amount), "", 0, "R", false, 0, "")
	pdf.Ln(6)
}

func eligibilityLabel(e string) string {
	switch e {
	case "eligible":
		return "Ja"
	case "ineligible":
		return "Nei"
	case "partial":
		return "Delvis"
	default:
		return "-"
	}
}
