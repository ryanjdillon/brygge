import { createI18n } from 'vue-i18n'
import nb from '@/locales/nb.json'
import nn from '@/locales/nn.json'
import en from '@/locales/en.json'
import de from '@/locales/de.json'
import fr from '@/locales/fr.json'
import nl from '@/locales/nl.json'
import it from '@/locales/it.json'
import pl from '@/locales/pl.json'

// Keep aligned with backend supportedUILocales and the clubs/users
// language CHECK constraints (migration 000047).
export const SUPPORTED_LOCALES = ['nb', 'nn', 'en', 'de', 'fr', 'nl', 'it', 'pl'] as const
export type Locale = (typeof SUPPORTED_LOCALES)[number]

// Endonym labels for the locale pickers (switcher, profile, club settings).
export const LOCALE_OPTIONS: { code: Locale; label: string }[] = [
  { code: 'nb', label: 'Norsk (bokmål)' },
  { code: 'nn', label: 'Norsk (nynorsk)' },
  { code: 'en', label: 'English' },
  { code: 'de', label: 'Deutsch' },
  { code: 'fr', label: 'Français' },
  { code: 'nl', label: 'Nederlands' },
  { code: 'it', label: 'Italiano' },
  { code: 'pl', label: 'Polski' },
]

const LOCALE_KEY = 'brygge-locale'

export function isSupportedLocale(v: unknown): v is Locale {
  return typeof v === 'string' && (SUPPORTED_LOCALES as readonly string[]).includes(v)
}

const saved = localStorage.getItem(LOCALE_KEY)
// Platform default is Bokmål; a club can move members to another
// language via club default / member preference (resolved post-login).
const initialLocale: Locale = isSupportedLocale(saved) ? saved : 'nb'

export const i18n = createI18n({
  legacy: false,
  locale: initialLocale,
  fallbackLocale: 'en',
  messages: { nb, nn, en, de, fr, nl, it, pl },
})

/**
 * setLocale switches the active UI language.
 *
 * persist=true is an explicit user choice (profile dropdown, header
 * switcher): it writes localStorage so the choice survives reloads and
 * logout. persist=false is used when applying the club default for a
 * member with no explicit preference — not stored, so they keep
 * tracking the club default if it later changes.
 */
export function setLocale(code: string, opts: { persist?: boolean } = {}) {
  if (!isSupportedLocale(code)) return
  i18n.global.locale.value = code
  if (opts.persist) localStorage.setItem(LOCALE_KEY, code)
}

export function hasExplicitLocale(): boolean {
  return isSupportedLocale(localStorage.getItem(LOCALE_KEY))
}
