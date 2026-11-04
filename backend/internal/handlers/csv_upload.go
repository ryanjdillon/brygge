package handlers

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

// errCSVNotUTF8 is returned for an upload that is not valid UTF-8. Excel's
// default "CSV" export on a Norwegian install is Windows-1252, which would
// otherwise turn æ, ø and å into garbage that never matches a stored name.
var errCSVNotUTF8 = errors.New(`the file is not UTF-8; in the spreadsheet choose "Save as" → "CSV UTF-8" and upload it again`)

// errCSVNoHeader is returned for an empty upload or an unreadable header.
var errCSVNoHeader = errors.New("empty CSV or unreadable header")

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// csvUpload is an uploaded CSV positioned after its header row, with the
// header's columns indexed by lower-cased, trimmed name.
type csvUpload struct {
	*csv.Reader
	cols map[string]int
}

// has reports whether the header carries col.
func (u *csvUpload) has(col string) bool {
	_, ok := u.cols[col]
	return ok
}

// get returns record's trimmed value in col, or "" when the column is absent
// or the record is short.
func (u *csvUpload) get(record []string, col string) string {
	i, ok := u.cols[col]
	if !ok || i >= len(record) {
		return ""
	}
	return strings.TrimSpace(record[i])
}

// readCSVUpload reads an uploaded CSV and its header. The upload is read
// whole (callers cap the body), so it can be checked for UTF-8 before any row
// is parsed. A leading BOM is dropped: Excel's "CSV UTF-8" writes one, and
// left in place it becomes part of the first column's name, so that column is
// never found. The delimiter is ',' or ';', whichever the header line uses
// more: spreadsheets in locales with a decimal comma save with ';'.
func readCSVUpload(r io.Reader) (*csvUpload, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, errCSVNoHeader
	}
	data = bytes.TrimPrefix(data, utf8BOM)
	if !utf8.Valid(data) {
		return nil, errCSVNotUTF8
	}

	firstLine := data
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		firstLine = data[:i]
	}
	reader := csv.NewReader(bytes.NewReader(data))
	if bytes.Count(firstLine, []byte{';'}) > bytes.Count(firstLine, []byte{','}) {
		reader.Comma = ';'
	}
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1

	header, err := reader.Read()
	if err != nil {
		return nil, errCSVNoHeader
	}
	cols := map[string]int{}
	for i, name := range header {
		cols[strings.ToLower(strings.TrimSpace(name))] = i
	}
	return &csvUpload{Reader: reader, cols: cols}, nil
}
