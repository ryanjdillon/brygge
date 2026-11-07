import { test, expect, type Page, type BrowserContext, type Request } from '@playwright/test'
import {
  assertLocalStack,
  assertRateLimitIsolation,
  authAudit,
  createUser,
  insertLoginToken,
  isolateRateLimit,
  loginLinkCount,
  loginTokenFromMail,
  loginTokenUsed,
  mailpitAvailable,
  retireUsers,
  sweepStaleUsers,
  type TestUser,
} from './support/stack'

// Magic-link login end to end (BRY-661, covering BRY-658): the email links
// to the SPA page /login/verify?token=…, which drops the token from the
// address bar and logs in only when the button POSTs it. Needs the full
// local stack (API + Postgres + Redis, Mailpit for the emailed-link case);
// see CONTRIBUTING.md §6 "End-to-end tests". Tokens are stored only as
// sha256 hex, so most cases insert a link row with a token the test made.

const SESSION_COOKIE = 'brygge_session'
const VERIFY_API = '/api/v1/auth/verify'
// The portal itself or any page under it; not /login.
const PORTAL_URL = /\/portal(\/[^?#]*)?$/

const created: TestUser[] = []

function track(user: TestUser): TestUser {
  created.push(user)
  return user
}

function member(): TestUser {
  return track(createUser(['member']))
}

// Playwright needs the fixtures argument destructured, even when empty.
// eslint-disable-next-line no-empty-pattern
test.beforeAll(async ({}, workerInfo) => {
  assertLocalStack()
  sweepStaleUsers()
  await assertRateLimitIsolation(workerInfo.workerIndex)
})

test.afterAll(() => {
  retireUsers(created)
})

let clientIp = ''
test.beforeEach(async ({ context }, testInfo) => {
  clientIp = await isolateRateLimit(context, testInfo.workerIndex)
})

/**
 * Records the POSTs to the verify API the page makes. Through a route rather
 * than the request event: Chromium leaves postData() empty for a fetch whose
 * body comes from a Request object (openapi-fetch), unless it is intercepted.
 */
async function verifyPosts(page: Page): Promise<Request[]> {
  const posts: Request[] = []
  await page.route(
    (url) => url.pathname === VERIFY_API,
    async (route) => {
      if (route.request().method() === 'POST') posts.push(route.request())
      await route.continue()
    },
  )
  return posts
}

/** Exactly one POST, carrying the token only in its JSON body. */
async function expectOneTokenPost(posts: Request[], token: string) {
  expect(posts, 'one POST to the verify API').toHaveLength(1)
  const post = posts[0]
  expect(post.url()).not.toContain('token=')
  expect(post.url()).not.toContain(token)
  expect(post.postDataJSON()).toEqual({ token })
  const referer = (await post.allHeaders())['referer'] ?? ''
  expect(referer, 'the Referer must not leak the token').not.toContain(token)
}

async function sessionCookie(context: BrowserContext) {
  return (await context.cookies()).find((c) => c.name === SESSION_COOKIE)
}

/** Opens the login link and checks the page that must not log in by itself. */
async function openLink(page: Page, token: string) {
  const posts = await verifyPosts(page)
  await page.goto(`/login/verify?token=${encodeURIComponent(token)}`)
  const button = page.getByTestId('verify-button')
  await expect(button).toBeVisible()
  // The token leaves the address bar (router.replace).
  await expect(page).toHaveURL(/\/login\/verify$/)
  expect(page.url()).not.toContain(token)
  expect(posts, 'opening the link must not POST the token').toEqual([])
  expect(loginTokenUsed(token), 'opening the link must not consume it').toBe(false)
  return { button, posts }
}

async function expectLoggedIn(page: Page, context: BrowserContext, url: RegExp = PORTAL_URL) {
  await expect(page).toHaveURL(url)
  const cookie = await sessionCookie(context)
  expect(cookie, 'session cookie').toBeDefined()
  expect(cookie?.httpOnly).toBe(true)
  expect(cookie?.sameSite).toBe('Lax')
}

test.describe('magic-link login', () => {
  test('requesting a link shows the check-your-email state and stores a link', async ({ page }) => {
    const user = member()
    await page.goto('/login')
    await page.locator('#email').fill(user.email)
    await page.getByTestId('login-send-link').click()
    await expect(page.getByTestId('login-link-sent')).toBeVisible()
    await expect(page.locator('#email')).toHaveCount(0)
    expect(loginLinkCount(user.email)).toBe(1)
  })

  test('the emailed link logs in (Mailpit)', async ({ page, context }) => {
    test.skip(!(await mailpitAvailable()), 'Mailpit is not running')
    const user = member()
    await page.goto('/login')
    await page.locator('#email').fill(user.email)
    await page.getByTestId('login-send-link').click()
    await expect(page.getByTestId('login-link-sent')).toBeVisible()

    const token = await loginTokenFromMail(user.email)
    const { button, posts } = await openLink(page, token)
    await button.click()
    await expectLoggedIn(page, context)
    await expectOneTokenPost(posts, token)
    expect(loginTokenUsed(token)).toBe(true)
  })

  test('a valid link logs in only on the button, once', async ({ page, context, browser }, testInfo) => {
    const user = member()
    const token = insertLoginToken(user.email)
    const firstIp = clientIp

    const { button, posts } = await openLink(page, token)
    await button.click()
    await expectLoggedIn(page, context)
    await expectOneTokenPost(posts, token)
    expect(loginTokenUsed(token)).toBe(true)

    // The same link in a fresh browser is spent.
    const other = await browser.newContext()
    let secondIp = ''
    try {
      secondIp = await isolateRateLimit(other, testInfo.workerIndex)
      const page2 = await other.newPage()
      await page2.goto(`/login/verify?token=${encodeURIComponent(token)}`)
      await page2.getByTestId('verify-button').click()
      await expect(page2.getByTestId('verify-problem-login_token_used')).toBeVisible()
      await expect(page2.getByTestId('verify-request-new')).toBeVisible()
      expect(await sessionCookie(other)).toBeUndefined()
    } finally {
      await other.close()
    }

    // BRY-659: the login and the refused reuse are both audited, each with
    // the client IP the API took from X-Forwarded-For.
    await expect
      .poll(() => authAudit(user.id))
      .toEqual([
        { action: 'auth.login_success', method: 'magic_link', reason: '', ip: firstIp },
        { action: 'auth.login_failed', method: 'magic_link', reason: 'used', ip: secondIp },
      ])
  })

  test('an expired link shows the expired problem and stays unused', async ({ page, context }) => {
    const user = member()
    const token = insertLoginToken(user.email, -1)
    const { button, posts } = await openLink(page, token)
    await button.click()
    await expect(page.getByTestId('verify-problem-login_token_expired')).toBeVisible()
    await expectOneTokenPost(posts, token)
    expect(await sessionCookie(context)).toBeUndefined()
    // The consume only matches unexpired rows, so the row is left as it was.
    expect(loginTokenUsed(token)).toBe(false)
    await expect
      .poll(() => authAudit(user.id))
      .toEqual([{ action: 'auth.login_failed', method: 'magic_link', reason: 'expired', ip: clientIp }])
  })

  test('an unknown token shows the invalid problem', async ({ page, context }) => {
    const token = 'e2e-not-a-real-token'
    const posts = await verifyPosts(page)
    await page.goto(`/login/verify?token=${token}`)
    await expect(page).toHaveURL(/\/login\/verify$/)
    await page.getByTestId('verify-button').click()
    await expect(page.getByTestId('verify-problem-login_token_invalid')).toBeVisible()
    await expectOneTokenPost(posts, token)
    expect(await sessionCookie(context)).toBeUndefined()
  })

  test('the page without a token says to open the link from the email', async ({ page }) => {
    const posts = await verifyPosts(page)
    await page.goto('/login/verify')
    await expect(page.getByTestId('verify-problem-login_token_missing')).toBeVisible()
    await expect(page.getByTestId('verify-button')).toHaveCount(0)
    expect(posts).toEqual([])
  })

  test('the legacy GET link redirects to the page without consuming the token', async ({ page, context }) => {
    const user = member()
    const token = insertLoginToken(user.email)
    const posts = await verifyPosts(page)

    await page.goto(`${VERIFY_API}?token=${encodeURIComponent(token)}`)
    await expect(page.getByTestId('verify-button')).toBeVisible()
    await expect(page).toHaveURL(/\/login\/verify$/)
    expect(posts).toEqual([])
    expect(loginTokenUsed(token)).toBe(false)

    await page.getByTestId('verify-button').click()
    await expectLoggedIn(page, context)
    await expectOneTokenPost(posts, token)
    expect(loginTokenUsed(token)).toBe(true)
  })

  test('an applicant lands on the finish-joining page', async ({ page, context }) => {
    const applicant = track(createUser(['applicant'], 'applicant'))
    const token = insertLoginToken(applicant.email)
    const { button, posts } = await openLink(page, token)
    await button.click()
    await expectLoggedIn(page, context, /\/join\/complete$/)
    await expectOneTokenPost(posts, token)
  })
})

test.describe('magic-link login on a phone (375px)', () => {
  test.use({ viewport: { width: 375, height: 812 }, hasTouch: true, isMobile: true })

  test('the verify page fits and the button is tappable', async ({ page, context }) => {
    const user = member()
    const token = insertLoginToken(user.email)
    const { button, posts } = await openLink(page, token)

    const box = await button.boundingBox()
    expect(box).not.toBeNull()
    expect(box!.height).toBeGreaterThanOrEqual(44)
    expect(box!.x).toBeGreaterThanOrEqual(0)
    expect(box!.x + box!.width).toBeLessThanOrEqual(375)
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
    expect(overflow, 'no horizontal page scroll').toBeLessThanOrEqual(0)

    await button.tap()
    await expectLoggedIn(page, context)
    await expectOneTokenPost(posts, token)
  })

  test('the problem state fits too', async ({ page }) => {
    await page.goto('/login/verify')
    const action = page.getByTestId('verify-request-new')
    await expect(action).toBeVisible()
    const box = await action.boundingBox()
    expect(box).not.toBeNull()
    expect(box!.height).toBeGreaterThanOrEqual(44)
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
    expect(overflow).toBeLessThanOrEqual(0)
  })
})
