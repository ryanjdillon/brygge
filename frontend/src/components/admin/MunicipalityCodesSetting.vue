<script setup lang="ts">
import { ref, reactive, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { X } from 'lucide-vue-next'
import { useFreshTotp } from '@/composables/useFreshTotp'

// The municipalities whose verified residents count as local when they join
// (clubs.municipality_codes, BRY-643). Each code shows its name from the
// server's kommuneinfo lookup; a lookup that fails shows the code alone, and
// an unknown code is marked but can still be saved (a new code after a merger
// may reach the register late).
const props = defineProps<{ codes: string[] }>()
const emit = defineEmits<{ saved: [codes: string[]] }>()

const { t } = useI18n()
const { ensureFreshTotp, totpAwareFetch } = useFreshTotp()

const MAX_CODES = 20
const CODE = /^[0-9]{4}$/

const draft = ref<string[]>([...props.codes])
// A reload from the parent replaces the draft only while it is unedited, so
// codes added but not yet saved survive it.
function sameCodes(a: string[], b: string[]) {
  return a.length === b.length && a.every((c, i) => c === b[i])
}
watch(() => props.codes, (codes, prev) => {
  if (sameCodes(draft.value, prev ?? [])) draft.value = [...codes]
})

const input = ref('')
const inputError = ref<string | null>(null)
const saving = ref(false)
const saved = ref(false)
const saveError = ref<string | null>(null)

type Lookup = { status: 'loading' | 'found' | 'unknown' | 'unavailable'; name: string }
const names = reactive<Record<string, Lookup>>({})
// Read out by a polite live region: the name found for the code just added.
const announced = ref('')
const announceFor = ref<string | null>(null)

async function lookUp(code: string) {
  if (names[code]) return
  names[code] = { status: 'loading', name: '' }
  try {
    const res = await fetch(`/api/v1/admin/settings/municipalities/${code}`, { credentials: 'include' })
    if (!res.ok) throw new Error(String(res.status))
    const body = await res.json()
    const status = body?.status === 'found' || body?.status === 'unknown' ? body.status : 'unavailable'
    names[code] = { status, name: status === 'found' && typeof body.name === 'string' ? body.name : '' }
  } catch {
    names[code] = { status: 'unavailable', name: '' }
  }
  if (announceFor.value === code) {
    const n = names[code]
    announced.value = n.status === 'found'
      ? `${code} ${n.name}`
      : n.status === 'unknown' ? `${code} (${t('admin.economySettings.municipalityCodesUnknown')})` : ''
  }
}
watch(draft, (codes) => codes.forEach(lookUp), { immediate: true, deep: true })

// add moves the typed code into the list; it reports false, with the input
// error set, when the code cannot be added.
function add(): boolean {
  const code = input.value.trim()
  inputError.value = null
  if (!CODE.test(code)) {
    inputError.value = t('admin.economySettings.municipalityCodesBadInput')
    return false
  }
  if (draft.value.includes(code)) {
    input.value = ''
    return true
  }
  if (draft.value.length >= MAX_CODES) {
    inputError.value = t('admin.economySettings.municipalityCodesMax', { max: MAX_CODES })
    return false
  }
  input.value = ''
  announceFor.value = code
  announced.value = ''
  draft.value = [...draft.value, code]
  saved.value = false
  return true
}

function remove(code: string) {
  draft.value = draft.value.filter((c) => c !== code)
  saved.value = false
}

async function save() {
  // A code typed but not added is saved with the rest; one that cannot be
  // added stops the save, with its error shown, before the TOTP prompt.
  if (input.value.trim() !== '' && !add()) return
  if (!(await ensureFreshTotp())) return
  saving.value = true
  saved.value = false
  saveError.value = null
  try {
    const res = await totpAwareFetch('/api/v1/admin/settings/site', {
      method: 'PATCH',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ municipality_codes: draft.value }),
    })
    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      let code = ''
      try { code = JSON.parse(txt)?.code ?? '' } catch { /* not JSON */ }
      saveError.value = res.status === 400 && code === 'invalid_municipality_codes'
        ? t('admin.economySettings.municipalityCodesInvalid')
        : `${res.status} ${txt}`
      return
    }
    saved.value = true
    emit('saved', [...draft.value])
  } catch (e) {
    saveError.value = (e as Error).message
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <fieldset class="rounded-md border border-slate-200 bg-slate-50 p-3 space-y-3" data-testid="municipality-codes">
    <legend class="px-1 text-xs font-semibold text-slate-700">
      {{ t('admin.economySettings.municipalityCodesLabel') }}
    </legend>
    <p id="municipality-codes-hint" class="text-xs text-slate-600">{{ t('admin.economySettings.municipalityCodesHint') }}</p>

    <ul v-if="draft.length" class="flex flex-wrap gap-2" data-testid="municipality-chips">
      <li
        v-for="code in draft"
        :key="code"
        class="inline-flex items-center gap-1 rounded-full border py-0.5 pl-3 pr-1 text-sm"
        :class="names[code]?.status === 'unknown' ? 'border-amber-300 bg-amber-50 text-amber-900' : 'border-slate-300 bg-white text-gray-900'"
        :data-testid="`municipality-chip-${code}`"
      >
        <span class="font-mono">{{ code }}</span>
        <span v-if="names[code]?.status === 'found'" data-testid="municipality-name">{{ names[code].name }}</span>
        <span v-else-if="names[code]?.status === 'unknown'" class="text-xs" data-testid="municipality-unknown">
          ({{ t('admin.economySettings.municipalityCodesUnknown') }})
        </span>
        <button
          type="button"
          class="inline-flex h-11 w-11 items-center justify-center rounded-full text-slate-500 hover:bg-slate-100 hover:text-slate-800 md:h-7 md:w-7"
          :aria-label="t('admin.economySettings.municipalityCodesRemove', { code })"
          :data-testid="`municipality-remove-${code}`"
          @click="remove(code)"
        >
          <X class="h-4 w-4" aria-hidden="true" />
        </button>
      </li>
    </ul>
    <p v-else class="text-sm text-slate-600" data-testid="municipality-none">
      {{ t('admin.economySettings.municipalityCodesNone') }}
    </p>

    <div class="flex flex-col gap-3 md:flex-row md:items-center">
      <input
        v-model="input"
        type="text"
        inputmode="numeric"
        maxlength="4"
        autocomplete="off"
        :aria-label="t('admin.economySettings.municipalityCodesInput')"
        :placeholder="t('admin.economySettings.municipalityCodesInput')"
        :aria-invalid="inputError ? 'true' : undefined"
        :aria-describedby="inputError ? 'municipality-codes-hint municipality-input-error' : 'municipality-codes-hint'"
        data-testid="municipality-input"
        class="min-h-[44px] w-full rounded-md border border-slate-300 bg-white px-3 py-1.5 text-sm text-gray-900 focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500 md:min-h-0 md:w-56"
        @keydown.enter.prevent="add"
      />
      <button
        type="button"
        class="min-h-[44px] rounded-md border border-slate-300 bg-white px-4 py-1.5 text-sm font-medium text-gray-900 hover:bg-slate-100 md:min-h-0"
        data-testid="municipality-add"
        @click="add"
      >
        {{ t('admin.economySettings.municipalityCodesAdd') }}
      </button>
      <button
        type="button"
        class="min-h-[44px] rounded-md bg-brand-600 px-4 py-1.5 text-sm font-medium text-white hover:bg-brand-700 disabled:opacity-50 md:min-h-0"
        :disabled="saving"
        data-testid="municipality-save"
        @click="save"
      >
        {{ t('common.save') }}
      </button>
      <span v-if="saved" class="text-sm font-medium text-green-600">{{ t('common.saved') }}</span>
    </div>
    <p v-if="inputError" id="municipality-input-error" role="alert" class="text-xs text-red-700" data-testid="municipality-input-error">{{ inputError }}</p>
    <p class="sr-only" aria-live="polite" data-testid="municipality-announce">{{ announced }}</p>
    <p v-if="saveError" role="alert" class="rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">{{ saveError }}</p>
  </fieldset>
</template>
