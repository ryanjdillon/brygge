import { describe, it, expect } from 'vitest'
import de from '@/locales/de.json'
import en from '@/locales/en.json'
import fr from '@/locales/fr.json'
import it_ from '@/locales/it.json'
import nb from '@/locales/nb.json'
import nl from '@/locales/nl.json'
import nn from '@/locales/nn.json'
import pl from '@/locales/pl.json'
import { REFUND_ERROR_CODES } from '@/composables/useOrderRefund'

// The refund dialog (BRY-497) reads admin.refund.*; every locale has every
// string, including a message for each refusal code the server can answer.
const LOCALES: Record<string, { admin: { refund?: Record<string, unknown> } }> = {
  de, en, fr, it: it_, nb, nl, nn, pl,
}

function leaves(node: unknown, prefix = ''): string[] {
  if (node === null || typeof node !== 'object') return [prefix]
  return Object.entries(node as Record<string, unknown>).flatMap(([k, v]) => leaves(v, prefix ? `${prefix}.${k}` : k))
}

describe('refund dialog locale keys', () => {
  const reference = leaves(nb.admin.refund).sort()

  it('has the refusal messages', () => {
    for (const code of [...REFUND_ERROR_CODES, 'invalid', 'not_found', 'generic']) {
      expect(reference, code).toContain(`errors.${code}`)
    }
  })

  for (const [locale, messages] of Object.entries(LOCALES)) {
    it(`${locale} has the same strings as nb, none empty`, () => {
      expect(leaves(messages.admin.refund).sort()).toEqual(reference)
      const strings = JSON.stringify(messages.admin.refund)
      expect(strings).not.toContain('""')
    })
  }
})
