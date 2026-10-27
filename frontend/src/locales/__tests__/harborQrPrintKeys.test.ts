import { describe, it, expect } from 'vitest'
import de from '@/locales/de.json'
import en from '@/locales/en.json'
import fr from '@/locales/fr.json'
import it_ from '@/locales/it.json'
import nb from '@/locales/nb.json'
import nl from '@/locales/nl.json'
import nn from '@/locales/nn.json'
import pl from '@/locales/pl.json'

// The QR print sheet (BRY-451) prints in every selected language, so each
// locale needs its own sign strings; and a printed sign outlives a change of
// payment provider, so none of them may name one.
const KEYS = ['style', 'styleStandard', 'styleHighVis', 'payHere', 'scanHint']

const LOCALES: Record<string, { harborTargets: { print: Record<string, unknown> } }> = {
  de,
  en,
  fr,
  it: it_,
  nb,
  nl,
  nn,
  pl,
}

describe('harbor QR print locale keys', () => {
  for (const [locale, messages] of Object.entries(LOCALES)) {
    const print = messages.harborTargets.print

    it(`${locale} has every print sign string`, () => {
      const missing = KEYS.filter((k) => typeof print[k] !== 'string' || print[k] === '')
      expect(missing).toEqual([])
    })

    it(`${locale} names no payment provider in the print strings`, () => {
      const named = Object.entries(print).filter(([, v]) => typeof v === 'string' && /vipps/i.test(v))
      expect(named).toEqual([])
    })
  }
})
