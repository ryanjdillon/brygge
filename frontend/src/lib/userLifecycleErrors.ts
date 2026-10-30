// Maps the typed refusals of the admin user-lifecycle routes (deactivate,
// reactivate, role change) to a locale key. The codes are in
// docs/developer/reference/enums.md.
const KEYS: Record<string, string> = {
  last_admin: 'admin.users.lastAdminError',
  admin_required: 'admin.users.adminRequiredError',
}

export function userLifecycleErrorKey(code?: string | null): string {
  return (code && KEYS[code]) || 'admin.users.actionError'
}
