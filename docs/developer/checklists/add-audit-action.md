# Adding an audit action

Four steps. The naming convention matters most — once an action string ships, it's effectively permanent (changing it breaks any downstream tooling that filters on it).

## 1. Constant in `internal/audit/audit.go`

Add the constant in the right block (alphabetical within the block):

```go
const (
    // … existing constants …
    ActionInvoiceReminded     = "invoice.reminded"
    ActionInvoiceRegenerated  = "invoice.pdf_regenerated"
)
```

**Naming convention**: `<resource>.<verb>` for the action string. Past-tense verb because the action has already happened by the time the log row is written.

- ✅ `invoice.created`, `invoice.emailed`, `invoice.pdf_regenerated`, `accounting.bank_synced`
- ❌ `regen_invoice` (no resource prefix), `invoice.regenerate` (present tense), `InvoiceWasRegenerated` (no need for camelCase)

## 2. Call site

```go
if h.audit != nil {
    h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
        audit.ActionInvoiceReminded, "invoice", invoiceID,
        map[string]any{
            "invoice_number": invoiceNumber,
            "email":          deliverTo,
        })
}
```

- Always guard on `if h.audit != nil` — tests may pass nil
- Resource string matches the table name (`invoice`, `user`, `bank_import`)
- Extra map keys are snake_case for consistency with JSON wire format
- Never let an audit error bubble — log the error, but don't return it from the handler. The audit log failing shouldn't break the operation.

## 3. Reference doc

Add a row to [`../audit-actions.md`](../audit-actions.md):

| Constant | String | Resource | When it fires | Notable `extra` fields |
|---|---|---|---|---|
| `ActionInvoiceReminded` | `invoice.reminded` | invoice | Per row in `HandleBulkSendReminder` after `SendWithAttachment` succeeds | `email`, `invoice_number` |

## 4. Subsystem README

If the new action lives in a subsystem with a README, update it to mention the action in the relevant section. E.g. `backend/internal/handlers/README.md` lists per-resource actions under each resource's section.

## What the audit row actually looks like

```json
{
  "id": "uuid",
  "club_id": "uuid",
  "actor_id": "uuid",         // user.id of whoever did it
  "remote_addr": "1.2.3.4",
  "action": "invoice.reminded",
  "resource_type": "invoice",
  "resource_id": "uuid",
  "extra": { "email": "...", "invoice_number": 42 },
  "created_at": "2026-06-13T..."
}
```

The `extra` map is whatever the call site passes. It's free-form JSON, so use it for things future queries might want to filter on (counts, amounts, email addresses, before/after values for state changes).

## Common misses

- **Naming inconsistency** — `invoice.send` vs `invoice.emailed` for the same concept. Pick one tense and stick with it (this codebase uses past tense).
- **Forgot the reference doc update** — the next person adds another action and invents a name that overlaps yours.
- **Audit log inside a transaction that might roll back** — the audit insert happens outside the transaction in current handlers, so a rollback doesn't lose the audit. Mirror that.
- **`extra` field with sensitive data** — never log raw passwords, full PDF bytes, full TOTP secrets. The audit log is queryable by anyone with admin role.

## When the action might fire many times

For bulk operations, audit each row individually with the same action constant. The bulk wrapper itself can fire a separate top-level action (e.g. `accounting.vipps_resynced` with counts in `extra`). This way a query like "show me every invoice reminded today" works without the operator having to JOIN through the wrapper.
