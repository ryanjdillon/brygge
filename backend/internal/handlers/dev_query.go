package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// DevQueryHandler exposes a read-only SQL endpoint for operator
// debugging (DIL-365 Phase 1a). Two defenses-in-depth:
//
//   - Go-side validator rejects non-SELECT/WITH/EXPLAIN and stacked
//     statements before touching Postgres.
//   - Postgres-side `brygge_dev_ro` role (migration 000051) is granted
//     SELECT on public.* EXCEPT audit_log + developer_tokens, so even
//     if the validator misses something, the role can't mutate or
//     enumerate sensitive tables.
//
// Phase 1b (DIL-389) adds bearer-token auth so external tools can
// query without a cookie session.
type DevQueryHandler struct {
	db    *pgxpool.Pool
	audit *audit.Service
	log   zerolog.Logger
}

func NewDevQueryHandler(db *pgxpool.Pool, auditService *audit.Service, log zerolog.Logger) *DevQueryHandler {
	return &DevQueryHandler{
		db:    db,
		audit: auditService,
		log:   log.With().Str("handler", "dev_query").Logger(),
	}
}

type devQueryRequest struct {
	SQL   string `json:"sql"`
	Limit int    `json:"limit"`
}

type devQueryResponse struct {
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	RowCount  int      `json:"row_count"`
	ElapsedMs int64    `json:"elapsed_ms"`
	Truncated bool     `json:"truncated"`
}

const (
	defaultDevQueryLimit = 200
	maxDevQueryLimit     = 1000
	devQueryTimeout      = 15 * time.Second
)

func (h *DevQueryHandler) HandleQuery(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req devQueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sql, err := validateDevQuery(req.SQL)
	if err != nil {
		Error(w, http.StatusBadRequest, err.Error())
		return
	}

	limit := req.Limit
	if limit <= 0 {
		limit = defaultDevQueryLimit
	}
	if limit > maxDevQueryLimit {
		limit = maxDevQueryLimit
	}
	if !hasLimitClause(sql) {
		sql = fmt.Sprintf("%s LIMIT %d", sql, limit+1) // +1 to detect truncation
	}

	start := time.Now()
	resp, runErr := h.runQuery(ctx, sql, limit)
	elapsed := time.Since(start)
	resp.ElapsedMs = elapsed.Milliseconds()

	// Audit every attempt, success or fail. SHA the SQL so the raw
	// text (which may carry PII via WHERE) doesn't land in audit_log,
	// but repeated identical queries dedupe at the SHA level.
	if h.audit != nil {
		sum := sha256.Sum256([]byte(sql))
		extra := map[string]any{
			"sql_sha256": hex.EncodeToString(sum[:]),
			"row_count":  resp.RowCount,
			"elapsed_ms": resp.ElapsedMs,
			"truncated":  resp.Truncated,
		}
		if runErr != nil {
			extra["error"] = runErr.Error()
		}
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionDeveloperQuery, "developer_query", "",
			extra)
	}

	if runErr != nil {
		h.log.Warn().Err(runErr).Msg("dev query failed")
		Error(w, http.StatusBadRequest, runErr.Error())
		return
	}
	JSON(w, http.StatusOK, resp)
}

