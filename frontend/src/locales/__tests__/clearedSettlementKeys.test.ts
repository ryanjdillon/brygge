import { describe, it, expect } from 'vitest'
import de from '@/locales/de.json'
import en from '@/locales/en.json'
import fr from '@/locales/fr.json'
import it_ from '@/locales/it.json'
import nb from '@/locales/nb.json'
import nl from '@/locales/nl.json'
import nn from '@/locales/nn.json'
import pl from '@/locales/pl.json'

// The refusal to undo a charge in a cleared Vipps settlement (BRY-555) offers
// an undo of the clearing through a confirmation, so every string of that
// flow must exist in every locale, with the placeholders the component fills.
const KEYS = [
  'refused', 'undoButton', 'undoing', 'confirmTitle', 'confirmBody',
  'detailPayout', 'detailDeposit', 'detailFee', 'noDeposit',
].map((k) => `admin.accounting.clearedSettlement.${k}`)

const LOCALES: Record<string, unknown> = { de, en, fr, it: it_, nb, nl, nn, pl }

function lookup(messages: unknown, key: string): unknown {
  return key.split('.').reduce<unknown>((node, part) => (node as Record<string, unknown> | undefined)?.[part], messages)
}

describe('cleared-settlement locale keys', () => {
  for (const [locale, messages] of Object.entries(LOCALES)) {
    it(`${locale} has every string, with its placeholders`, () => {
      const missing = KEYS.filter((k) => typeof lookup(messages, k) !== 'string' || lookup(messages, k) === '')
      expect(missing).toEqual([])
      const text = (k: string) => lookup(messages, `admin.accounting.clearedSettlement.${k}`) as string
      expect(text('refused')).toContain('{settlement}')
      expect(text('refused')).toContain('{msn}')
      expect(text('confirmTitle')).toContain('{settlement}')
      expect(text('detailDeposit')).toContain('{settlement}')
      expect(text('detailDeposit')).toContain('{msn}')
    })
  }
})
