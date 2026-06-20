// migrate-invoices-to-s3 uploads existing invoice PDFs stored in the
// database BYTEA columns to S3 (S3_BUCKET_LEGAL) and nulls the source
// columns to recover storage. Run once after deploying migration 000065
// and before removing the pdf_data columns.
//
// Environment variables (same as the API):
//
//	DATABASE_URL, S3_ENDPOINT, S3_BUCKET_LEGAL, S3_ACCESS_KEY, S3_SECRET_KEY
//
// The tool is idempotent: rows that already have an s3_key are skipped.
// Run with DRY_RUN=1 to preview without writing anything.
package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brygge-klubb/brygge/internal/storage"
)

func main() {
	ctx := context.Background()
	dryRun := os.Getenv("DRY_RUN") == "1"

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	s3Endpoint := os.Getenv("S3_ENDPOINT")
	s3Bucket := os.Getenv("S3_BUCKET_LEGAL")
	s3Access := os.Getenv("S3_ACCESS_KEY")
	s3Secret := os.Getenv("S3_SECRET_KEY")

	s3Client, err := storage.NewClient(s3Endpoint, s3Bucket, s3Access, s3Secret)
	if err != nil || !s3Client.IsConfigured() {
		log.Fatal("S3 not configured; set S3_ENDPOINT, S3_BUCKET_LEGAL, S3_ACCESS_KEY, S3_SECRET_KEY")
	}

	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer db.Close()

	if dryRun {
		log.Println("DRY RUN — no changes will be written")
	}

	migrateInvoices(ctx, db, s3Client, dryRun)
	migrateArchive(ctx, db, s3Client, dryRun)
}

// migrateInvoices uploads invoice PDFs from invoices.pdf_data to S3.
func migrateInvoices(ctx context.Context, db *pgxpool.Pool, s3 *storage.Client, dryRun bool) {
	rows, err := db.Query(ctx,
		`SELECT id, club_id, pdf_data
		   FROM invoices
		  WHERE pdf_data IS NOT NULL AND s3_key IS NULL
		  ORDER BY created_at`)
	if err != nil {
		log.Fatalf("query invoices: %v", err)
	}
	defer rows.Close()

	var ok, failed int
	for rows.Next() {
		var id, clubID string
		var pdfData []byte
		if err := rows.Scan(&id, &clubID, &pdfData); err != nil {
			log.Printf("scan invoice: %v", err)
			failed++
			continue
		}
		key := fmt.Sprintf("clubs/%s/invoices/%s.pdf", clubID, id)
		if dryRun {
			log.Printf("DRY: would upload invoice %s → %s (%d bytes)", id, key, len(pdfData))
			ok++
			continue
		}
		if err := s3.Upload(ctx, key, bytes.NewReader(pdfData), int64(len(pdfData)), "application/pdf"); err != nil {
			log.Printf("ERROR upload invoice %s: %v", id, err)
			failed++
			continue
		}
		if _, err := db.Exec(ctx,
			`UPDATE invoices SET pdf_data = NULL, s3_key = $1 WHERE id = $2`,
			key, id,
		); err != nil {
			log.Printf("ERROR update invoice %s: %v", id, err)
			failed++
			continue
		}
		log.Printf("migrated invoice %s → %s", id, key)
		ok++
	}
	if err := rows.Err(); err != nil {
		log.Printf("rows error: %v", err)
	}
	log.Printf("invoices: %d migrated, %d failed", ok, failed)
}

// migrateArchive uploads PDFs from invoice_pdf_archive.pdf_data to S3.
func migrateArchive(ctx context.Context, db *pgxpool.Pool, s3 *storage.Client, dryRun bool) {
	rows, err := db.Query(ctx,
		`SELECT a.id, i.club_id, a.pdf_data
		   FROM invoice_pdf_archive a
		   JOIN invoices i ON i.id = a.invoice_id
		  WHERE a.pdf_data IS NOT NULL AND a.s3_key IS NULL
		  ORDER BY a.archived_at`)
	if err != nil {
		log.Fatalf("query archive: %v", err)
	}
	defer rows.Close()

	var ok, failed int
	for rows.Next() {
		var id, clubID string
		var pdfData []byte
		if err := rows.Scan(&id, &clubID, &pdfData); err != nil {
			log.Printf("scan archive: %v", err)
			failed++
			continue
		}
		key := fmt.Sprintf("clubs/%s/invoices/archive/%s.pdf", clubID, id)
		if dryRun {
			log.Printf("DRY: would upload archive %s → %s (%d bytes)", id, key, len(pdfData))
			ok++
			continue
		}
		if err := s3.Upload(ctx, key, bytes.NewReader(pdfData), int64(len(pdfData)), "application/pdf"); err != nil {
			log.Printf("ERROR upload archive %s: %v", id, err)
			failed++
			continue
		}
		if _, err := db.Exec(ctx,
			`UPDATE invoice_pdf_archive SET pdf_data = NULL, s3_key = $1 WHERE id = $2`,
			key, id,
		); err != nil {
			log.Printf("ERROR update archive %s: %v", id, err)
			failed++
			continue
		}
		log.Printf("migrated archive %s → %s", id, key)
		ok++
	}
	if err := rows.Err(); err != nil {
		log.Printf("rows error: %v", err)
	}
	log.Printf("archive: %d migrated, %d failed", ok, failed)
}
