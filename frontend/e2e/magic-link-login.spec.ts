import { test, expect, type Page, type BrowserContext, type Request } from '@playwright/test'
import {
  assertLocalStack,
  assertRateLimitIsolation,
  authAudit,
  BASE_URL,
  createUser,
  insertJoinApplication,
  insertLoginToken,
  joinAddress,
  isolateRateLimit,
  loginLinkCount,
  loginTokenFromMail,
  loginTokenUsed,
  mailpitAvailable,
  retireUsers,
  sweepStaleUsers,
  type TestUser,
} from './support/stack'

// Magic-link login end to end (BRY-661, covering BRY-658 and BRY-660): the
// email links to the SPA page /login/verify?token=…, which drops the token
// from the address bar, asks which account the link is for (the peek, which
// consumes nothing), shows it, and logs in only when the button POSTs the
// token. A cross-site POST never reaches the API's handlers. Needs the full
// local stack (API + Postgres + Redis, Mailpit for the emailed-link case);
// see CONTRIBUTING.md §6 "End-to-end tests". Tokens are stored only as
// sha256 hex, so most cases insert a link row with a token the test made.

const SESSION_COOKIE = 'brygge_session'
const VERIFY_API = '/api/v1/auth/verify'
const PEEK_API = '/api/v1/auth/verify/peek'
// The portal itself or any page under it; not /login.
const PORTAL_URL = /\/portal(\/[^?#]*)?$/

const created: TestUser[] = []

function track(user: TestUser): TestUser {
  created.push(user)
  return user
}

function member(label = 'login'): TestUser {
  return track(createUser(['member'], label))
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
 * Records the POSTs the page makes to path. Through a route rather than the
 * request event: Chromium leaves postData() empty for a fetch whose body
 * comes from a Request object (openapi-fetch), unless it is intercepted.
 */
async function recordPosts(page: Page, path: string): Promise<Request[]> {
  const posts: Request[] = []
  await page.route(
    (url) => url.pathname === path,
    async (route) => {
      if (route.request().method() === 'POST') posts.push(route.request())
      await route.continue()
    },
  )
  return posts
}

/** The POSTs to the verify API: the only ones that can consume a token. */
function verifyPosts(page: Page): Promise<Request[]> {
  return recordPosts(page, VERIFY_API)
}

/** The peek POSTs: ask which account a token logs in as, consume nothing. */
function peekPosts(page: Page): Promise<Request[]> {
  return recordPosts(page, PEEK_API)
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
async function openLink(page: Page, token: string, account: string) {
  const posts = await verifyPosts(page)
  const peeks = await peekPosts(page)
  await page.goto(`/login/verify?token=${encodeURIComponent(token)}`)
  const button = page.getByTestId('verify-button')
  await expect(button).toBeVisible()
  // The button comes with the account the link logs in as (BRY-660).
  await expect(page.getByTestId('verify-account')).toHaveText(account)
  // The token leaves the address bar (router.replace).
  await expect(page).toHaveURL(/\/login\/verify$/)
  expect(page.url()).not.toContain(token)
  // Only the peek was sent, with the token in its JSON body.
  expect(peeks.length, 'the page peeks on load').toBeGreaterThanOrEqual(1)
  for (const peek of peeks) {
    expect(peek.url()).not.toContain(token)
    expect(peek.postDataJSON()).toEqual({ token })
    // The token left the address bar before the peek was sent.
    const referer = (await peek.allHeaders())['referer'] ?? ''
    expect(referer, 'the peek Referer must not leak the token').not.toContain(token)
  }
  expect(posts, 'opening the link must not POST to verify').toEqual([])
  expect(loginTokenUsed(token), 'opening the link must not consume it').toBe(false)
  return { button, posts, peeks }
}

/**
 * The app's dev server under another loopback host name (127.0.0.1 or
 * [::1] for localhost): another site in the browser's eyes, but served by a
 * real server on this machine. Vite listens on one address family only, so
 * the first one that answers is used.
 */
async function crossSiteOrigin(): Promise<string> {
  const app = new URL(BASE_URL)
  const port = app.port ? `:${app.port}` : ''
  for (const host of ['127.0.0.1', '[::1]', 'localhost']) {
    if (host === app.hostname) continue
    const origin = `${app.protocol}//${host}${port}`
    try {
      const res = await fetch(`${origin}/`, { signal: AbortSignal.timeout(2000) })
      if (res.ok) return origin
    } catch {
      // Not listening on that address; try the next.
    }
  }
  throw new Error(`the dev server at ${BASE_URL} answers under no other loopback host name`)
}

/** The page has no horizontal scroll at its current width. */
async function expectNoOverflow(page: Page) {
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
  expect(overflow, 'no horizontal page scroll').toBeLessThanOrEqual(0)
}

/** The element fits the viewport's width and is at least 44px tall. */
async function expectTappable(page: Page, testId: string) {
  const box = await page.getByTestId(testId).boundingBox()
  expect(box, testId).not.toBeNull()
  const width = page.viewportSize()!.width
  expect(box!.height, `${testId} height`).toBeGreaterThanOrEqual(44)
  expect(box!.x).toBeGreaterThanOrEqual(0)
  expect(box!.x + box!.width).toBeLessThanOrEqual(width)
}

/** The element's box lies within the viewport's width. */
async function expectWithinWidth(page: Page, testId: string) {
  const box = await page.getByTestId(testId).boundingBox()
  expect(box, testId).not.toBeNull()
  expect(box!.x).toBeGreaterThanOrEqual(0)
  expect(box!.x + box!.width).toBeLessThanOrEqual(page.viewportSize()!.width)
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
    const { button, posts } = await openLink(page, token, user.email)
    await button.click()
    await expectLoggedIn(page, context)
    await expectOneTokenPost(posts, token)
    expect(loginTokenUsed(token)).toBe(true)
  })

  test('a valid link logs in only on the button, once', async ({ page, context, browser }, testInfo) => {
    const user = member()
    const token = insertLoginToken(user.email)
    const firstIp = clientIp

    const { button, posts } = await openLink(page, token, user.email)
    await button.click()
    await expectLoggedIn(page, context)
    await expectOneTokenPost(posts, token)
    expect(loginTokenUsed(token)).toBe(true)

    // The same link in a fresh browser is spent: the peek says so on load,
    // and the page offers no button.
    const other = await browser.newContext()
    let secondIp = ''
    try {
      secondIp = await isolateRateLimit(other, testInfo.workerIndex)
      const page2 = await other.newPage()
      const posts2 = await verifyPosts(page2)
      await page2.goto(`/login/verify?token=${encodeURIComponent(token)}`)
      await expect(page2.getByTestId('verify-problem-login_token_used')).toBeVisible()
      await expect(page2.getByTestId('verify-request-new')).toBeVisible()
      await expect(page2.getByTestId('verify-button')).toHaveCount(0)
      await expect(page2.getByTestId('verify-account')).toHaveCount(0)
      expect(posts2).toEqual([])
      expect(await sessionCookie(other)).toBeUndefined()
    } finally {
      await other.close()
    }

    // BRY-659: the login and the refused reuse are both audited, each with
    // the client IP the API took from X-Forwarded-For; the refusal came from
    // the peek (BRY-660). A successful peek writes nothing.
    await expect
      .poll(() => authAudit(user.id))
      .toEqual([
        { action: 'auth.login_success', method: 'magic_link', reason: '', stage: '', ip: firstIp },
        { action: 'auth.login_failed', method: 'magic_link', reason: 'used', stage: 'peek', ip: secondIp },
      ])
  })

  test('an expired link shows the expired problem on load and stays unused', async ({ page, context }) => {
    const user = member()
    const token = insertLoginToken(user.email, -1)
    const posts = await verifyPosts(page)
    const peeks = await peekPosts(page)
    await page.goto(`/login/verify?token=${encodeURIComponent(token)}`)
    await expect(page.getByTestId('verify-problem-login_token_expired')).toBeVisible()
    await expect(page.getByTestId('verify-button')).toHaveCount(0)
    await expect(page).toHaveURL(/\/login\/verify$/)
    expect(posts, 'no login attempt').toEqual([])
    expect(peeks).toHaveLength(1)
    expect(await sessionCookie(context)).toBeUndefined()
    expect(loginTokenUsed(token)).toBe(false)
    await expect
      .poll(() => authAudit(user.id))
      .toEqual([{ action: 'auth.login_failed', method: 'magic_link', reason: 'expired', stage: 'peek', ip: clientIp }])
  })

  test('an unknown token shows the invalid problem on load', async ({ page, context }) => {
    const token = 'e2e-not-a-real-token'
    const posts = await verifyPosts(page)
    const peeks = await peekPosts(page)
    await page.goto(`/login/verify?token=${token}`)
    await expect(page.getByTestId('verify-problem-login_token_invalid')).toBeVisible()
    await expect(page).toHaveURL(/\/login\/verify$/)
    await expect(page.getByTestId('verify-button')).toHaveCount(0)
    expect(posts).toEqual([])
    expect(peeks).toHaveLength(1)
    expect(await sessionCookie(context)).toBeUndefined()
  })

  test('the page names the account the link logs in as, with a way out', async ({ page }) => {
    const user = member()
    const token = insertLoginToken(user.email)
    await openLink(page, token, user.email)
    await expect(page.getByTestId('verify-other-user')).toHaveCount(0)
    await page.getByTestId('verify-not-you').click()
    await expect(page).toHaveURL(/\/login$/)
    expect(loginTokenUsed(token)).toBe(false)
  })

  test('a visitor signed in as someone else is warned before the button', async ({ page, context }) => {
    const signedIn = member()
    const { button } = await openLink(page, insertLoginToken(signedIn.email), signedIn.email)
    await button.click()
    await expectLoggedIn(page, context)

    const other = member()
    const token = insertLoginToken(other.email)
    await openLink(page, token, other.email)
    const warning = page.getByTestId('verify-other-user')
    await expect(warning).toBeVisible()
    await expect(warning).toContainText(signedIn.email)
    await expect(warning).toContainText(other.email)
  })

  test('a cross-site POST is refused before the login handler', async ({ page }) => {
    const user = member()
    const token = insertLoginToken(user.email)
    // The same dev server under another loopback host name is another site,
    // as another website's page is. A real server must serve it: Chrome's
    // local network access rules block a page of unknown address (a route
    // fulfilled by Playwright) from posting to localhost at all.
    const crossOrigin = await crossSiteOrigin()
    await page.goto(`${crossOrigin}/`)

    const target = `${BASE_URL}${VERIFY_API}`
    // A "simple" request (no-cors, text/plain): CORS lets it reach the
    // server, as an HTML form would. The response is opaque to the page, so
    // the status is read from the network.
    const post = async () => {
      const answered = page.waitForResponse((res) => res.url() === target && res.request().method() === 'POST')
      await page.evaluate(
        async ({ target, token }) => {
          await fetch(target, {
            method: 'POST',
            mode: 'no-cors',
            credentials: 'include',
            headers: { 'Content-Type': 'text/plain' },
            body: JSON.stringify({ token }),
          })
        },
        { target, token },
      )
      return (await answered).status()
    }

    // From the other site the guard answers 403 before the handler.
    expect(await post(), 'cross-site POST').toBe(403)
    // The same request from the app's own origin reaches the handler, which
    // refuses the text/plain body with 415: the 403 above was the guard.
    await page.goto('/')
    expect(await post(), 'same-origin POST').toBe(415)

    expect(loginTokenUsed(token)).toBe(false)
    expect(authAudit(user.id)).toEqual([])
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
    await expect(page.getByTestId('verify-account')).toHaveText(user.email)
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
    const { button, posts } = await openLink(page, token, applicant.email)
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
    const { button, posts } = await openLink(page, token, user.email)

    await expectTappable(page, 'verify-button')
    await expectTappable(page, 'verify-not-you')
    await expectNoOverflow(page)

    await button.tap()
    await expectLoggedIn(page, context)
    await expectOneTokenPost(posts, token)
  })

  test('a long address wraps instead of widening the page', async ({ page }) => {
    // A long local part and no spaces: only break-all keeps it on screen.
    const user = member(`long-${'adresse'.repeat(6)}`)
    expect(user.email.length).toBeGreaterThan(60)
    const token = insertLoginToken(user.email)
    await openLink(page, token, user.email)

    await expectWithinWidth(page, 'verify-account')
    await expectTappable(page, 'verify-button')
    await expectNoOverflow(page)
  })

  test('the problem state fits too', async ({ page }) => {
    await page.goto('/login/verify')
    await expect(page.getByTestId('verify-request-new')).toBeVisible()
    await expectTappable(page, 'verify-request-new')
    await expectNoOverflow(page)
  })

  test('a refused link shows its problem on load and fits', async ({ page }) => {
    await page.goto('/login/verify?token=e2e-not-a-real-token')
    await expect(page.getByTestId('verify-problem-login_token_invalid')).toBeVisible()
    await expectTappable(page, 'verify-request-new')
    await expectNoOverflow(page)
  })

  test('the join confirm page names the address and fits', async ({ page }) => {
    const applicant = track(joinAddress('join', 40))
    const token = insertJoinApplication(applicant.email)
    await page.goto(`/join/confirm?token=${encodeURIComponent(token)}`)
    await expect(page.getByTestId('confirm-account')).toHaveText(applicant.email)
    await expectWithinWidth(page, 'confirm-account')
    await expectTappable(page, 'confirm-button')
    await expectNoOverflow(page)
  })
})

test.describe('magic-link login on a tablet (768px)', () => {
  test.use({ viewport: { width: 768, height: 1024 }, hasTouch: true })

  test('the verify page and its problem state fit', async ({ page }) => {
    const user = member(`long-${'adresse'.repeat(6)}`)
    const token = insertLoginToken(user.email)
    await openLink(page, token, user.email)
    await expectWithinWidth(page, 'verify-account')
    await expectTappable(page, 'verify-button')
    await expectNoOverflow(page)

    await page.goto('/login/verify')
    await expectTappable(page, 'verify-request-new')
    await expectNoOverflow(page)
  })

  test('the join confirm page names the address and fits', async ({ page }) => {
    const applicant = track(joinAddress('join', 40))
    const token = insertJoinApplication(applicant.email)
    await page.goto(`/join/confirm?token=${encodeURIComponent(token)}`)
    await expect(page.getByTestId('confirm-account')).toHaveText(applicant.email)
    await expectWithinWidth(page, 'confirm-account')
    await expectTappable(page, 'confirm-button')
    await expectNoOverflow(page)
  })
})
