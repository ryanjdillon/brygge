# Adding a new HTTP route

Six steps. Skipping the audit one is the most common mistake (silent gap in the trail); skipping the i18n one is the most visible (raw key string in the UI).

## 1. Handler

Pick the right file in `backend/internal/handlers/`. Convention is one file per resource — `invoices.go`, `accounting.go`, etc. Bulk-action variants live in `*_bulk_actions.go` siblings.

Standard handler shape:

```go
func (h *XHandler) HandleSomething(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    claims := middleware.GetClaims(ctx)
    if claims == nil {
        Error(w, http.StatusUnauthorized, "authentication required")
        return
    }
    // … do the work …
    if h.audit != nil {
        h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
            audit.ActionXyz, "resource", resourceID,
            map[string]any{"key": value})
    }
    JSON(w, http.StatusOK, result)
}
```

Use `Error(w, status, msg)` for failures; never bare `http.Error`. Use `JSON(w, status, body)` for success; never hand-marshal.

## 2. Route registration

Register in `backend/cmd/api/main.go` under the right route group. Pattern:

```go
r.With(
    middleware.RequireRole("treasurer", "admin"),
    middleware.RequireFreshTOTPDefault(),
).Post("/some/path", xHandler.HandleSomething)
```

- Role middleware always present for admin routes
- TOTP gate for any mutation more sensitive than a lookup
- Use `RequireFreshTOTPDefault()` (configured window) unless you specifically need a custom duration

## 3. Audit action

If the route writes data, add an `audit.Action*` constant in `backend/internal/audit/audit.go` and use it from the handler. See [`add-audit-action.md`](add-audit-action.md) for the constant-naming convention.

## 4. Tests

Add at least one test in `backend/internal/handlers/<resource>_test.go`. For integration tests, use `testutil.SkipIfNoDB(t)` + `testutil.SetupTestDB(t)` + `testutil.SeedClub(t, db)`.

If the handler depends on the audit service, pass `audit.NewService(db, zerolog.Nop())`.

## 5. Frontend wiring

If the SPA calls this endpoint:

- Add a typed helper in `frontend/src/composables/use<Resource>.ts` (or extend an existing one)
- Add i18n keys for any button labels, confirm dialogs, error messages, result toasts — en + nb + nn at minimum
- For TOTP-gated mutations, use `useFreshTotp().totpAwareFetch(...)` instead of bare fetch — it re-prompts and replays on 403 transparently

## 6. Docs

Update the subsystem README in `backend/internal/<pkg>/README.md` if the new route changes the package's public surface. If the route adds a new audit action, also update [`../audit-actions.md`](../audit-actions.md). If it touches an invariant, [`../invariants.md`](../invariants.md).

## Common misses

- **Forgot to gate on role**: route works for any authenticated user. Test with a member-tier session to catch it.
- **Forgot the audit log**: trail has a hole. The handler tests should assert the audit row exists.
- **Forgot the i18n keys**: SPA shows `admin.something.title` as raw text instead of the translation.
- **TOTP gate on a GET**: usually wrong — GETs shouldn't need step-up. Either the read is genuinely sensitive (in which case ok) or the gate should be on a sibling write.
