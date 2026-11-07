// An applicant has confirmed a membership application but not paid yet. The
// server is the hard gate (middleware.ApplicantRoutes answers everything else
// with 403 membership_not_completed); this mirrors it so the SPA sends an
// applicant to the page that finishes joining instead of to pages that fail.

/** Where an applicant-only user lands from any page they may not open. */
export const APPLICANT_COMPLETE_PATH = '/join/complete'

/**
 * True when the roles are the applicant role and nothing else, mirroring
 * roles.IsApplicantOnly. Any other role lifts the restriction: a board role
 * is valid without member.
 */
export function isApplicantOnly(roles: readonly string[]): boolean {
  return roles.length > 0 && roles.every((r) => r === 'applicant')
}
