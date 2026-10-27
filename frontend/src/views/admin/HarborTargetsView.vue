<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Plus, FileDown, Pencil, Archive, Copy, QrCode, Printer } from 'lucide-vue-next'
import { CARDS_PER_PAGE, DEFAULT_CARDS_PER_PAGE, type CardsPerPage } from '@/lib/qrSheet'
import { useClubStore } from '@/stores/club'

const { t } = useI18n()
const club = useClubStore()

type Target = {
  id: string
  token: string
  label: string
  location: string
  kind: string
  price_item_id: string
  item_name: string
  unit_amount: number
  currency: string
  unit_label: string
  min_units: number
  max_units: number
  fixed_units: boolean
  archived: boolean
  public_url: string
  short_code: string
  short_url: string
}
type PriceItem = { id: string; name: string; amount: number; category: string }

const kinds = ['guest_slip', 'motorhome', 'service'] as const

const targets = ref<Target[]>([])
const priceItems = ref<PriceItem[]>([])
const loading = ref(true)
const error = ref('')
const showArchived = ref(false)
const saving = ref(false)

const selected = ref<Set<string>>(new Set())
const perPage = ref<CardsPerPage>(DEFAULT_CARDS_PER_PAGE)

const activeTargets = computed(() => targets.value.filter((tg) => !tg.archived))
const allSelected = computed(
  () => activeTargets.value.length > 0 && activeTargets.value.every((tg) => selected.value.has(tg.id)),
)