// runQuery wraps the user SQL in a transaction with SET LOCAL ROLE and
// a statement timeout, so an injection that bypassed validation still
// can't write or take down the DB.
func (h *DevQueryHandler) runQuery(parent context.Context, sql string, limit int) (devQueryResponse, error) {
	var resp devQueryResponse
	resp.Rows = make([][]any, 0)
	resp.Columns = []string{}

	ctx, cancel := context.WithTimeout(parent, devQueryTimeout+5*time.Second)
	defer cancel()

	tx, err := h.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return resp, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) // ROLLBACK is fine for read-only

	if _, err := tx.Exec(ctx, "SET LOCAL ROLE brygge_dev_ro"); err != nil {
		return resp, fmt.Errorf("set role: %w", err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL statement_timeout = '15s'"); err != nil {
		return resp, fmt.Errorf("set timeout: %w", err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL idle_in_transaction_session_timeout = '15s'"); err != nil {
		return resp, fmt.Errorf("set idle timeout: %w", err)
	}

	rows, err := tx.Query(ctx, sql)
	if err != nil {
		return resp, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	for _, fd := range rows.FieldDescriptions() {
		resp.Columns = append(resp.Columns, string(fd.Name))
	}

	for rows.Next() {
		if resp.RowCount >= limit {
			resp.Truncated = true
			break
		}
		vals, err := rows.Values()
		if err != nil {
			return resp, fmt.Errorf("scan: %w", err)
		}
		out := make([]any, len(vals))
		for i, v := range vals {
			out[i] = toJSONValue(v)
		}
		resp.Rows = append(resp.Rows, out)
		resp.RowCount++
	}
	if err := rows.Err(); err != nil {
		return resp, fmt.Errorf("iterate: %w", err)
	}
	return resp, nil
}

// toJSONValue normalises driver-returned values into types that
// encoding/json handles cleanly. pgx returns [16]byte for UUIDs which
// would otherwise serialise as a byte array; time.Time uses ISO8601;
// raw []byte is rendered as a string when it's actually text, base64
// only when it isn't valid UTF-8.
func toJSONValue(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case [16]byte:
		// UUID rendering — 8-4-4-4-12.
		return fmt.Sprintf("%x-%x-%x-%x-%x", x[0:4], x[4:6], x[6:8], x[8:10], x[10:16])
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	case []byte:
		// Heuristic: if it's printable, return as string; otherwise hex.
		if isLikelyText(x) {
			return string(x)
		}
		return hex.EncodeToString(x)
	default:
		return x
	}
}

func isLikelyText(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	for _, c := range b {
		if c < 0x20 && c != '\t' && c != '\n' && c != '\r' {
			return false
		}
	}
	return true
}

// validateDevQuery returns the trimmed-and-validated SQL ready to run.
// Rules: must start with SELECT/WITH/EXPLAIN; no semicolons except as
// final char; no SQL-line comments (`--`) or block comments (`/* */`)
// because they're the easiest way to smuggle stacked statements past
// a naive validator.
func validateDevQuery(raw string) (string, error) {
	sql := strings.TrimSpace(raw)
	if sql == "" {
		return "", errors.New("sql is required")
	}
	// Trim any trailing semicolons.
	sql = strings.TrimRight(sql, "; \t\n")
	if sql == "" {
		return "", errors.New("sql is required")
	}
	// Reject stacked statements.
	if strings.Contains(sql, ";") {
		return "", errors.New("multiple statements not allowed")
	}
	// Reject SQL comments — pgx already disallows stacked statements
	// in Query() but comments make the validation harder to reason
	// about, so we cut them off too.
	if strings.Contains(sql, "--") || strings.Contains(sql, "/*") {
		return "", errors.New("SQL comments not allowed")
	}

	upper := strings.ToUpper(sql)
	switch {
	case strings.HasPrefix(upper, "SELECT"):
	case strings.HasPrefix(upper, "WITH"):
	case strings.HasPrefix(upper, "EXPLAIN"):
	default:
		return "", errors.New("only SELECT, WITH, or EXPLAIN allowed")
	}
	return sql, nil
}

// hasLimitClause is a deliberately loose check — it just looks for the
// keyword "LIMIT" at the top level. False negatives (we append a LIMIT
// when the user already had one in a subquery) are harmless because
// the outer LIMIT just becomes a no-op cap; false positives (we
// don't append because LIMIT appears in a string literal) only matter
// if the user really intended unlimited, which they can't have anyway.
var limitClauseRE = regexp.MustCompile(`(?i)\bLIMIT\s+\d`)

func hasLimitClause(sql string) bool {
	return limitClauseRE.MatchString(sql)
}
