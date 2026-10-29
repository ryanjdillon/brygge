import { describe, it, expect } from 'vitest'
import de from '@/locales/de.json'
import en from '@/locales/en.json'
import fr from '@/locales/fr.json'
import it_ from '@/locales/it.json'
import nb from '@/locales/nb.json'
import nl from '@/locales/nl.json'
import nn from '@/locales/nn.json'
import pl from '@/locales/pl.json'

// The review reasons and refusal codes card refund booking (BRY-446) adds to
// the Stripe transaction list exist in every locale. The faktura list's
// credit states still fall back to English in some locales, so those are
// pinned only where the section exists.
const KEYS = [
  ...[
    'refund_unlinked',
    'refund_charge_unbooked',
    'refund_origin_dismissed',
    'refund_legacy',
    'refund_failure_settled',
    'refund_split',
  ].map((k) => `admin.bankImports.stripeReviewReasons.${k}`),
  ...['refund_booked', 'credit_note_issued', 'faktura_settled_since'].map(
    (k) => `admin.bankImports.stripeTxn.errors.${k}`,
  ),
]
const STATE_KEYS = ['refund', 'refund_order'].map((k) => `admin.faktura.credit.state.${k}`)

const LOCALES: Record<string, unknown> = { de, en, fr, it: it_, nb, nl, nn, pl }

function lookup(messages: unknown, key: string): unknown {
  return key.split('.').reduce<unknown>((node, part) => (node as Record<string, unknown> | undefined)?.[part], messages)
}

describe('Stripe refund locale keys', () => {
  for (const [locale, messages] of Object.entries(LOCALES)) {
    it(`${locale} has every refund string`, () => {
      const missing = KEYS.filter((k) => typeof lookup(messages, k) !== 'string' || lookup(messages, k) === '')
      expect(missing).toEqual([])
      expect(lookup(messages, 'admin.bankImports.stripeBookCounts')).toContain('{refunds}')
      if (lookup(messages, 'admin.faktura.credit.state') !== undefined) {
        expect(STATE_KEYS.filter((k) => typeof lookup(messages, k) !== 'string')).toEqual([])
      }
    })
  }
})
