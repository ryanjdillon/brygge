import { APPLICANT_COMPLETE_PATH } from '@/lib/applicantAccess'

/** The 403 error the server answers an applicant-only session with. */
export const MEMBERSHIP_NOT_COMPLETED = 'membership_not_completed'

/**
 * Sends the user to the page that finishes joining when the server refused
 * a request because their membership is not complete. The router guard
 * keeps applicants off member pages; this catches a request made anyway
 * (a stale tab, a shared component). The router is imported lazily: it
 * pulls in the views, and the API client must not.
 */
export async function routeIfMembershipIncomplete(status: number, error: unknown): Promise<boolean> {
  if (status !== 403 || error !== MEMBERSHIP_NOT_COMPLETED) return false
  const { router } = await import('@/router')
  if (router.currentRoute.value.path !== APPLICANT_COMPLETE_PATH) {
    await router.push(APPLICANT_COMPLETE_PATH)
  }
  return true
}
