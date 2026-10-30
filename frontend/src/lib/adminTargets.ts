// Who may change an admin (BRD-9): a member who holds the admin role, and the
// grant of that role, are the admin's to change. The server refuses a
// non-admin (403 admin_required); these keep the controls out of the board's
// way.
export function canManageMember(viewerIsAdmin: boolean, targetRoles?: readonly string[] | null): boolean {
  return viewerIsAdmin || !(targetRoles ?? []).includes('admin')
}

export function assignableRoles(viewerIsAdmin: boolean, roles: readonly string[]): string[] {
  return viewerIsAdmin ? [...roles] : roles.filter((r) => r !== 'admin')
}
