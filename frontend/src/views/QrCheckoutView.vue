<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Loader2, Minus, Plus } from 'lucide-vue-next'
import { i18n } from '@/i18n'
import PayMethodButtons from '@/components/pay/PayMethodButtons.vue'

const { t } = useI18n()
const route = useRoute()
const token = route.params.token as string

type Target = {
  label: string
  location: string
  kind: string
  unit_label: string
  unit_amount: number
  currency: string
  min_units: number
  max_units: number
  fixed_units: boolean
  club_name: string
  label_translations?: Record<string, string>
  // Configured payment providers in display order.
  providers: string[]
}

const state = ref<'loading' | 'ready' | 'error'>('loading')
const target = ref<Target | null>(null)
const units = ref(1)
const guestName = ref('')
const busy = ref(false)
const error = ref('')

function fmt(n: number): string {
  return n.toLocaleString('nb-NO', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

// A visitor reading another language sees the target's name in it, with the
// club-language name (the one on the printed card) underneath to match up.
const translatedLabel = computed(() => {
  const name = target.value?.label_translations?.[i18n.global.locale.value]?.trim() ?? ''
  return name && name !== target.value?.label ? name : ''
})

const total = computed(() => (target.value ? target.value.unit_amount * units.value : 0))

function step(delta: number) {
  if (!target.value) return
  const next = units.value + delta
  if (next < target.value.min_units || next > target.value.max_units) return
  units.value = next
}

async function load() {
  try {
    const res = await fetch(`/api/v1/qr/${token}`)
    if (!res.ok) throw new Error(String(res.status))
    const data: Target = await res.json()
    target.value = data
    units.value = data.fixed_units ? 1 : Math.max(1, data.min_units)
    state.value = 'ready'
  } catch {
    state.value = 'error'
  }
}

async function pay(provider: string) {
  if (busy.value || !target.value) return
  busy.value = true
  error.value = ''
  try {
    const res = await fetch(`/api/v1/qr/${token}/pay`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ units: units.value, guest_name: guestName.value.trim(), provider }),
    })
    if (!res.ok) throw new Error(String(res.status))
    const data = await res.json()
    if (data.redirect_url) {
      window.location.href = data.redirect_url
      return
    }
    throw new Error('no redirect')
  } catch {
    error.value = t('qr.payError')
    busy.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="mx-auto max-w-md px-4 py-10 sm:px-6">
    <div v-if="state === 'loading'" class="space-y-4 py-16 text-center">
      <Loader2 class="mx-auto h-10 w-10 animate-spin text-slate-400" />
      <p class="text-slate-500">{{ t('qr.loading') }}</p>
    </div>

    <div v-else-if="state === 'error'" class="space-y-3 py-16 text-center">
      <h1 class="text-xl font-semibold text-slate-900">{{ t('qr.notFoundTitle') }}</h1>
      <p class="text-slate-500">{{ t('qr.notFoundBody') }}</p>
    </div>

    <div v-else-if="target" class="space-y-6">
      <div class="space-y-1 text-center">
        <p class="text-xs font-medium uppercase tracking-wide text-slate-400">{{ target.club_name }}</p>
        <template v-if="translatedLabel">
          <h1 class="text-2xl font-bold text-slate-900" data-testid="qr-heading">{{ translatedLabel }}</h1>
          <p class="text-sm text-slate-500" data-testid="qr-club-label">{{ target.label }}</p>
        </template>
        <h1 v-else class="text-2xl font-bold text-slate-900" data-testid="qr-heading">{{ target.label }}</h1>
        <p v-if="target.location" class="text-sm text-slate-500">{{ target.location }}</p>
      </div>

      <div class="rounded-lg border border-slate-200 bg-white p-4 text-center">
        <p class="text-sm text-slate-600">
          {{ fmt(target.unit_amount) }} kr <span v-if="!target.fixed_units">/ {{ target.unit_label }}</span>
        </p>
      </div>

      <!-- Units stepper (hidden for a fixed-price target) -->
      <div v-if="!target.fixed_units" class="flex items-center justify-between rounded-lg border border-slate-200 bg-white px-4 py-3">
        <span class="text-sm font-medium text-slate-700">{{ t('qr.quantityLabel', { unit: target.unit_label }) }}</span>
        <div class="flex items-center gap-4">
          <button
            class="flex h-9 w-9 items-center justify-center rounded-full border border-slate-300 text-slate-600 disabled:opacity-40"
            :disabled="units <= target.min_units"
            :aria-label="t('qr.decrease')"
            @click="step(-1)"
          >
            <Minus class="h-4 w-4" />
          </button>
          <span class="w-8 text-center text-lg font-semibold tabular-nums text-slate-900">{{ units }}</span>
          <button
            class="flex h-9 w-9 items-center justify-center rounded-full border border-slate-300 text-slate-600 disabled:opacity-40"
            :disabled="units >= target.max_units"
            :aria-label="t('qr.increase')"
            @click="step(1)"
          >
            <Plus class="h-4 w-4" />
          </button>
        </div>
      </div>

      <!-- Optional payer name -->
      <label class="block">
        <span class="mb-1 block text-xs font-medium text-slate-500">{{ t('qr.guestNameLabel') }}</span>
        <input
          v-model="guestName"
          type="text"
          :placeholder="t('qr.guestNamePlaceholder')"
          class="w-full rounded-md border border-slate-300 px-3 py-2 text-sm"
        />
      </label>

      <!-- Total -->
      <div class="rounded-lg border border-slate-200 bg-slate-50 p-4">
        <div class="flex items-center justify-between text-lg font-bold text-slate-900">
          <span>{{ t('qr.total') }}</span>
          <span class="tabular-nums">{{ fmt(total) }} kr</span>
        </div>
      </div>

      <div role="alert">
        <div v-if="error" class="rounded-md bg-red-50 p-3 text-sm text-red-800">{{ error }}</div>
      </div>

      <!-- Guests are often visitors without Vipps, so card leads here. -->
      <PayMethodButtons
        :providers="target.providers ?? []"
        emphasis="card"
        :busy="busy"
        :disabled="total <= 0"
        @pay="pay"
      />
      <p v-if="!target.providers?.length" class="text-center text-sm text-slate-500" data-testid="qr-unavailable">
        {{ t('qr.unavailable') }}
      </p>
    </div>
  </div>
</template>
