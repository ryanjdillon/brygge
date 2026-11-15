import { describe, it, expect, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import de from '@/locales/de.json'
import en from '@/locales/en.json'
import fr from '@/locales/fr.json'
import it_ from '@/locales/it.json'
import nb from '@/locales/nb.json'
import nl from '@/locales/nl.json'
import nn from '@/locales/nn.json'
import pl from '@/locales/pl.json'

// Plural selection is vue-i18n's own behaviour; the global setup stubs it.
vi.unmock('vue-i18n')

// BRY-682: the "Send testvarsel" button and the iPhone Home Screen help on
// the member notifications page.
const PLAIN = [
  'iosTitle', 'iosIntro', 'iosStep1', 'iosStep2', 'iosStep3', 'iosStep4', 'iosLoginNote', 'iosVersion',
  'iosNeedsHomeScreen', 'pushDenied', 'pushSubscribeFailed', 'pushUnsubscribeFailed',
  'test.button', 'test.hint', 'test.noSubscription', 'test.notConfigured', 'test.rateLimited', 'test.error',
].map((k) => `notifications.${k}`)

const COUNTED: Record<string, string[]> = {
  'notifications.test.removed': ['{count}'],
  'notifications.test.failed': ['{count}'],
}

const SENT = 'notifications.test.sent'

const LOCALES = { de, en, fr, it: it_, nb, nl, nn, pl }

function lookup(messages: unknown, key: string): unknown {
  return key.split('.').reduce<unknown>((node, part) => (node as Record<string, unknown> | undefined)?.[part], messages)
}

const i18n = createI18n({ legacy: false, locale: 'en', fallbackLocale: 'en', messages: LOCALES })

describe('test push and iPhone help locale keys', () => {
  for (const [locale, messages] of Object.entries(LOCALES)) {
    for (const key of PLAIN) {
      it(`${locale} has ${key}`, () => {
        const value = lookup(messages, key)
        expect(typeof value).toBe('string')
        expect((value as string).trim()).not.toBe('')
        expect(value as string).not.toMatch(/[{}|@$]/)
      })
    }
    for (const [key, params] of Object.entries(COUNTED)) {
      it(`${locale} has ${key} with its placeholder`, () => {
        const value = lookup(messages, key) as string
        expect(typeof value).toBe('string')
        for (const p of params) expect(value).toContain(p)
        expect(value).not.toMatch(/[|@$]/)
      })
    }
    it(`${locale} has a singular and a plural form of ${SENT}`, () => {
      const forms = (lookup(messages, SENT) as string).split('|').map((s) => s.trim())
      expect(forms).toHaveLength(2)
      for (const f of forms) {
        expect(f).toContain('{sent}')
        expect(f).toContain('{total}')
      }
      i18n.global.locale.value = locale as keyof typeof LOCALES
      const one = i18n.global.t(SENT, { sent: 1, total: 1 }, 1)
      const two = i18n.global.t(SENT, { sent: 2, total: 2 }, 2)
      expect(one).toBe(forms[0]!.replace('{sent}', '1').replace('{total}', '1'))
      expect(two).toBe(forms[1]!.replace('{sent}', '2').replace('{total}', '2'))
    })
  }

  it('nb and nn name the iOS menu item as the phone shows it', () => {
    expect(lookup(nb, 'notifications.iosStep2')).toContain('Legg til på Hjem-skjerm')
    expect(lookup(nn, 'notifications.iosStep2')).toContain('Legg til på Hjem-skjerm')
    expect(lookup(nb, 'notifications.test.button')).toBe('Send testvarsel')
  })

  it('nb and nn tell the member to sign in to the Home Screen app with a passkey', () => {
    expect(lookup(nb, 'notifications.iosStep3')).toContain('passnøkkel')
    expect(lookup(nn, 'notifications.iosStep3')).toContain('logg inn med passnøkkel')
  })

  it('nn singular and plural read eining/einingar', () => {
    i18n.global.locale.value = 'nn'
    expect(i18n.global.t(SENT, { sent: 1, total: 1 }, 1)).toBe('Testvarsel sendt til 1 av 1 eining.')
    expect(i18n.global.t(SENT, { sent: 1, total: 3 }, 3)).toBe('Testvarsel sendt til 1 av 3 einingar.')
  })
})
