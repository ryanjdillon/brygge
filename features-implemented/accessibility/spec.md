# Accessibility — WCAG 2.1 AA Compliance

## Overview

Ensure the app meets WCAG 2.1 Level AA standards, with particular attention to
elderly users, color-blind users, and text readability. Integrate automated checks
into CI to prevent regression.

## Target Audience Considerations

- **Elderly users**: larger text, clear focus indicators, simple navigation, generous tap targets
- **Color-blind users**: never encode information with color alone — always pair with
  icons, text labels, or patterns
- **Low vision**: sufficient contrast ratios, functional at 200% browser zoom

## Key WCAG 2.1 AA Requirements

### Perceivable
- **1.1.1** Non-text content: all images have alt text, decorative images have `alt=""`
- **1.3.1** Info and relationships: proper heading hierarchy (h1 → h2 → h3), form labels,
  table headers, ARIA landmarks
- **1.4.1** Use of color: status badges (confirmed/pending) use icons + text, not just
  green/yellow. Chart data uses patterns in addition to color
- **1.4.3** Contrast minimum: 4.5:1 for normal text, 3:1 for large text (18px+ bold or 24px+)
- **1.4.4** Resize text: app remains usable at 200% zoom without horizontal scrolling
- **1.4.11** Non-text contrast: UI components and borders have 3:1 contrast against background

### Operable
- **2.1.1** Keyboard accessible: all interactive elements reachable via Tab, activatable via Enter/Space
- **2.1.2** No keyboard trap: focus can always move away from any component
- **2.4.1** Skip navigation: "Skip to main content" link at top of page
- **2.4.3** Focus order: logical tab order following visual layout
- **2.4.7** Focus visible: clear, high-contrast focus ring on all interactive elements
- **2.5.5** Target size: interactive targets at least 44x44 CSS pixels (important for elderly users)

### Understandable
- **3.1.1** Language of page: `<html lang="nb">` (already set)
- **3.2.2** On input: no unexpected context changes on form input
- **3.3.1** Error identification: form errors clearly described in text (not just red border)
- **3.3.2** Labels or instructions: all form fields have visible labels

### Robust
- **4.1.2** Name, role, value: custom components have appropriate ARIA attributes

## Color-Blind Safe Palette

Review and adjust the current Tailwind color usage:

| Purpose          | Current         | Accessible alternative              |
|------------------|-----------------|-------------------------------------|
| Success/confirmed| green-100/800   | green + ShieldCheck icon + "Godkjent" text |
| Warning/pending  | yellow-100/800  | yellow + AlertTriangle icon + text  |
| Error            | red-50/800      | red + XCircle icon + text           |
| Info             | blue-50/700     | blue + Info icon + text             |
| Local badge      | green-100/800   | green + MapPin icon + "Lokal" text  |
| Non-local badge  | yellow-100/800  | yellow + MapPin icon + "Ikke-lokal" text |

Most of these already pair color with icons/text (good). Audit for any remaining
color-only indicators.

## Implementation

### 1. Automated CI Checks

Add axe-core to the test pipeline:

```bash
# In CI (vitest + axe-core for component tests)
npm install -D vitest-axe axe-core

# In Playwright E2E tests
npm install -D @axe-core/playwright
```

**Component tests**: wrap render calls with `expect(await axe(container)).toHaveNoViolations()`

**E2E tests**: add accessibility scan to key pages:
```ts
import AxeBuilder from '@axe-core/playwright'

test('home page has no a11y violations', async ({ page }) => {
  await page.goto('/')
  const results = await new AxeBuilder({ page }).analyze()
  expect(results.violations).toEqual([])
})
```

Pages to scan in E2E:
- Home, Login, Join
- Portal: dashboard, boats, waiting list, profile
- Admin: users, boats, pricing, waiting list

### 2. CSS / Tailwind Adjustments

- Base font size: ensure `16px` minimum (Tailwind default is fine)
- Add `focus-visible:ring-2 focus-visible:ring-blue-500 focus-visible:ring-offset-2`
  to all interactive elements via Tailwind plugin or base styles
- Minimum button/link tap target: `min-h-[44px] min-w-[44px]`
- Skip nav link: hidden until focused, positioned at top of page

### 3. Semantic HTML Audit

- Ensure all pages use `<main>`, `<nav>`, `<header>`, `<footer>`, `<aside>` landmarks
- Heading hierarchy: one `<h1>` per page, logical nesting
- Form inputs: all have associated `<label>` elements (not just placeholder text)
- Tables: use `<th scope="col">` for column headers
- Buttons vs links: `<button>` for actions, `<a>` for navigation

### 4. Screen Reader Testing

Manual testing with:
- VoiceOver (macOS/iOS) — primary, matches elderly iOS user demographic
- NVDA (Windows) — secondary

Key flows to test:
- Login → dashboard → boats → add boat → search model → save
- Waiting list view
- Admin confirmation queue

## Acceptance Criteria

- Zero axe-core violations on all scanned pages in CI
- All form fields have visible labels
- All status indicators use icon + text (not color alone)
- Keyboard navigation works for full login → portal → admin flow
- App is usable at 200% browser zoom without horizontal scroll
- Focus indicators visible on all interactive elements
- Tap targets minimum 44x44px
