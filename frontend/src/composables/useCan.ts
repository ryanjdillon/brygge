import { useAuthStore } from '@/stores/auth'
import { canWrite, type WriteArea } from '@/lib/adminAccess'

/**
 * useCan answers "may the signed-in user change this?" for an admin write
 * area, so a page the board may read hides the controls it cannot use.
 * Step-up prompts (ensureFreshTotp) stay where they are for writers.
 */
export function useCan() {
  const auth = useAuthStore()
  function can(area: WriteArea): boolean {
    return canWrite(area, auth.user?.roles ?? [])
  }
  return { can }
}
