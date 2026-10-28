import { describe, it, expect } from 'vitest'
import de from '@/locales/de.json'
import en from '@/locales/en.json'
import fr from '@/locales/fr.json'
import it_ from '@/locales/it.json'
import nb from '@/locales/nb.json'
import nl from '@/locales/nl.json'
import nn from '@/locales/nn.json'
import pl from '@/locales/pl.json'

type Messages = Record<string, unknown>

function get(messages: Messages, path: string): unknown {
  return path.split('.').reduce<unknown>((node, key) => (node as Messages | undefined)?.[key], messages)
}

const ALL: Record<string, Messages> = { de, en, fr, it: it_, nb, nl, nn, pl }
const FULL: Record<string, Messages> = { en, nb, nn }

// The pay buttons are shared by the faktura page and the harbor QR page, which
// guests read in their own language, so every locale carries them.
const METHOD_KEYS = ['pay.methods.vipps', 'pay.methods.card', 'pay.methods.chooseTitle', 'pay.processing']

// Payer-facing texts that apply whichever provider takes the payment.
const NEUTRAL_KEYS = [
  'pay.methods.card',
  'pay.methods.chooseTitle',
  'pay.processing',
  'portal.payables.pay',
  'portal.payables.bankTransferHint',
]
const NEUTRAL_FULL_KEYS = [
  'pay.bankOnlyBody',
  'pay.bankOnlyBodyNoProvider',
  'qr.payError',
  'qr.unavailable',
  'admin.payLink.title',
  'admin.payLink.unavailable',
]

describe('payment method locale keys', () => {
  for (const [locale, messages] of Object.entries(ALL)) {
    it(`${locale} has every pay-method string`, () => {
      const missing = METHOD_KEYS.filter((k) => typeof get(messages, k) !== 'string' || get(messages, k) === '')
      expect(missing).toEqual([])
    })

    it(`${locale} keeps neutral texts free of provider names`, () => {
      const keys = locale in FULL ? [...NEUTRAL_KEYS, ...NEUTRAL_FULL_KEYS] : NEUTRAL_KEYS
      const named = keys.filter((k) => /vipps|stripe/i.test(String(get(messages, k) ?? '')))
      expect(named).toEqual([])
      if (locale in FULL) {
        const missing = NEUTRAL_FULL_KEYS.filter((k) => typeof get(messages, k) !== 'string' || get(messages, k) === '')
        expect(missing).toEqual([])
      }
    })

    it(`${locale} has no dead Vipps-only pay keys`, () => {
      expect(get(messages, 'join.payWithVipps')).toBeUndefined()
      expect(get(messages, 'merchandise.payWithVipps')).toBeUndefined()
      expect(get(messages, 'admin.economySettings.vippsMaxAmount')).toBeUndefined()
      expect(get(messages, 'pay.payButton')).toBeUndefined()
    })
  }
})
