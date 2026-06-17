<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Plus, Trash2, ListPlus } from 'lucide-vue-next'
import Modal from '@/components/ui/Modal.vue'
import { useAccountsList, useFiscalPeriods } from '@/composables/useAccounting'
import { usePricing } from '@/composables/usePricing'
import MemberSearch, { type MemberHit } from '@/components/members/MemberSearch.vue'
import LineItemPicker from '@/components/admin/LineItemPicker.vue'
import AccountSelect from '@/components/admin/AccountSelect.vue'
import { formatNOK } from '@/lib/format'
import {
  lookupByOrgNumber,
  searchByName,
  formatBrregAddress,
  type BrregEntity,
} from '@/composables/useBrreg'

defineProps<{
  open: boolean
}>()
const emit = defineEmits<{
  (e: 'close'): void
  (e: 'created', invoiceId: string): void
}>()

const { t } = useI18n()

const { categoryLabel } = usePricing()
const { data: accounts } = useAccountsList()
// A faktura is money owed *to* the club, so the line's bookkeeping
// counterpart should be revenue (or, occasionally, a liability —
// e.g. a deposit / forskuddsbetaling that we book against a 29xx
// account). Asset and expense accounts shouldn't appear: they'd be
// nonsensical credits when issuing an invoice.
const billableAccountTypes = new Set(['revenue', 'liability'])
const activeAccounts = computed(() =>
  (accounts.value ?? [])
    .filter((a) => a.is_active && billableAccountTypes.has(a.account_type))
    .slice()
    .sort((a, b) => a.code.localeCompare(b.code, undefined, { numeric: true })),
)

// Categories present on the active list, used to render the legend
// below the line-item editor.
const accountTypesPresent = computed(() => {
  const set = new Set<string>()
  for (const a of activeAccounts.value) set.add(a.account_type)
  return [...set]
})

function chipClass(type: string): string {
  switch (type) {
    case 'asset': return 'bg-blue-100 text-blue-800'
    case 'liability': return 'bg-amber-100 text-amber-800'
    case 'revenue': return 'bg-green-100 text-green-800'
    case 'expense': return 'bg-red-100 text-red-800'
    default: return 'bg-slate-100 text-slate-700'
  }
}

function typeLabel(type: string): string {
  return t(`admin.singleFaktura.accountType.${type}`) || type
}

const { data: periods } = useFiscalPeriods()
const openPeriods = computed(() =>
  (periods.value ?? []).filter((p) => p.status === 'open').sort((a, b) => b.year - a.year),
)
const fiscalPeriodId = ref('')
watch(openPeriods, (list) => {
  if (!fiscalPeriodId.value && list.length > 0) {
    fiscalPeriodId.value = list[0].id
  }
}, { immediate: true })

interface PriceItem {
  id: string
  category: string
  name: string
  amount: number
  unit: string
  is_active: boolean
  pricing_kind?: 'flat' | 'tiered'
  tier_dimension?: 'beam' | 'length' | null
  show_in_single?: boolean
  requires_boat_selection?: boolean
}

interface Boat {
  id: string
  name: string
  manufacturer?: string
  model?: string
  beam_m?: number
  length_m?: number
}

// Each line tracks its origin so the submit step knows whether to send
// it as a custom line (account_id required), a flat price_item_id
// line, or a tier_category line that the server resolves from boat_id.
interface LineDraft {
  kind: 'custom' | 'flat' | 'tier'
  description: string
  sub_description: string
  quantity: number
  unit_price: number
  account_id: string
  price_item_id?: string
  tier_category?: string
  boat_id: string
  requires_boat_selection: boolean
}

const member = ref<MemberHit | null>(null)
const dueDate = ref(defaultDueDate())
const lines = ref<LineDraft[]>([emptyCustomLine()])
const submitting = ref(false)
const error = ref<string | null>(null)

// Recipient kind defaults to "private". Either mode supports issuing
// without a member: in private mode the admin types the recipient
// name + address directly; in organisation mode the org block stands
// on its own. Linking a member is optional and just pre-fills the
// relevant fields on selection.
type RecipientKind = 'private' | 'organization'
const recipientKind = ref<RecipientKind>('private')

