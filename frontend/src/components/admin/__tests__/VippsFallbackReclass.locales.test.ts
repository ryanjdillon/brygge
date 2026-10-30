import { describe, it, expect } from 'vitest'
import de from '@/locales/de.json'
import en from '@/locales/en.json'
import fr from '@/locales/fr.json'
import it_ from '@/locales/it.json'
import nb from '@/locales/nb.json'
import nl from '@/locales/nl.json'
import nn from '@/locales/nn.json'
import pl from '@/locales/pl.json'

// Every reason_code the server can give a charge on 3900 (the decide* codes
// in vipps_auto_assign.go and the reasonCode* ones in
// vipps_fallback_reclass.go) has a translation in every locale: the UI never
// shows the server's English text.
const CODES = [
  'auto_assign_off', 'kid_alias_conflict', 'open_faktura_same_amount', 'no_match', 'several_categories',
  'several_nights', 'members_only_unknown_payer', 'no_revenue_account', 'same_account', 'maybe_paid_faktura',
  'has_faktura', 'order_charge', 'bank_linked', 'unexpected_shape', 'target_account_missing', 'already_moved',
  'not_in_request', 'target_changed', 'period_not_open', 'decision_failed', 'failed',
]

const locales = { de, en, fr, it: it_, nb, nl, nn, pl } as Record<string, {
  admin: { accounting: { vippsFallbackReclass: { reason: Record<string, string>; confirmBody: string; maybePaid?: string } } }
}>

describe('Vipps fallback reclass locales', () => {
  for (const [name, locale] of Object.entries(locales)) {
    it(`${name} translates every reason code`, () => {
      const block = locale.admin.accounting.vippsFallbackReclass
      expect(Object.keys(block.reason).sort()).toEqual([...CODES].sort())
      for (const code of CODES) expect(block.reason[code]).toBeTruthy()
      // The faktura a possibly paid charge may belong to is named.
      expect(block.reason.maybe_paid_faktura).toContain('{number}')
      // The clearing account is the club's setting, never hard-coded.
      expect(block.confirmBody).toContain('{clearing}')
      expect(block.confirmBody).not.toContain('2900')
    })
  }
})
