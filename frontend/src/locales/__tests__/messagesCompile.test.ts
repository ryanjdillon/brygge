import { describe, it, expect } from 'vitest'
// vue-i18n's own compiler (a dependency of vue-i18n, same version). t() in
// tests does not surface compile errors, so the compiler is called directly.
import { baseCompile } from '@intlify/message-compiler'
import de from '@/locales/de.json'
import en from '@/locales/en.json'
import fr from '@/locales/fr.json'
import it_ from '@/locales/it.json'
import nb from '@/locales/nb.json'
import nl from '@/locales/nl.json'
import nn from '@/locales/nn.json'
import pl from '@/locales/pl.json'

// vue-i18n compiles a message the first time it renders, so a bare `@`,
// `{` or `|` in a string only throws when that page opens ("SyntaxError:
// 10" took down Nettstedinnstillinger over a hint reading "(post@)").
// Compile every string here instead. A literal @ is written {'@'}.
const LOCALES: Record<string, Record<string, unknown>> = { de, en, fr, it: it_, nb, nl, nn, pl }

function entries(node: unknown, prefix = ''): [string, string][] {
  if (typeof node === 'string') return [[prefix, node]]
  if (typeof node !== 'object' || node === null) return []
  return Object.entries(node).flatMap(([k, v]) => entries(v, prefix ? `${prefix}.${k}` : k))
}

describe('locale messages compile', () => {
  it.each(Object.keys(LOCALES))('%s', (loc) => {
    const broken: string[] = []
    for (const [key, message] of entries(LOCALES[loc])) {
      baseCompile(message, {
        onError: (e) => broken.push(`${key}: ${e.message}`),
      })
    }
    expect(broken).toEqual([])
  })
})