// Private-mode recipient fields. recipientName/recipientAddress are
// the manually-fillable "Mottaker" fields; selecting a member fills
// them but they remain editable so admins can correct or override.
const recipientName = ref('')
const recipientAddress = ref('')

// Organisation-mode recipient fields.
const orgName = ref('')
const orgNumber = ref('')
const orgAddress = ref('')
const contactPerson = ref('')
const theirRef = ref('')
const recipientEmail = ref('')

watch(member, (m) => {
  if (!m) return
  // Pre-fill the relevant recipient block from the chosen member; the
  // admin can still override them. Private-mode fields are
  // populated whenever empty so re-selecting a member after editing
  // doesn't clobber manual input.
  if (!recipientName.value) recipientName.value = m.full_name ?? ''
  if (!recipientEmail.value) recipientEmail.value = m.email ?? ''
  if (!contactPerson.value) contactPerson.value = m.full_name ?? ''
  if (!recipientAddress.value) {
    const cityLine = [m.postal_code, m.city].filter(Boolean).join(' ').trim()
    recipientAddress.value = [m.address_line, cityLine].filter(Boolean).join(', ')
  }
})

// --- Brønnøysund (BRREG) integration ---
// On org-number change: when 9 digits with a valid mod-11 checksum,
// fetch the entity and auto-fill the empty fields. Never overwrite
// admin-entered text. Surface a warning when the entity is bankrupt
// or deleted.
const brregWarning = ref<string | null>(null)
const brregLooking = ref(false)
let brregLookupAbort: AbortController | null = null

watch(orgNumber, async (val) => {
  brregWarning.value = null
  if (recipientKind.value !== 'organization') return
  const cleaned = (val ?? '').replace(/\s/g, '')
  if (cleaned.length !== 9) return
  if (brregLookupAbort) brregLookupAbort.abort()
  const ctrl = new AbortController()
  brregLookupAbort = ctrl
  brregLooking.value = true
  try {
    const ent = await lookupByOrgNumber(cleaned, ctrl.signal)
    if (ctrl.signal.aborted) return
    if (!ent) return
    if (!orgName.value.trim()) orgName.value = ent.navn ?? ''
    if (!orgAddress.value.trim()) {
      orgAddress.value = formatBrregAddress(ent.forretningsadresse ?? ent.postadresse)
    }
    if (ent.konkurs) brregWarning.value = t('admin.singleFaktura.brregBankrupt')
    else if (ent.slettedato) brregWarning.value = t('admin.singleFaktura.brregDeleted', { date: ent.slettedato })
    else if (ent.underAvvikling) brregWarning.value = t('admin.singleFaktura.brregWindingDown')
  } finally {
    brregLooking.value = false
  }
})

// Name autocomplete: 300ms debounced search of /enheter?navn=...
// Selecting a hit fills name + number + address atomically. The
// brregSearching flag drives a visible "Søker…" indicator so the
// admin can see something is happening — without it the dropdown
// silently appearing 300+ ms later felt unresponsive.
const brregHits = ref<BrregEntity[]>([])
const brregHitsOpen = ref(false)
const brregSearching = ref(false)
const brregLastQuery = ref('')
let brregSearchAbort: AbortController | null = null
let brregSearchTimer: ReturnType<typeof setTimeout> | null = null

watch(orgName, (val) => {
  if (recipientKind.value !== 'organization') {
    brregHits.value = []
    brregSearching.value = false
    return
  }
  if (brregSearchTimer) clearTimeout(brregSearchTimer)
  const q = (val ?? '').trim()
  brregLastQuery.value = q
  if (q.length < 2) {
    brregHits.value = []
    brregHitsOpen.value = false
    brregSearching.value = false
    return
  }
  brregSearching.value = true
  brregHitsOpen.value = true
  brregSearchTimer = setTimeout(async () => {
    if (brregSearchAbort) brregSearchAbort.abort()
    const ctrl = new AbortController()
    brregSearchAbort = ctrl
    try {
      const hits = await searchByName(q, ctrl.signal)
      if (ctrl.signal.aborted) return
      brregHits.value = hits
      brregHitsOpen.value = true
    } finally {
      if (!ctrl.signal.aborted) brregSearching.value = false
    }
  }, 300)
})

