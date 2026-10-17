import { describe, it, expect } from 'vitest'
import de from '@/locales/de.json'
import en from '@/locales/en.json'
import fr from '@/locales/fr.json'
import it_ from '@/locales/it.json'
import nb from '@/locales/nb.json'
import nl from '@/locales/nl.json'
import nn from '@/locales/nn.json'
import pl from '@/locales/pl.json'

// Role exemption strings (BRY-351) exist in every locale. Older sections of
// some locales still fall back to English, so parity is pinned per feature
// rather than across whole files.
const KEYS = [
  'portal.invoices.status.nothing_due',
  'admin.economySettings.exemptions.label',
  'admin.economySettings.exemptions.hint',
  'admin.economySettings.exemptions.add',
  'admin.economySettings.exemptions.categories',
  'admin.economySettings.exemptions.items',
  'admin.economySettings.exemptions.expenseAccount',
  'admin.economySettings.exemptions.accountHint',
  'admin.economySettings.exemptions.accountRequired',
  'bulkInvoices.exemptionsHeader',
  'bulkInvoices.exemptionApply',
  'bulkInvoices.exemptionRole',
  'bulkInvoices.exemptionItem',
  'bulkInvoices.exemptionsHint',
  'bulkInvoices.exemptionsApplied',
]

const LOCALES: Record<string, unknown> = { de, en, fr, it: it_, nb, nl, nn, pl }

function lookup(messages: unknown, key: string): unknown {
  return key.split('.').reduce<unknown>((node, part) => (node as Record<string, unknown> | undefined)?.[part], messages)
}

describe('role exemption locale keys', () => {
  for (const [locale, messages] of Object.entries(LOCALES)) {
    it(`${locale} has every exemption string`, () => {
      const missing = KEYS.filter((k) => typeof lookup(messages, k) !== 'string' || lookup(messages, k) === '')
      expect(missing).toEqual([])
    })
  }
})
