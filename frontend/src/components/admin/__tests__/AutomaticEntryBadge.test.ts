import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import AutomaticEntryBadge from '@/components/admin/AutomaticEntryBadge.vue'

// The i18n mock in src/test/setup.ts returns keys, so text shows as its key.
describe('AutomaticEntryBadge', () => {
  const badge = () => mount(AutomaticEntryBadge).get('[data-testid="automatic-entry-badge"]')

  it('shows the label as visible text, so touch users do not need hover', () => {
    expect(badge().get('[aria-hidden="true"]').text()).toBe('admin.accounting.journal.automatic')
  })

  it('explains itself in the title and to screen readers', () => {
    expect(badge().attributes('title')).toBe('admin.accounting.journal.automaticTooltip')
    expect(badge().get('.sr-only').text()).toBe('admin.accounting.journal.automaticTooltip')
  })

  it('stays on one line beside a wrapping description', () => {
    expect(badge().classes()).toEqual(expect.arrayContaining(['whitespace-nowrap', 'shrink-0']))
  })
})
