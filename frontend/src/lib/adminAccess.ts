// Who may see and who may change what in the admin area. The server is
// the hard gate (cmd/api/main.go); this mirrors it so the SPA shows
// board members every page they may read and hides the controls they
// could not use (BRY-372).

/** The board (styre) roles, mirroring roles.Board on the server. */
export const BOARD_ROLES = [
  'chair',
  'vice_chair',
  'treasurer',
  'secretary',
  'harbor_master',
  'deputy',
  'board',
] as const

/** Any board role or admin may open the admin area read-only. */
export function canViewAdmin(roles: readonly string[]): boolean {
  return roles.some((r) => r === 'admin' || (BOARD_ROLES as readonly string[]).includes(r))
}

/**
 * Write areas and the roles the server accepts for their writes. Each
 * list is the role set of the matching RequireRole / moneyGate in
 * cmd/api/main.go; keep the two in step.
 */
export const WRITE_ROLES = {
  /** Faktura, Tildel, imports, syncs, pricing, economy and bank settings. */
  money: ['treasurer', 'admin'],
  /** Accounting group writes outside moneyGate: accounts, periods, rules, Vipps aliases. */
  ledger: ['treasurer', 'board', 'admin'],
  /** Member records under /admin/users. */
  members: ['board', 'admin'],
  /** Slips and harbor layout. */
  harbor: ['board', 'harbor_master', 'admin'],
} as const satisfies Record<string, readonly string[]>

export type WriteArea = keyof typeof WRITE_ROLES

export function canWrite(area: WriteArea, roles: readonly string[]): boolean {
  return roles.some((r) => (WRITE_ROLES[area] as readonly string[]).includes(r))
}

/** Route meta for pages only a site admin may open (Developer and Site). */
export function canOpenAdminRoute(
  meta: { requiresAdmin?: boolean; adminOnly?: boolean },
  roles: readonly string[],
): boolean {
  if (meta.adminOnly) return roles.includes('admin')
  if (meta.requiresAdmin) return canViewAdmin(roles)
  return true
}
