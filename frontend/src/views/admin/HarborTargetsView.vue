<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Plus, FileDown, Pencil, Archive, Copy, QrCode } from 'lucide-vue-next'

const { t } = useI18n()

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
}
type PriceItem = { id: string; name: string; amount: number; category: string }

const kinds = ['guest_slip', 'motorhome', 'service'] as const

const targets = ref<Target[]>([])
const priceItems = ref<PriceItem[]>([])
const loading = ref(true)
const error = ref('')
const showArchived = ref(false)
const saving = ref(false)

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
  })
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
  })
  editing.value = true
}

const canSave = computed(() => form.label.trim() !== '' && form.price_item_id !== '')

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
  }
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
    if (!res.ok) throw new Error(`${res.status} ${await res.text().catch(() => '')}`)
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
  await Promise.all([load(), loadPriceItems()])
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
