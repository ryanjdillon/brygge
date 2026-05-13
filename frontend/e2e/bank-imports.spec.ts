import { test, expect } from '@playwright/test'

// The /admin/accounting/bank-imports route is auth-gated.
// Without a session it must redirect to /login — this is the
// minimum smoke we can run without the full dev DB stack.
// When a future test fixture provides an authenticated admin
// session, this file is where the upload/reconcile flow tests
// should land (covers DIL-285).
test.describe('Bank imports route', () => {
  test('redirects to login when unauthenticated', async ({ page }) => {
    await page.goto('/admin/accounting/bank-imports')
    await page.waitForURL(/\/login(\?|$)/)
    await expect(page.locator('h1')).toBeVisible()
  })
})
