import { describe, it, expect } from 'vitest'
import de from '@/locales/de.json'
import en from '@/locales/en.json'
import fr from '@/locales/fr.json'
import it_ from '@/locales/it.json'
import nb from '@/locales/nb.json'
import nl from '@/locales/nl.json'
import nn from '@/locales/nn.json'
import pl from '@/locales/pl.json'

// The "Automatisk" bilag badge (BRY-569) reads two strings; every locale needs
// both, or the badge shows a raw key.
const LOCALES: Record<string, { admin: { accounting: { journal: Record<string, unknown> } } }> = {
  de, en, fr, it: it_, nb, nl, nn, pl,
}

describe('automatic bilag badge locale keys', () => {
  for (const [locale, messages] of Object.entries(LOCALES)) {
    it(`${locale} has the label and the explanation`, () => {
      const { automatic, automaticTooltip } = messages.admin.accounting.journal
      expect(typeof automatic === 'string' && automatic !== '').toBe(true)
      expect(typeof automaticTooltip === 'string' && automaticTooltip !== '').toBe(true)
    })
  }

  it('Norwegian reads Automatisk and Bokført automatisk av systemet', () => {
    expect(nb.admin.accounting.journal.automatic).toBe('Automatisk')
    expect(nb.admin.accounting.journal.automaticTooltip).toBe('Bokført automatisk av systemet')
  })
})
