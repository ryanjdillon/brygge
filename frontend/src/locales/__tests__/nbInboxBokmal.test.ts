import { describe, it, expect } from 'vitest'
import nb from '@/locales/nb.json'

// nb.json is Bokmål; the Nynorsk forms belong in nn.json. These markers are
// unambiguous Nynorsk, so matching them as whole words has no Bokmål false
// positives. "vel" is only a marker as a leading imperative ("Vel ei melding"),
// because it is legitimate Bokmål mid-sentence ("så vel som").
const NYNORSK_WORDS = [
  'ikkje',
  'nokon',
  'noko',
  'meldingar',
  'bilete',
  'utsendingar',
  'sjå',
  'korleis',
  'kva',
  'frå',
  'mottakar',
  'ventar',
  'feila',
  'éin',
]
const NYNORSK_WORD = new RegExp(`(?<![\\p{L}])(${NYNORSK_WORDS.join('|')})(?![\\p{L}])`, 'iu')
const NYNORSK_LEADING_VEL = /^vel(?![\p{L}])/iu

function collect(node: unknown, path: string, out: [string, string][]): void {
  if (typeof node === 'string') {
    out.push([path, node])
  } else if (node && typeof node === 'object') {
    for (const [key, value] of Object.entries(node)) collect(value, `${path}.${key}`, out)
  }
}

describe('nb.json admin.inbox is Bokmål', () => {
  const strings: [string, string][] = []
  collect(nb.admin.inbox, 'admin.inbox', strings)

  it('has strings to check', () => {
    expect(strings.length).toBeGreaterThan(30)
  })

  it('contains no Nynorsk marker words', () => {
    const offenders = strings
      .filter(([, text]) => NYNORSK_WORD.test(text) || NYNORSK_LEADING_VEL.test(text))
      .map(([path, text]) => `${path}: ${text}`)
    expect(offenders).toEqual([])
  })
})
