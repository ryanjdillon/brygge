# Adding a bulk action

`POST /api/v1/admin/.../bulk-something` with body `{ids: [...]}`. Common pattern for bulk send, regenerate, void, delete, copy-emails-like operations.

## 1. Request shape

```go
type bulkXyzRequest struct {
    IDs []string `json:"ids"`
}
```

Validate `len(req.IDs) > 0` with a 400.

⚠️ **Type name collision**: there's already a `bulkInvoiceRequest` in `invoices_bulk.go`. New bulk-action requests must use a distinct name (`bulkInvoiceActionRequest`, `bulkXyzActionRequest`, etc.). This was a real burn in [DIL-364].

## 2. Result shape

```go
type bulkInvoiceResult struct {
    Processed int      `json:"processed"`
    Skipped   int      `json:"skipped"`
    Failures  []string `json:"failures"`
}
```

⚠️ **Initialize `Failures` as `[]string{}`, not nil**. Go's nil slice serializes to JSON `null`. The SPA template that does `result.failures.length` crashes on null. This was a real burn in [DIL-369] — the resync result returned `null` and the BankImportsView blew up with a TypeError.

```go
res := bulkInvoiceResult{Failures: []string{}}
```

Or defend on the frontend too:

```vue
{{ result.failures?.length ?? 0 }}
```

Both. Defense in depth — the backend fix matters because curl callers also see the wrong shape.

## 3. Skip vs fail

A "skipped" row is a deliberate decision: not eligible (already paid, never sent, would violate invariant). A "failed" row is an error during processing. Never conflate them.

```go
for _, id := range req.IDs {
    err := h.doOne(ctx, ..., id, ...)
    switch {
    case err == nil:
        res.Processed++
    case errors.Is(err, errSkip):
        res.Skipped++
    default:
        res.Failures = append(res.Failures, id+": "+err.Error())
        h.log.Warn().Err(err).Str("id", id).Msg("bulk action failed for row")
    }
}
```

## 4. Audit per row

Audit each processed row with the same `audit.Action*` constant. Don't aggregate — the audit log is per-action, not per-bulk-run.

If the bulk operation itself needs a record (e.g. "treasurer kicked off a Vipps resync"), use a separate action like `accounting.vipps_resynced` for the top-level event.

## 5. Frontend

- Confirm dialog before firing — operator must explicitly opt in
- `useFreshTotp().ensureFreshTotp()` at click time (matches the backend's `RequireFreshTOTPDefault()` route)
- Result toast: "Resynced N/M, skipped K, failed J"
- Refresh the affected list (refetch query) so the UI reflects the outcome
- i18n keys: confirm title, confirm body, action label, result toast, "no rows selected" guard message

## 6. Route registration

```go
r.With(
    middleware.RequireRole("treasurer", "admin"),
    middleware.RequireFreshTOTPDefault(),
).Post("/.../bulk-xyz", xHandler.HandleBulkXyz)
```

## 7. Docs

If the bulk action introduces a new audit action, update [`../reference/audit-actions.md`](../reference/audit-actions.md). If it touches an invariant (e.g. bulk-regenerate now archives PDFs per bokføringsloven), update [`../reference/invariants.md`](../reference/invariants.md). If you wrote a tricky pattern that future bulk actions should follow, update `backend/internal/handlers/README.md`.

## Common misses

- **Failures as nil slice** — see above. Same shape applies to any other slice in the response (e.g. `WarningCodes []string`).
- **No skip distinction** — every "not eligible" row gets counted as a failure, alarming the operator. Tweak the predicate.
- **TOTP gate skipped** — the route should match the per-row equivalent's gating. If `POST /invoices/{id}/send` requires TOTP, so does `POST /invoices/bulk-send`.
- **Per-row work not idempotent** — a partial run that gets retried double-applies. Make the per-row handler idempotent (check `payment_id IS NOT NULL`, etc.) before processing.
