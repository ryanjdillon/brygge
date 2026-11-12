import { describe, it, expect } from 'vitest'
import de from '@/locales/de.json'
import en from '@/locales/en.json'
import fr from '@/locales/fr.json'
import it_ from '@/locales/it.json'
import nb from '@/locales/nb.json'
import nl from '@/locales/nl.json'
import nn from '@/locales/nn.json'
import pl from '@/locales/pl.json'
import { TRANSFER_REFUSAL_CODES } from '@/composables/useBankReconcile'

// BRY-672: Tildel's «Par som overføring» and the toast for every code the
// server refuses a pair with.
const PLAIN = [
  'admin.bankReconcile.clearSelection',
  'admin.bankReconcile.transfer.button',
  'admin.bankReconcile.transfer.confirmTitle',
  'admin.bankReconcile.transfer.success',
  ...TRANSFER_REFUSAL_CODES.map((c) => `admin.bankReconcile.transfer.refused.${c}`),
]
const BODY = 'admin.bankReconcile.transfer.confirmBody'

const LOCALES: Record<string, unknown> = { de, en, fr, it: it_, nb, nl, nn, pl }

function lookup(messages: unknown, key: string): unknown {
  return key.split('.').reduce<unknown>((node, part) => (node as Record<string, unknown> | undefined)?.[part], messages)
}

describe('transfer pair locale keys', () => {
  for (const [locale, messages] of Object.entries(LOCALES)) {
    for (const key of PLAIN) {
      it(`${locale} has ${key}`, () => {
        const value = lookup(messages, key)
        expect(typeof value).toBe('string')
        expect((value as string).trim()).not.toBe('')
        expect(value as string).not.toMatch(/[{}|@$]/)
      })
    }
    it(`${locale} has ${BODY} with every placeholder`, () => {
      const value = lookup(messages, BODY) as string
      expect(typeof value).toBe('string')
      for (const p of ['{amount}', '{credit}', '{debit}', '{date}']) expect(value).toContain(p)
      expect(value).not.toMatch(/[|@$]/)
    })
  }
})
