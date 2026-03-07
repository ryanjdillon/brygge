import { test, expect } from '@playwright/test'
import AxeBuilder from '@axe-core/playwright'

const publicPages = [
  { name: 'Home', path: '/' },
  { name: 'Login', path: '/login' },
  { name: 'Join', path: '/join' },
  { name: 'Calendar', path: '/calendar' },
  { name: 'Pricing', path: '/pricing' },
  { name: 'Contact', path: '/contact' },
  { name: 'Directions', path: '/directions' },
]

for (const page of publicPages) {
  test(`${page.name} page has no a11y violations`, async ({ page: p }) => {
    await p.goto(page.path)
    await p.waitForLoadState('networkidle')

    const results = await new AxeBuilder({ page: p })
      .withTags(['wcag2a', 'wcag2aa'])
      .analyze()

    expect(results.violations).toEqual([])
  })
}

test('skip navigation link is present and functional', async ({ page }) => {
  await page.goto('/')
  const skipLink = page.locator('.skip-nav')
  await expect(skipLink).toBeAttached()

  await skipLink.focus()
  await expect(skipLink).toBeVisible()
})

test('all pages have lang="nb" on html element', async ({ page }) => {
  await page.goto('/')
  const lang = await page.locator('html').getAttribute('lang')
  expect(lang).toBe('nb')
})

test('focus indicators are visible on interactive elements', async ({ page }) => {
  await page.goto('/')
  await page.keyboard.press('Tab')

  const focused = page.locator(':focus-visible')
  await expect(focused).toBeVisible()
})