function pickBrregHit(hit: BrregEntity) {
  orgName.value = hit.navn ?? ''
  orgNumber.value = hit.organisasjonsnummer ?? ''
  if (!orgAddress.value.trim()) {
    orgAddress.value = formatBrregAddress(hit.forretningsadresse ?? hit.postadresse)
  }
  if (hit.konkurs) brregWarning.value = t('admin.singleFaktura.brregBankrupt')
  else if (hit.slettedato) brregWarning.value = t('admin.singleFaktura.brregDeleted', { date: hit.slettedato })
  else if (hit.underAvvikling) brregWarning.value = t('admin.singleFaktura.brregWindingDown')
  else brregWarning.value = null
  brregHits.value = []
  brregHitsOpen.value = false
}

// Tiny delay before closing the dropdown on blur so a click on a hit
// (which fires after blur) lands on a still-mounted button. The
// 150 ms matches @mousedown.prevent + browser focus handoff timing.
function closeBrregHitsSoon() {
  setTimeout(() => { brregHitsOpen.value = false }, 150)
}

function brregSubtitle(hit: BrregEntity): string {
  const parts: string[] = [hit.organisasjonsnummer]
  const place = hit.forretningsadresse?.poststed || hit.postadresse?.poststed
  if (place) parts.push(place)
  if (hit.konkurs) parts.push(t('admin.singleFaktura.brregBankruptShort'))
  else if (hit.slettedato) parts.push(t('admin.singleFaktura.brregDeletedShort'))
  return parts.join(' · ')
}

const showPicker = ref(false)
const pickerFlatIds = ref<string[]>([])
const pickerTierCategories = ref<string[]>([])
const knownItems = ref<PriceItem[]>([])
const boats = ref<Boat[]>([])

watch(member, async (m) => {
  boats.value = []
  if (!m) return
  try {
    const res = await fetch(`/api/v1/admin/users/${m.id}/boats`, { credentials: 'include' })
    if (res.ok) {
      const body = await res.json()
      boats.value = (body.boats ?? []) as Boat[]
    }
  } catch {
    // boats list is best-effort; absence just disables boat-tied lines
  }
})

function defaultDueDate(): string {
  const d = new Date()
  d.setDate(d.getDate() + 21)
  return d.toISOString().slice(0, 10)
}

function emptyCustomLine(): LineDraft {
  return {
    kind: 'custom',
    description: '',
    sub_description: '',
    quantity: 1,
    unit_price: 0,
    account_id: '',
    boat_id: '',
    requires_boat_selection: false,
  }
}

function addCustomLine() {
  const last = lines.value[lines.value.length - 1]
  lines.value.push({
    ...emptyCustomLine(),
    description: last?.kind === 'custom' ? last.description : '',
    unit_price: last?.kind === 'custom' ? last.unit_price : 0,
    account_id: last?.kind === 'custom' ? last.account_id : '',
  })
}

function removeLine(i: number) {
  if (lines.value.length === 1) {
    lines.value = [emptyCustomLine()]
    return
  }
  lines.value.splice(i, 1)
}

function applyPickerSelection() {
  // Convert picker selection into draft lines, then drop the trailing
  // empty custom line if the only line was the placeholder.
  const newLines: LineDraft[] = []
  for (const id of pickerFlatIds.value) {
    const item = knownItems.value.find((i) => i.id === id)
    if (!item) continue
    newLines.push({
      kind: 'flat',
      description: item.name,
      sub_description: '',
      quantity: 1,
      unit_price: item.amount,
      account_id: '',
      price_item_id: item.id,
      boat_id: '',
      requires_boat_selection: item.requires_boat_selection === true,
    })
  }
  for (const cat of pickerTierCategories.value) {
    newLines.push({
      kind: 'tier',
      description: categoryLabel(cat),
      sub_description: '',
      quantity: 1,
      unit_price: 0,
      account_id: '',
      tier_category: cat,
      boat_id: '',
      requires_boat_selection: true,
    })
  }
  if (newLines.length === 0) return
  // Replace placeholder if it's still empty.
  if (
    lines.value.length === 1 &&
    lines.value[0].kind === 'custom' &&
    !lines.value[0].description &&
    lines.value[0].unit_price === 0
  ) {
    lines.value = newLines
  } else {
    lines.value = [...lines.value, ...newLines]
  }
  pickerFlatIds.value = []
  pickerTierCategories.value = []
  showPicker.value = false
}

