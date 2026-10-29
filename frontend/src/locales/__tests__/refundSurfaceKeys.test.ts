import { describe, it, expect } from 'vitest'
import de from '@/locales/de.json'
import en from '@/locales/en.json'
import fr from '@/locales/fr.json'
import it_ from '@/locales/it.json'
import nb from '@/locales/nb.json'
import nl from '@/locales/nl.json'
import nn from '@/locales/nn.json'
import pl from '@/locales/pl.json'
import { REISSUE_ERROR_CODES } from '@/composables/useReissueAfterRefund'

// Card refunds in the lists (BRY-498): the chips and "Ny faktura" read
// admin.refund.chip / .reissue (parity with the rest of admin.refund is held
// by orderRefundKeys), and the member portal reads these portal.invoices keys.
// Every locale has every one, none empty, with the same placeholders.
type Tree = Record<string, unknown>
const LOCALES: Record<string, Tree> = { de, en, fr, it: it_, nb, nl, nn, pl } as never

const PORTAL_KEYS = [
  'status.refunded',
  'status.refund_credit_note',
  'refundCreditNote',
  'refundedFull',
  'refundedPartly',
  'creditNoteNumber',
]
const ADMIN_KEYS = [
  'chip.refunded',
  'chip.partial',
  'chip.partialAmount',
  'chip.refundedTitle',
  'chip.disputed',
  'chip.disputedDue',
  'chip.disputeLost',
  'chip.disputeWon',
  'chip.openStripe',
  'chip.disputesWaiting',
  'chip.answerInStripe',
  'creditNote',
  'reissue.button',
  'reissue.buttonAria',
  'reissue.confirmTitle',
  'reissue.confirmBody',
  'reissue.confirmAction',
  'reissue.done',
  'reissue.doneUnsent',
  'reissue.doneWarning',
  'reissue.doneNoClub',
  'reissue.reissuedAs',
  'reissue.reissueOf',
  ...[...REISSUE_ERROR_CODES, 'not_found', 'generic'].map((c) => `reissue.errors.${c}`),
]

function lookup(tree: Tree, path: string): string | undefined {
  let node: unknown = tree
  for (const part of path.split('.')) node = (node as Tree | undefined)?.[part]
  return typeof node === 'string' ? node : undefined
}

const placeholders = (s: string) => (s.match(/\{\w+\}/g) ?? []).sort()

describe.each([
  ['admin.refund', (m: Tree) => (m.admin as Tree).refund as Tree, ADMIN_KEYS],
  ['portal.invoices', (m: Tree) => (m.portal as Tree).invoices as Tree, PORTAL_KEYS],
])('%s refund strings', (_name, pick, keys) => {
  for (const [locale, messages] of Object.entries(LOCALES)) {
    it(`${locale} has every key with the placeholders of nb`, () => {
      for (const key of keys) {
        const value = lookup(pick(messages), key)
        expect(value, `${locale} ${key}`).toBeTruthy()
        expect(placeholders(value!), `${locale} ${key}`).toEqual(placeholders(lookup(pick(nb as Tree), key)!))
      }
    })
  }
})
