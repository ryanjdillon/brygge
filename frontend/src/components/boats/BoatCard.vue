<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { Pencil, Trash2, ShieldCheck, AlertTriangle } from 'lucide-vue-next'
import { formatSlip } from '@/lib/slipSort'

export interface BoatCardBoat {
  id: string
  name?: string
  type?: string
  manufacturer?: string
  model?: string
  length_m?: number | null
  beam_m?: number | null
  draft_m?: number | null
  weight_kg?: number | null
  registration_number?: string
  measurements_confirmed?: boolean
  slip?: { slip_id: string; section: string; number: string; assignment_type: string } | null
}

defineProps<{
  boat: BoatCardBoat
  /** Show edit/delete action buttons. */
  actions?: boolean
  /** Compact density used inside small modals. */
  compact?: boolean
  /** Suppress the auto-rendered slip chip — the parent supplies its own
      via the #slip slot (admin per-boat picker). */
  hideSlip?: boolean
}>()

const emit = defineEmits<{
  (e: 'edit', boat: BoatCardBoat): void
  (e: 'delete', boatId: string): void
}>()

const { t } = useI18n()
const fmtDim = (v?: number | null): string => (v != null ? `${v} m` : '—')
</script>

<template>
  <div :class="['rounded-lg border border-gray-200 bg-white', compact ? 'p-2.5' : 'p-4']">
    <div class="flex items-start justify-between gap-2">
      <div class="min-w-0">
        <div class="flex flex-wrap items-center gap-2">
          <span :class="['font-semibold text-gray-900', compact ? 'text-sm' : 'text-lg']">
            {{ boat.name || [boat.manufacturer, boat.model].filter(Boolean).join(' ') || '—' }}
          </span>
          <span
            v-if="boat.measurements_confirmed"
            class="inline-flex items-center gap-1 rounded-full bg-green-100 px-2 py-0.5 text-xs font-medium text-green-800"
          >
            <ShieldCheck class="h-3 w-3" />
            {{ t('portal.boats.confirmed') }}
          </span>
          <span
            v-else
            class="inline-flex items-center gap-1 rounded-full bg-yellow-100 px-2 py-0.5 text-xs font-medium text-yellow-800"
          >
            <AlertTriangle class="h-3 w-3" />
            {{ t('portal.boats.pendingConfirmation') }}
          </span>
        </div>
        <p
          v-if="(boat.manufacturer || boat.model) && boat.name"
          :class="['text-gray-500', compact ? 'text-xs' : 'mt-0.5 text-sm']"
        >
          {{ [boat.manufacturer, boat.model].filter(Boolean).join(' ') }}
        </p>
      </div>
      <div v-if="actions" class="flex shrink-0 gap-2">
        <button
          class="text-blue-600 hover:text-blue-800"
          :title="t('common.edit')"
          @click="emit('edit', boat)"
        >
          <Pencil :class="compact ? 'h-3.5 w-3.5' : 'h-4 w-4'" />
        </button>
        <button
          class="text-red-600 hover:text-red-800"
          :title="t('common.delete')"
          @click="emit('delete', boat.id)"
        >
          <Trash2 :class="compact ? 'h-3.5 w-3.5' : 'h-4 w-4'" />
        </button>
      </div>
    </div>
    <div :class="['grid grid-cols-2 gap-x-4 gap-y-0.5 sm:grid-cols-4', compact ? 'mt-1.5 text-xs' : 'mt-3 text-sm']">
      <div>
        <span class="text-gray-500">{{ t('portal.boats.length') }}:</span>
        <span class="ml-1 text-gray-900">{{ fmtDim(boat.length_m) }}</span>
      </div>
      <div>
        <span class="text-gray-500">{{ t('portal.boats.beam') }}:</span>
        <span class="ml-1 text-gray-900">{{ fmtDim(boat.beam_m) }}</span>
      </div>
      <div>
        <span class="text-gray-500">{{ t('portal.boats.draft') }}:</span>
        <span class="ml-1 text-gray-900">{{ fmtDim(boat.draft_m) }}</span>
      </div>
      <div v-if="boat.weight_kg">
        <span class="text-gray-500">{{ t('portal.boats.weight') }}:</span>
        <span class="ml-1 text-gray-900">{{ boat.weight_kg }} kg</span>
      </div>
    </div>
    <div
      v-if="boat.registration_number"
      :class="['text-gray-500', compact ? 'mt-0.5 text-xs' : 'mt-1 text-sm']"
    >
      {{ t('portal.boats.registrationNumber') }}: {{ boat.registration_number }}
    </div>
    <div
      v-if="boat.slip && !hideSlip"
      :class="['text-gray-700', compact ? 'mt-0.5 text-xs' : 'mt-1 text-sm']"
    >
      <span class="text-gray-500">{{ t('admin.users.spots') }}:</span>
      <span class="ml-1 font-mono">{{ formatSlip(boat.slip) }}</span>
      <span class="ml-1 text-xs text-gray-500">
        ({{ t('admin.users.spot' + (boat.slip.assignment_type === 'seasonal' ? 'Seasonal' : 'Permanent')) }})
      </span>
    </div>
    <slot name="slip" />
  </div>
</template>