const total = computed(() =>
  lines.value.reduce((s, l) => {
    if (l.kind === 'tier') return s
    return s + Number(l.quantity || 0) * Number(l.unit_price || 0)
  }, 0),
)

function reset() {
  member.value = null
  dueDate.value = defaultDueDate()
  lines.value = [emptyCustomLine()]
  pickerFlatIds.value = []
  pickerTierCategories.value = []
  showPicker.value = false
  recipientKind.value = 'private'
  recipientName.value = ''
  recipientAddress.value = ''
  orgName.value = ''
  orgNumber.value = ''
  orgAddress.value = ''
  contactPerson.value = ''
  theirRef.value = ''
  recipientEmail.value = ''
  brregHits.value = []
  brregHitsOpen.value = false
  brregWarning.value = null
  error.value = null
}

async function submit() {
  error.value = null
  if (recipientKind.value === 'organization') {
    if (!orgName.value.trim()) {
      error.value = t('admin.singleFaktura.orgNameRequired')
      return
    }
  } else {
    // Private mode needs *some* name on the PDF: either the linked
    // member's, or a manually-typed Mottaker.
    if (!member.value && !recipientName.value.trim()) {
      error.value = t('admin.singleFaktura.recipientRequired')
      return
    }
  }
  // Tier lines need a boat which lives on the user record, so they
  // require a linked member regardless of mode.
  if (!member.value && lines.value.some((l) => l.kind === 'tier' || (l.kind === 'flat' && l.requires_boat_selection))) {
    error.value = t('admin.singleFaktura.memberRequiredForBoatLines')
    return
  }
  for (const l of lines.value) {
    if (!l.description.trim() || l.quantity < 1) {
      error.value = t('admin.singleFaktura.linesInvalid')
      return
    }
    if (l.kind === 'custom') {
      if (l.unit_price <= 0) {
        error.value = t('admin.singleFaktura.linesInvalid')
        return
      }
      if (!l.account_id) {
        error.value = t('admin.singleFaktura.accountRequired')
        return
      }
    }
    if (l.kind === 'tier' && !l.boat_id) {
      error.value = t('admin.singleFaktura.boatRequired')
      return
    }
    if (l.kind === 'flat' && l.requires_boat_selection && !l.boat_id) {
      error.value = t('admin.singleFaktura.boatRequired')
      return
    }
  }

  submitting.value = true
  try {
    const res = await fetch('/api/v1/admin/financials/invoices/full', {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        user_id: member.value?.id || undefined,
        due_date: dueDate.value,
        fiscal_period_id: fiscalPeriodId.value || undefined,
        recipient_kind: recipientKind.value,
        // recipient_org_name is the canonical recipient name in both
        // modes (org legal name, or person name in private mode);
        // recipient_org_address is the canonical address. The form
        // sends them whenever filled so the invoice carries a
        // self-contained snapshot independent of the member record.
        recipient_org_name: recipientKind.value === 'organization'
          ? orgName.value.trim()
          : (recipientName.value.trim() || undefined),
        recipient_org_address: recipientKind.value === 'organization'
          ? (orgAddress.value.trim() || undefined)
          : (recipientAddress.value.trim() || undefined),
        recipient_org_number: recipientKind.value === 'organization' && orgNumber.value.trim() ? orgNumber.value.trim() : undefined,
        recipient_contact_person: recipientKind.value === 'organization' && contactPerson.value.trim() ? contactPerson.value.trim() : undefined,
        recipient_their_ref: recipientKind.value === 'organization' && theirRef.value.trim() ? theirRef.value.trim() : undefined,
        recipient_email: recipientEmail.value.trim() || undefined,
        lines: lines.value.map((l) => ({
          description: l.description.trim(),
          sub_description: l.sub_description.trim() || undefined,
          quantity: Number(l.quantity),
          unit_price: l.kind === 'tier' ? 0 : Number(l.unit_price),
          account_id: l.kind === 'custom' ? l.account_id : undefined,
          price_item_id: l.kind === 'flat' ? l.price_item_id : undefined,
          tier_category: l.kind === 'tier' ? l.tier_category : undefined,
          boat_id: l.boat_id || undefined,
        })),
      }),
    })
    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      error.value = `${res.status} ${txt}`
      return
    }
    const body = await res.json().catch(() => ({}))
    emit('created', body.id ?? '')
    reset()
    emit('close')
  } finally {
    submitting.value = false
  }
}