function toggleSelected(id: string) {
  const next = new Set(selected.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  selected.value = next
}

function toggleAll() {
  selected.value = allSelected.value ? new Set() : new Set(activeTargets.value.map((tg) => tg.id))
}

// Keeps list order, so the sheet prints cards in the order shown here.
function openPrint(ids: string[]) {
  if (!ids.length) return
  const q = new URLSearchParams({ ids: ids.join(','), per: String(perPage.value) })
  window.open(`/admin/harbor/payment-targets/print?${q}`, '_blank', 'noopener,noreferrer')
}

function printSelected() {
  openPrint(activeTargets.value.filter((tg) => selected.value.has(tg.id)).map((tg) => tg.id))
}

const editing = ref(false)
const form = reactive({
  id: '',
  label: '',
  location: '',
  kind: 'guest_slip',
  price_item_id: '',
  unit_label: 'natt',
  min_units: 1,
  max_units: 14,
  fixed_units: false,
  short_code: '',
})
// The code the target was loaded with, so the editor can warn when it changes.
const originalShortCode = ref('')
const shortCodeServerError = ref('')

// Mirrors the server's CHECK; the server normalises the same way.
const SHORT_CODE_PATTERN = /^[a-z0-9-]{2,24}$/
const normalisedShortCode = computed(() => form.short_code.trim().toLowerCase())
const shortCodeFormatError = computed(() =>
  normalisedShortCode.value !== '' && !SHORT_CODE_PATTERN.test(normalisedShortCode.value)
    ? t('harborTargets.field.shortCodeInvalid')
    : '',
)
const shortCodeError = computed(() => shortCodeFormatError.value || shortCodeServerError.value)
const shortCodeChanged = computed(
  () => form.id !== '' && normalisedShortCode.value !== '' && normalisedShortCode.value !== originalShortCode.value,
)

// The server builds short_url from its own public base URL, so an existing
// target's address is the best source for the prefix; the club domain covers
// a club with no targets yet.
const shortUrlPrefix = computed(() => {
  const sample = targets.value.find((tg) => tg.short_url && tg.short_code)
  if (sample) {
    return sample.short_url.replace(/^https?:\/\//, '').slice(0, -sample.short_code.length)
  }
  return `${club.domain || window.location.host}/q/`
})

function fmt(n: number): string {
  return n.toLocaleString('nb-NO', { minimumFractionDigits: 0, maximumFractionDigits: 2 })
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const res = await fetch(`/api/v1/harbor/payment-targets?include_archived=${showArchived.value}`, {
      credentials: 'include',
    })
    if (!res.ok) throw new Error(String(res.status))
    targets.value = await res.json()
    const live = new Set(targets.value.filter((tg) => !tg.archived).map((tg) => tg.id))
    selected.value = new Set([...selected.value].filter((id) => live.has(id)))
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    loading.value = false
  }
}

async function loadPriceItems() {
  try {
    const res = await fetch('/api/v1/admin/pricing', { credentials: 'include' })
    if (!res.ok) return
    const body = await res.json()
    priceItems.value = (body.items ?? body ?? []) as PriceItem[]
  } catch {
    /* non-fatal */
  }
}

function openCreate() {
  Object.assign(form, {
    id: '',
    label: '',
    location: '',
    kind: 'guest_slip',
    price_item_id: priceItems.value[0]?.id ?? '',
    unit_label: 'natt',
    min_units: 1,
    max_units: 14,
    fixed_units: false,
    short_code: '',
  })
  originalShortCode.value = ''
  shortCodeServerError.value = ''
  editing.value = true
}

function openEdit(tg: Target) {
  Object.assign(form, {
    id: tg.id,
    label: tg.label,
    location: tg.location,
    kind: tg.kind,
    price_item_id: tg.price_item_id,
    unit_label: tg.unit_label,
    min_units: tg.min_units,
    max_units: tg.max_units,
    fixed_units: tg.fixed_units,
    short_code: tg.short_code,
  })
  originalShortCode.value = tg.short_code
  shortCodeServerError.value = ''
  editing.value = true
}

const canSave = computed(
  () => form.label.trim() !== '' && form.price_item_id !== '' && shortCodeFormatError.value === '',
)

async function save() {
  if (!canSave.value || saving.value) return
  saving.value = true
  error.value = ''
  const payload = {
    label: form.label.trim(),
    location: form.location.trim(),
    kind: form.kind,
    price_item_id: form.price_item_id,
    unit_label: form.unit_label.trim() || 'natt',
    min_units: Number(form.min_units) || 1,
    max_units: Number(form.max_units) || 1,
    fixed_units: form.fixed_units,
    short_code: normalisedShortCode.value,
  }
  shortCodeServerError.value = ''
  try {
    const url = form.id
      ? `/api/v1/harbor/payment-targets/${form.id}`
      : '/api/v1/harbor/payment-targets'
    const res = await fetch(url, {
      method: form.id ? 'PATCH' : 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    })
    if (!res.ok) {
      const text = await res.text().catch(() => '')
      let code = ''
      try {
        code = (JSON.parse(text) as { code?: string }).code ?? ''
      } catch {
        /* not JSON */
      }
      if (code === 'short_code_taken') {
        shortCodeServerError.value = t('harborTargets.field.shortCodeTaken')
        return
      }
      if (code === 'short_code_invalid') {
        shortCodeServerError.value = t('harborTargets.field.shortCodeInvalid')
        return
      }
      throw new Error(`${res.status} ${text}`)
    }
    editing.value = false
    await load()
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    saving.value = false
  }
}

async function archive(tg: Target) {
  if (!confirm(t('harborTargets.archiveConfirm', { label: tg.label }))) return
  try {
    const res = await fetch(`/api/v1/harbor/payment-targets/${tg.id}/archive`, {
      method: 'POST',
      credentials: 'include',
    })
    if (!res.ok) throw new Error(String(res.status))
    await load()
  } catch (e) {
    error.value = (e as Error).message
  }
}

async function copyUrl(url: string) {
  try {
    await navigator.clipboard.writeText(url)
  } catch {
    /* clipboard unavailable */
  }
}

onMounted(async () => {
  await Promise.all([load(), loadPriceItems(), club.ensureLoaded()])
})
</script>

<template>
  <div class="mx-auto max-w-4xl">
    <div class="mb-6 flex items-center justify-between">
      <div>
        <h1 class="text-xl font-bold text-slate-900">{{ t('harborTargets.title') }}</h1>
        <p class="mt-1 text-sm text-slate-500">{{ t('harborTargets.subtitle') }}</p>
      </div>
      <button
        class="flex items-center gap-1.5 rounded-md bg-slate-900 px-3 py-2 text-sm font-medium text-white hover:bg-slate-700"
        @click="openCreate"
      >
        <Plus class="h-4 w-4" /> {{ t('harborTargets.new') }}
      </button>
    </div>

    <label class="mb-4 flex items-center gap-2 text-sm text-slate-600">
      <input v-model="showArchived" type="checkbox" class="rounded border-slate-300" @change="load" />
      {{ t('harborTargets.showArchived') }}
    </label>

    <div
      v-if="activeTargets.length"
      class="mb-4 flex flex-wrap items-center gap-3 rounded-lg border border-slate-200 bg-slate-50 px-3 py-2 text-sm"
    >
      <label class="flex items-center gap-2 text-slate-700">
        <input
          type="checkbox"
          class="rounded border-slate-300"
          :checked="allSelected"
          data-testid="select-all"
          @change="toggleAll"
        />
        {{ t('harborTargets.print.selectAll') }}
      </label>
      <label class="ml-auto flex items-center gap-2 text-slate-600">
        {{ t('harborTargets.print.perPage') }}
        <select v-model.number="perPage" class="rounded-md border border-slate-300 px-2 py-1 text-sm" data-testid="per-page">
          <option v-for="n in CARDS_PER_PAGE" :key="n" :value="n">{{ n }}</option>
        </select>
      </label>
      <button
        class="flex items-center gap-1.5 rounded-md bg-slate-900 px-3 py-1.5 text-sm font-medium text-white hover:bg-slate-700 disabled:opacity-50"
        :disabled="selected.size === 0"
        data-testid="print-selected"
        @click="printSelected"
      >
        <Printer class="h-4 w-4" /> {{ t('harborTargets.print.printSelected', { n: selected.size }) }}
      </button>
    </div>

    <p v-if="error" class="mb-4 rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">{{ error }}</p>
    <p v-if="loading" class="text-sm text-slate-500">{{ t('common.loading') }}…</p>

    <p v-else-if="!targets.length" class="rounded-md border border-dashed border-slate-300 p-8 text-center text-sm text-slate-500">
      {{ t('harborTargets.empty') }}
    </p>

    <ul v-else class="space-y-3">
      <li
        v-for="tg in targets"
        :key="tg.id"
        class="flex flex-col gap-3 rounded-lg border border-slate-200 bg-white p-4 sm:flex-row sm:items-center"
        :class="tg.archived ? 'opacity-60' : ''"
      >
        <input
          v-if="!tg.archived"
          type="checkbox"
          class="shrink-0 rounded border-slate-300"
          :checked="selected.has(tg.id)"
          :aria-label="tg.label"
          data-testid="select-target"
          @change="toggleSelected(tg.id)"
        />
        <img
          :src="`/api/v1/harbor/payment-targets/${tg.id}/qr.png`"
          :alt="tg.label"
          class="h-20 w-20 shrink-0 rounded border border-slate-100"
        />
        <div class="min-w-0 flex-1">
          <div class="flex items-center gap-2">
            <span class="font-semibold text-slate-900">{{ tg.label }}</span>
            <span class="rounded-full bg-slate-100 px-2 py-0.5 text-xs text-slate-600">{{ t('harborTargets.kind.' + tg.kind) }}</span>
            <span v-if="tg.archived" class="rounded-full bg-amber-100 px-2 py-0.5 text-xs text-amber-700">{{ t('harborTargets.archived') }}</span>
          </div>
          <p v-if="tg.location" class="text-sm text-slate-500">{{ tg.location }}</p>
          <p class="text-sm text-slate-600">
            {{ tg.item_name }} — {{ fmt(tg.unit_amount) }} kr<span v-if="!tg.fixed_units"> / {{ tg.unit_label }} ({{ tg.min_units }}–{{ tg.max_units }})</span>
          </p>
          <button class="mt-1 flex items-center gap-1 text-xs text-slate-400 hover:text-slate-700" @click="copyUrl(tg.public_url)">
            <Copy class="h-3 w-3" /> {{ tg.public_url }}
          </button>
          <button
            v-if="tg.short_url"
            class="flex items-center gap-1 font-mono text-xs text-slate-500 hover:text-slate-800"
            data-testid="short-url"
            @click="copyUrl(tg.short_url)"
          >
            <Copy class="h-3 w-3" /> {{ tg.short_url }}
          </button>
        </div>
        <div v-if="!tg.archived" class="flex shrink-0 items-center gap-1">
          <a
            :href="`/api/v1/harbor/payment-targets/${tg.id}/poster.pdf`"
            target="_blank"
            rel="noopener"
            class="flex items-center gap-1 rounded-md border border-slate-300 px-2.5 py-1.5 text-xs font-medium text-slate-700 hover:bg-slate-50"
          >
            <FileDown class="h-3.5 w-3.5" /> {{ t('harborTargets.poster') }}
          </a>
          <button
            class="rounded-md border border-slate-300 p-1.5 text-slate-600 hover:bg-slate-50"
            :title="t('harborTargets.print.printOne')"
            :aria-label="t('harborTargets.print.printOne')"
            data-testid="print-one"
            @click="openPrint([tg.id])"
          >
            <Printer class="h-3.5 w-3.5" />
          </button>
          <button class="rounded-md border border-slate-300 p-1.5 text-slate-600 hover:bg-slate-50" :title="t('common.edit')" @click="openEdit(tg)">
            <Pencil class="h-3.5 w-3.5" />
          </button>
          <button class="rounded-md border border-slate-300 p-1.5 text-slate-600 hover:bg-red-50 hover:text-red-600" :title="t('harborTargets.archiveAction')" @click="archive(tg)">
            <Archive class="h-3.5 w-3.5" />
          </button>
        </div>
      </li>
    </ul>

    <!-- Create / edit panel -->
    <div v-if="editing" class="fixed inset-0 z-50 flex items-end justify-center bg-black/40 sm:items-center" @click.self="editing = false">
      <div class="w-full max-w-lg rounded-t-2xl bg-white p-6 shadow-xl sm:rounded-2xl">
        <h2 class="mb-4 flex items-center gap-2 text-lg font-bold text-slate-900">
          <QrCode class="h-5 w-5 text-slate-400" />
          {{ form.id ? t('harborTargets.editTitle') : t('harborTargets.new') }}
        </h2>

        <div class="space-y-3">
          <label class="block">
            <span class="mb-1 block text-xs font-medium text-slate-500">{{ t('harborTargets.field.label') }}</span>
            <input v-model="form.label" type="text" class="w-full rounded-md border border-slate-300 px-3 py-2 text-sm" />
          </label>
          <label class="block">
            <span class="mb-1 block text-xs font-medium text-slate-500">{{ t('harborTargets.field.location') }}</span>
            <input v-model="form.location" type="text" class="w-full rounded-md border border-slate-300 px-3 py-2 text-sm" />
          </label>
          <div>
            <label for="harbor-target-short-code" class="mb-1 block text-xs font-medium text-slate-500">
              {{ t('harborTargets.field.shortCode') }}
            </label>
            <div
              class="flex items-center rounded-md border text-sm focus-within:ring-1"
              :class="shortCodeError ? 'border-red-400 focus-within:ring-red-400' : 'border-slate-300 focus-within:ring-slate-400'"
            >
              <span class="select-none whitespace-nowrap pl-3 font-mono text-slate-400" data-testid="short-code-prefix">{{ shortUrlPrefix }}</span>
              <input
                id="harbor-target-short-code"
                v-model="form.short_code"
                type="text"
                autocapitalize="off"
                autocomplete="off"
                spellcheck="false"
                maxlength="24"
                :placeholder="t('harborTargets.field.shortCodePlaceholder')"
                :aria-invalid="shortCodeError !== ''"
                aria-describedby="harbor-target-short-code-help"
                class="min-w-0 flex-1 rounded-r-md border-0 bg-transparent py-2 pr-3 font-mono focus:outline-none"
                data-testid="short-code-input"
                @input="shortCodeServerError = ''"
              />
            </div>
            <div id="harbor-target-short-code-help">
              <p v-if="shortCodeError" class="mt-1 text-xs text-red-600" data-testid="short-code-error">{{ shortCodeError }}</p>
              <p v-else class="mt-1 text-xs text-slate-500">
                {{ form.id ? t('harborTargets.field.shortCodeHint') : t('harborTargets.field.shortCodeHintNew') }}
              </p>
              <p
                v-if="form.id"
                class="mt-1 text-xs"
                :class="shortCodeChanged ? 'font-medium text-amber-700' : 'text-slate-500'"
                data-testid="short-code-warning"
              >
                {{ t('harborTargets.field.shortCodeChangeWarning') }}
              </p>
            </div>
          </div>
          <div class="grid grid-cols-2 gap-3">
            <label class="block">
              <span class="mb-1 block text-xs font-medium text-slate-500">{{ t('harborTargets.field.kind') }}</span>
              <select v-model="form.kind" class="w-full rounded-md border border-slate-300 px-3 py-2 text-sm">
                <option v-for="k in kinds" :key="k" :value="k">{{ t('harborTargets.kind.' + k) }}</option>
              </select>
            </label>
            <label class="block">
              <span class="mb-1 block text-xs font-medium text-slate-500">{{ t('harborTargets.field.priceItem') }}</span>
              <select v-model="form.price_item_id" class="w-full rounded-md border border-slate-300 px-3 py-2 text-sm">
                <option v-for="p in priceItems" :key="p.id" :value="p.id">{{ p.name }} ({{ fmt(p.amount) }} kr)</option>
              </select>
            </label>
          </div>

          <label class="flex items-center gap-2 text-sm text-slate-700">
            <input v-model="form.fixed_units" type="checkbox" class="rounded border-slate-300" />
            {{ t('harborTargets.field.fixedUnits') }}
          </label>

          <div v-if="!form.fixed_units" class="grid grid-cols-3 gap-3">
            <label class="block">
              <span class="mb-1 block text-xs font-medium text-slate-500">{{ t('harborTargets.field.unitLabel') }}</span>
              <input v-model="form.unit_label" type="text" class="w-full rounded-md border border-slate-300 px-3 py-2 text-sm" />
            </label>
            <label class="block">
              <span class="mb-1 block text-xs font-medium text-slate-500">{{ t('harborTargets.field.minUnits') }}</span>
              <input v-model.number="form.min_units" type="number" min="1" class="w-full rounded-md border border-slate-300 px-3 py-2 text-sm" />
            </label>
            <label class="block">
              <span class="mb-1 block text-xs font-medium text-slate-500">{{ t('harborTargets.field.maxUnits') }}</span>
              <input v-model.number="form.max_units" type="number" min="1" class="w-full rounded-md border border-slate-300 px-3 py-2 text-sm" />
            </label>
          </div>
        </div>

        <div class="mt-6 flex justify-end gap-2">
          <button class="rounded-md px-4 py-2 text-sm font-medium text-slate-600 hover:bg-slate-100" @click="editing = false">
            {{ t('common.cancel') }}
          </button>
          <button
            class="rounded-md bg-slate-900 px-4 py-2 text-sm font-medium text-white hover:bg-slate-700 disabled:opacity-50"
            :disabled="!canSave || saving"
            @click="save"
          >
            {{ saving ? t('common.saving') : t('common.save') }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
