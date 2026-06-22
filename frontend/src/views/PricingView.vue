<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { usePricing } from '@/composables/usePricing'

const { t } = useI18n()
const { categories, isLoading, isError, unitLabel } = usePricing()

function formatAmount(amount: number): string {
  return amount.toLocaleString('nb-NO')
}

function seasonLabel(metadata: Record<string, string> | unknown): string | null {
  if (!metadata || typeof metadata !== 'object') return null
  const m = metadata as Record<string, string>
  if (!m.period_start || !m.period_end) return null
  return `${m.period_start} – ${m.period_end}`
}
</script>

<template>
  <div class="mx-auto max-w-7xl px-4 py-12 sm:px-6 lg:px-8">
    <h1 class="text-3xl font-bold text-slate-900">{{ t('pricing.title') }}</h1>

    <div v-if="isLoading" class="mt-8 grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
      <div
        v-for="i in 6"
        :key="i"
        class="animate-pulse rounded-2xl border border-slate-200 p-6"
      >
        <div class="h-5 w-32 rounded bg-slate-200" />
        <div class="mt-4 space-y-2">
          <div class="h-4 w-full rounded bg-slate-200" />
          <div class="h-4 w-3/4 rounded bg-slate-200" />
        </div>
      </div>
    </div>

    <div v-else-if="isError" class="mt-8 rounded-lg border border-red-200 bg-red-50 p-6 text-red-700">
      {{ t('pricing.error') }}
    </div>

    <div v-else-if="!categories.length" class="mt-8 text-center text-slate-500">
      {{ t('pricing.noPricing') }}
    </div>

    <div v-else class="mt-8 grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
      <div
        v-for="cat in categories"
        :key="cat.key"
        class="rounded-2xl border border-slate-200 bg-white p-6"
      >
        <h2 class="text-lg font-semibold text-slate-900">{{ cat.label }}</h2>

        <!-- Split header when at least one row has member/non-member prices -->
        <div v-if="cat.hasSplit" class="mt-3 flex justify-end gap-4 text-xs font-semibold uppercase tracking-wide text-slate-400">
          <span class="w-20 text-right">{{ t('pricing.member') }}</span>
          <span class="w-20 text-right">{{ t('pricing.nonMember') }}</span>
        </div>

        <ul class="mt-2 space-y-3">
          <li
            v-for="row in cat.rows"
            :key="row.id"
            class="border-b border-slate-100 pb-3 last:border-0 last:pb-0"
          >
            <!-- Paired member/non-member row -->
            <template v-if="row.memberAmount !== undefined || row.nonMemberAmount !== undefined">
              <div class="flex items-baseline justify-between gap-2">
                <span class="flex-1 text-slate-700">{{ row.name }}</span>
                <span class="w-20 text-right font-semibold text-slate-900">
                  <template v-if="row.memberAmount !== undefined">
                    {{ formatAmount(row.memberAmount) }} kr
                  </template>
                  <span v-else class="text-slate-300">—</span>
                </span>
                <span class="w-20 text-right font-semibold text-slate-900">
                  <template v-if="row.nonMemberAmount !== undefined">
                    {{ formatAmount(row.nonMemberAmount) }} kr
                  </template>
                  <span v-else class="text-slate-300">—</span>
                </span>
              </div>
              <p v-if="row.description" class="mt-0.5 text-sm text-slate-500">{{ row.description }}</p>
              <p v-if="unitLabel(row.unit)" class="mt-0.5 text-xs text-slate-400">{{ unitLabel(row.unit) }}</p>
            </template>

            <!-- Standard single-price row -->
            <template v-else>
              <div class="flex items-baseline justify-between">
                <span class="text-slate-700">{{ row.name }}</span>
                <span class="font-semibold text-slate-900">
                  {{ formatAmount(row.allAmount ?? 0) }} kr
                  <span class="text-sm font-normal text-slate-500">{{ unitLabel(row.unit) }}</span>
                </span>
              </div>
              <p v-if="row.description" class="mt-0.5 text-sm text-slate-500">
                {{ row.description }}
              </p>
              <p v-if="seasonLabel(row.metadata)" class="mt-0.5 text-xs text-slate-400">
                {{ t('pricing.period') }}: {{ seasonLabel(row.metadata) }}
              </p>
              <p v-if="row.installments_allowed" class="mt-0.5 text-xs text-brand-600">
                {{ t('pricing.installments', { max: row.max_installments }) }}
              </p>
            </template>
          </li>
        </ul>
      </div>
    </div>
  </div>
</template>