function boatLabel(b: Boat): string {
  const make = [b.manufacturer, b.model].filter(Boolean).join(' ')
  const dims: string[] = []
  if (b.length_m) dims.push(`${b.length_m}m`)
  if (b.beam_m) dims.push(`${b.beam_m}m b`)
  return [b.name, make].filter(Boolean).join(' — ') + (dims.length ? ` (${dims.join(', ')})` : '')
}
</script>

<template>
  <Modal
    :open="open"
    size="2xl"
    :z-index="40"
    :close-on-backdrop="false"
    :title="t('admin.singleFaktura.title')"
    @close="emit('close')"
  >
    <form class="mt-4 space-y-4" @submit.prevent="submit">
        <!-- Payee block — wrapped in a soft slate card so it reads as
             one logical group separate from the line-item editor. -->
        <div class="space-y-3 rounded-lg bg-slate-50 p-4">
          <div>
            <label class="block text-xs font-medium text-slate-700">{{ t('admin.singleFaktura.recipientKindLabel') }}</label>
            <div class="mt-1 inline-flex rounded-md bg-white p-0.5 ring-1 ring-slate-200">
              <button
                type="button"
                :class="['px-3 py-1 text-xs font-medium rounded-[5px] transition', recipientKind === 'private' ? 'bg-slate-900 text-white' : 'text-slate-600 hover:text-slate-900']"
                @click="recipientKind = 'private'"
              >
                {{ t('admin.singleFaktura.recipientPrivate') }}
              </button>
              <button
                type="button"
                :class="['px-3 py-1 text-xs font-medium rounded-[5px] transition', recipientKind === 'organization' ? 'bg-slate-900 text-white' : 'text-slate-600 hover:text-slate-900']"
                @click="recipientKind = 'organization'"
              >
                {{ t('admin.singleFaktura.recipientOrganization') }}
              </button>
            </div>
            <p v-if="recipientKind === 'organization'" class="mt-1 text-xs text-slate-500">
              {{ t('admin.singleFaktura.recipientOrgHint') }}
            </p>
          </div>

          <div>
            <label class="block text-xs font-medium text-slate-700">
              {{ t('admin.singleFaktura.member') }}
              <span class="ml-1 text-[10px] font-normal text-slate-400">{{ t('admin.singleFaktura.memberOptional') }}</span>
            </label>
            <MemberSearch v-model="member" :placeholder="t('admin.singleFaktura.memberPlaceholder')" class="mt-1" />
          </div>

          <!-- Private-mode mottaker block. Fields are editable
               regardless of whether a member is selected — selection
               just pre-fills empty fields. Admins can override the
               typed text at any time. -->
          <div v-if="recipientKind === 'private'" class="space-y-2 rounded-md bg-white p-3 ring-1 ring-slate-200">
            <div>
              <label class="block text-xs font-medium text-slate-700">
                {{ t('admin.singleFaktura.recipientName') }} *
              </label>
              <input
                v-model="recipientName"
                type="text"
                required
                class="mt-1 w-full rounded-md border border-slate-300 px-2 py-1 text-sm"
                :placeholder="t('admin.singleFaktura.recipientNamePlaceholder')"
              />
            </div>
            <div>
              <label class="block text-xs font-medium text-slate-700">{{ t('admin.singleFaktura.recipientAddress') }}</label>
              <textarea
                v-model="recipientAddress"
                rows="2"
                class="mt-1 w-full rounded-md border border-slate-300 px-2 py-1 text-sm"
                :placeholder="t('admin.singleFaktura.recipientAddressPlaceholder')"
              />
            </div>
            <div>
              <label class="block text-xs font-medium text-slate-700">{{ t('admin.singleFaktura.recipientEmail') }}</label>
              <input
                v-model="recipientEmail"
                type="email"
                class="mt-1 w-full rounded-md border border-slate-300 px-2 py-1 text-sm"
                :placeholder="t('admin.singleFaktura.recipientEmailPlaceholder')"
              />
            </div>
          </div>

          <!-- Organisation override block — shown when the toggle is in
               'organization' mode. Org name is required; the rest are
               optional overrides over the contact person's defaults. -->
          <div v-if="recipientKind === 'organization'" class="space-y-2 rounded-md bg-white p-3 ring-1 ring-slate-200">
            <p
              v-if="brregWarning"
              class="rounded-md bg-amber-50 px-2 py-1.5 text-xs text-amber-800 ring-1 ring-amber-200"
            >
              ⚠ {{ brregWarning }}
            </p>
            <div class="grid gap-3 sm:grid-cols-2">
              <div class="sm:col-span-2">
                <label class="block text-xs font-medium text-slate-700">
                  {{ t('admin.singleFaktura.orgName') }} *
                  <span class="ml-1 text-[10px] font-normal text-slate-400">{{ t('admin.singleFaktura.brregHint') }}</span>
                </label>
                <div class="relative">
                  <input
                    v-model="orgName"
                    type="text"
                    required
                    autocomplete="off"
                    class="mt-1 w-full rounded-md border border-slate-300 px-2 py-1 text-sm"
                    :placeholder="t('admin.singleFaktura.orgNamePlaceholder')"
                    @focus="brregHitsOpen = brregHits.length > 0"
                    @blur="closeBrregHitsSoon"
                  />
                  <div
                    v-if="brregHitsOpen && (brregSearching || brregHits.length || brregLastQuery.length >= 2)"
                    class="absolute left-0 right-0 top-full z-30 mt-1 max-h-60 overflow-y-auto rounded-md border border-slate-200 bg-white py-1 shadow-lg"
                  >
                    <div v-if="brregSearching" class="px-3 py-2 text-[11px] text-slate-500">
                      {{ t('admin.singleFaktura.brregSearching') }}
                    </div>
                    <button
                      v-for="hit in brregHits"
                      :key="hit.organisasjonsnummer"
                      type="button"
                      class="block w-full px-2 py-1.5 text-left text-xs hover:bg-slate-50"
                      @mousedown.prevent="pickBrregHit(hit)"
                    >
                      <span class="block truncate font-medium text-slate-900">{{ hit.navn }}</span>
                      <span class="block truncate text-[11px] text-slate-500">{{ brregSubtitle(hit) }}</span>
                    </button>
                    <div
                      v-if="!brregSearching && brregHits.length === 0 && brregLastQuery.length >= 2"
                      class="px-3 py-2 text-[11px] text-slate-500"
                    >
                      {{ t('admin.singleFaktura.brregNoHits') }}
                    </div>
                  </div>
                </div>
              </div>
              <div>
                <label class="block text-xs font-medium text-slate-700">
                  {{ t('admin.singleFaktura.orgNumber') }}
                  <span v-if="brregLooking" class="ml-1 text-[10px] font-normal text-slate-400">
                    {{ t('common.loading') }}…
                  </span>
                </label>
                <input
                  v-model="orgNumber"
                  type="text"
                  inputmode="numeric"
                  pattern="\d{3} ?\d{3} ?\d{3}"
                  class="mt-1 w-full rounded-md border border-slate-300 px-2 py-1 text-sm font-mono"
                  placeholder="999 999 999"
                />
              </div>
              <div>
                <label class="block text-xs font-medium text-slate-700">{{ t('admin.singleFaktura.theirRef') }}</label>
                <input
                  v-model="theirRef"
                  type="text"
                  class="mt-1 w-full rounded-md border border-slate-300 px-2 py-1 text-sm"
                  :placeholder="t('admin.singleFaktura.theirRefPlaceholder')"
                />
              </div>
            </div>
            <div>
              <label class="block text-xs font-medium text-slate-700">{{ t('admin.singleFaktura.orgAddress') }}</label>
              <textarea
                v-model="orgAddress"
                rows="2"
                class="mt-1 w-full rounded-md border border-slate-300 px-2 py-1 text-sm"
                :placeholder="t('admin.singleFaktura.orgAddressPlaceholder')"
              />
            </div>
            <div class="grid gap-3 sm:grid-cols-2">
              <div>
                <label class="block text-xs font-medium text-slate-700">{{ t('admin.singleFaktura.contactPerson') }}</label>
                <input
                  v-model="contactPerson"
                  type="text"
                  class="mt-1 w-full rounded-md border border-slate-300 px-2 py-1 text-sm"
                  :placeholder="t('admin.singleFaktura.contactPersonPlaceholder')"
                />
              </div>
              <div>
                <label class="block text-xs font-medium text-slate-700">{{ t('admin.singleFaktura.recipientEmail') }}</label>
                <input
                  v-model="recipientEmail"
                  type="email"
                  class="mt-1 w-full rounded-md border border-slate-300 px-2 py-1 text-sm"
                  :placeholder="t('admin.singleFaktura.recipientEmailPlaceholder')"
                />
              </div>
            </div>
          </div>

          <div class="grid gap-3 sm:grid-cols-2">
            <div>
              <label class="block text-xs font-medium text-slate-700">{{ t('admin.singleFaktura.fiscalPeriod') }}</label>
              <select v-model="fiscalPeriodId" class="mt-1 w-full rounded-md border border-slate-300 bg-white px-2 py-1 text-sm">
                <option value="">{{ t('admin.singleFaktura.fiscalPeriodNone') }}</option>
                <option v-for="p in openPeriods" :key="p.id" :value="p.id">{{ p.year }}</option>
              </select>
            </div>
            <div>
              <label class="block text-xs font-medium text-slate-700">{{ t('admin.singleFaktura.dueDate') }}</label>
              <input
                v-model="dueDate"
                type="date"
                required
                class="mt-1 w-full rounded-md border border-slate-300 bg-white px-2 py-1 text-sm"
              />
            </div>
          </div>
        </div>

        <div>
          <div class="flex items-center justify-between">
            <label class="text-xs font-medium text-gray-700">{{ t('admin.singleFaktura.lines') }}</label>
            <div class="flex gap-2">
              <button
                type="button"
                class="inline-flex items-center gap-1 rounded-md border border-gray-300 bg-white px-2 py-1 text-xs hover:bg-gray-50"
                @click="showPicker = !showPicker"
              >
                <ListPlus class="h-3.5 w-3.5" /> {{ t('admin.singleFaktura.addFromPriceList') }}
              </button>
              <button
                type="button"
                class="inline-flex items-center gap-1 rounded-md border border-gray-300 bg-white px-2 py-1 text-xs hover:bg-gray-50"
                @click="addCustomLine"
              >
                <Plus class="h-3.5 w-3.5" /> {{ t('admin.singleFaktura.addCustomLine') }}
              </button>
            </div>
          </div>

          <div v-if="showPicker" class="mt-2 rounded-md border border-blue-200 bg-blue-50/30 p-3">
            <LineItemPicker
              mode="single"
              :flat-ids="pickerFlatIds"
              :tier-categories="pickerTierCategories"
              @update:flat-ids="(v) => (pickerFlatIds = v)"
              @update:tier-categories="(v) => (pickerTierCategories = v)"
              @loaded="(items) => (knownItems = items)"
            />
            <div class="mt-2 flex justify-end gap-2">
              <button
                type="button"
                class="rounded-md px-2 py-1 text-xs text-gray-600 hover:bg-gray-100"
                @click="showPicker = false"
              >
                {{ t('common.cancel') }}
              </button>
              <button
                type="button"
                :disabled="pickerFlatIds.length === 0 && pickerTierCategories.length === 0"
                class="rounded-md bg-blue-600 px-2 py-1 text-xs font-semibold text-white hover:bg-blue-700 disabled:opacity-50"
                @click="applyPickerSelection"
              >
                {{ t('admin.singleFaktura.applyPickerSelection') }}
              </button>
            </div>
          </div>

          <div class="mt-2 space-y-3">
            <!-- Each line is its own slate card. Same fill as the payee
                 block so the form reads as a sequence of cohesive
                 panels. -->
            <div v-for="(l, i) in lines" :key="i" class="rounded-lg bg-slate-50 p-3">
              <div class="grid grid-cols-12 gap-2">
                <input
                  v-model="l.description"
                  type="text"
                  :placeholder="t('admin.singleFaktura.descriptionPlaceholder')"
                  :readonly="l.kind === 'tier'"
                  class="col-span-7 rounded-md border border-slate-300 bg-white px-2 py-1 text-sm"
                />
                <input
                  v-model.number="l.quantity"
                  type="number"
                  min="1"
                  class="col-span-2 rounded-md border border-slate-300 bg-white px-2 py-1 text-sm tabular-nums"
                  :title="t('admin.singleFaktura.quantity')"
                />
                <input
                  v-model.number="l.unit_price"
                  type="number"
                  min="0"
                  step="0.01"
                  :readonly="l.kind === 'tier'"
                  :placeholder="l.kind === 'tier' ? t('admin.singleFaktura.tierResolved') : ''"
                  class="col-span-2 rounded-md border border-slate-300 bg-white px-2 py-1 text-sm tabular-nums"
                  :title="t('admin.singleFaktura.unitPrice')"
                />
                <button
                  type="button"
                  class="col-span-1 text-red-500 hover:text-red-700"
                  :title="t('common.delete')"
                  @click="removeLine(i)"
                >
                  <Trash2 class="h-4 w-4" />
                </button>
              </div>
              <div class="mt-2 grid grid-cols-12 gap-2">
                <input
                  v-model="l.sub_description"
                  type="text"
                  :placeholder="t('admin.singleFaktura.subDescriptionPlaceholder')"
                  class="col-span-7 rounded-md border border-slate-200 bg-white px-2 py-1 text-xs text-slate-700"
                />
                <div class="col-span-5">
                  <AccountSelect
                    v-if="l.kind === 'custom'"
                    v-model="l.account_id"
                    :accounts="activeAccounts"
                    :placeholder="t('admin.singleFaktura.accountPlaceholder')"
                  />
                  <select
                    v-else-if="l.kind === 'tier' || l.requires_boat_selection"
                    v-model="l.boat_id"
                    required
                    class="w-full rounded-md border border-slate-300 bg-white px-2 py-1 text-xs"
                  >
                    <option value="">{{ t('admin.singleFaktura.boatPlaceholder') }}</option>
                    <option v-for="b in boats" :key="b.id" :value="b.id">
                      {{ boatLabel(b) }}
                    </option>
                  </select>
                  <span v-else class="block self-center text-xs text-slate-400">
                    {{ t('admin.singleFaktura.fromPriceList') }}
                  </span>
                </div>
              </div>
              <p v-if="l.kind === 'tier' && !boats.length && member" class="mt-1 text-xs text-amber-600">
                {{ t('admin.singleFaktura.noBoatsForMember') }}
              </p>
            </div>
          </div>

          <!-- Account-type legend — chips render the localised type
               name directly, matching the chart-of-accounts palette so
               an admin can decode a chip at a glance. Only renders
               types actually represented in the active list. -->
          <div v-if="accountTypesPresent.length" class="mt-3 flex flex-wrap items-center gap-x-2 gap-y-1.5 text-xs">
            <span class="font-medium text-slate-600">{{ t('admin.singleFaktura.legendLabel') }}</span>
            <span
              v-for="ty in accountTypesPresent"
              :key="ty"
              :class="['rounded-full px-2 py-0.5 text-[11px] font-semibold', chipClass(ty)]"
            >
              {{ typeLabel(ty) }}
            </span>
          </div>

          <p class="mt-2 text-right text-sm font-semibold text-slate-700">
            {{ t('admin.singleFaktura.total') }}: {{ formatNOK(total) }}
            <span v-if="lines.some((l) => l.kind === 'tier')" class="ml-1 text-xs font-normal text-slate-500">
              ({{ t('admin.singleFaktura.tierExcluded') }})
            </span>
          </p>
        </div>

        <p v-if="error" class="rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">{{ error }}</p>

        <div class="flex justify-end gap-2 border-t border-gray-100 pt-3">
          <button
            type="button"
            class="rounded-md border border-gray-300 bg-white px-3 py-2 text-sm font-semibold text-gray-700 hover:bg-gray-50"
            @click="emit('close')"
          >
            {{ t('common.cancel') }}
          </button>
          <button
            type="submit"
            :disabled="submitting"
            class="rounded-md bg-blue-600 px-3 py-2 text-sm font-semibold text-white hover:bg-blue-700 disabled:opacity-50"
          >
            {{ submitting ? t('common.loading') : t('admin.singleFaktura.create') }}
          </button>
        </div>
      </form>
  </Modal>
</template>
