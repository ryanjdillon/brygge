import { describe, it, expect } from 'vitest'
import de from '@/locales/de.json'
import en from '@/locales/en.json'
import fr from '@/locales/fr.json'
import it_ from '@/locales/it.json'
import nb from '@/locales/nb.json'
import nl from '@/locales/nl.json'
import nn from '@/locales/nn.json'
import pl from '@/locales/pl.json'

// The receipt page is where every payment provider returns the payer, guests
// included, so each locale carries its own strings and none names a provider.
const KEYS = [
  'loading',
  'paidTitle',
  'paidBody',
  'amountPaid',
  'invoice',
  'pendingTitle',
  'pendingBody',
  'confirmingTitle',
  'confirmingBody',
  'failedTitle',
  'failedBody',
  'tryAgain',
  'notFoundTitle',
  'notFoundBody',
]

const LOCALES: Record<string, { pay: { receipt: Record<string, unknown> } }> = {
  de,
  en,
  fr,
  it: it_,
  nb,
  nl,
  nn,
  pl,
}

describe('payment receipt locale keys', () => {
  for (const [locale, messages] of Object.entries(LOCALES)) {
    const receipt = messages.pay.receipt

    it(`${locale} has every receipt string`, () => {
      const missing = KEYS.filter((k) => typeof receipt[k] !== 'string' || receipt[k] === '')
      expect(missing).toEqual([])
    })

    it(`${locale} names no payment provider`, () => {
      const named = Object.entries(receipt).filter(([, v]) => typeof v === 'string' && /vipps|stripe/i.test(v))
      expect(named).toEqual([])
    })
  }
})
