import { test, expect } from '@playwright/test'

test.describe('Directions / Sjokart page', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/directions')
  })

  test('renders page heading', async ({ page }) => {
    await expect(page.locator('h1')).toBeVisible()
  })

  test('renders land and sea section headings', async ({ page }) => {
    const h2s = page.locator('h2')
    await expect(h2s).toHaveCount(2)
  })

  test('renders GPX download link', async ({ page }) => {
    const gpxLink = page.locator('a[href="/api/v1/map/export/gpx"]')
    await expect(gpxLink).toBeVisible()
  })

  test('renders VHF channel info', async ({ page }) => {
    await expect(page.getByText('Ch 16 / Ch 73')).toBeVisible()
  })

  test('renders Google Maps navigation link', async ({ page }) => {
    const mapsLink = page.locator('a[href*="google.com/maps/dir"]')
    await expect(mapsLink).toHaveCount(1)
    await expect(mapsLink).toHaveAttribute('target', '_blank')
  })

  test('sea chart and land map containers are rendered', async ({ page }) => {
    // MapLibre renders canvas elements inside the map containers
    // Even if tile loading fails, the containers should be present
    const sections = page.locator('section')
    await expect(sections).toHaveCount(2)
  })

  test('aria-hidden on decorative icons', async ({ page }) => {
    const decorativeIcons = page.locator('h2 svg[aria-hidden="true"], h2 [aria-hidden="true"]')
    expect(await decorativeIcons.count()).toBeGreaterThanOrEqual(2)
  })
})
