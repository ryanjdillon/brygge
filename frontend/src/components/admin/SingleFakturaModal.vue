<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { X, Plus, Trash2 } from 'lucide-vue-next'
import { useTotpGateStore } from '@/stores/totpGate'
import { useAuthStore } from '@/stores/auth'
import { useAccountsList } from '@/composables/useAccounting'
import MemberSearch, { type MemberHit } from '@/components/members/MemberSearch.vue'

defineProps<{
  open: boolean
}>()
const emit = defineEmits<{
  (e: 'close'): void
  (e: 'created', invoiceId: string): void
}>()

const { t } = useI18n()
const auth = useAuthStore()
const totpGate = useTotpGateStore()

const { data: accounts } = useAccountsList()
const revenueAccounts = computed(() =>
  (accounts.value ?? []).filter((a) => a.is_active && (a.account_type === 'revenue' || a.account_type === 'liability')),
)

interface LineDraft {
  description: string
  sub_description: string
  quantity: number
  unit_price: number
  account_id: string
}

const member = ref<MemberHit | null>(null)
const dueDate = ref(defaultDueDate())
const lines = ref<LineDraft[]>([
  { description: '', sub_description: '', quantity: 1, unit_price: 0, account_id: '' },
])
const submitting = ref(false)
const error = ref<string | null>(null)

function defaultDueDate(): string {
  const d = new Date()
  d.setDate(d.getDate() + 21)
  return d.toISOString().slice(0, 10)
}

function addLine() {
  const last = lines.value[lines.value.length - 1]
  lines.value.push({
    description: last?.description ?? '',
    sub_description: '',
    quantity: 1,
    unit_price: last?.unit_price ?? 0,
    account_id: last?.account_id ?? '',
  })
}

function removeLine(i: number) {
  if (lines.value.length === 1) return
  lines.value.splice(i, 1)
}

const total = computed(() =>
  lines.value.reduce((s, l) => s + Number(l.quantity || 0) * Number(l.unit_price || 0), 0),
)

function formatNOK(n: number): string {
  return new Intl.NumberFormat('nb-NO', { style: 'currency', currency: 'NOK' }).format(n)
}

async function ensureFreshTotp(): Promise<boolean> {
  if (auth.hasFreshTotp) return true
  return totpGate.open()
}

function reset() {
  member.value = null
  dueDate.value = defaultDueDate()
  lines.value = [{ description: '', sub_description: '', quantity: 1, unit_price: 0, account_id: '' }]
  error.value = null
}

async function submit() {
  error.value = null
  if (!member.value) {
    error.value = t('admin.singleFaktura.memberRequired')
    return
  }
  if (lines.value.some((l) => !l.description.trim() || l.quantity < 1 || l.unit_price <= 0)) {
    error.value = t('admin.singleFaktura.linesInvalid')
    return
  }
  if (lines.value.some((l) => !l.account_id)) {
    error.value = t('admin.singleFaktura.accountRequired')
    return
  }
  if (!(await ensureFreshTotp())) return
  submitting.value = true
  try {
    const res = await fetch('/api/v1/admin/financials/invoices/full', {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        user_id: member.value.id,
        due_date: dueDate.value,
        lines: lines.value.map((l) => ({
          description: l.description.trim(),
          sub_description: l.sub_description.trim() || undefined,
          quantity: Number(l.quantity),
          unit_price: Number(l.unit_price),
          account_id: l.account_id,
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
</script>

<template>
  <div
    v-if="open"
    class="fixed inset-0 z-40 flex items-center justify-center bg-black/50 p-4"
    role="dialog"
    aria-modal="true"
    @keydown.esc="emit('close')"
  >
    <div class="w-full max-w-2xl rounded-lg bg-white p-5 shadow-xl">
      <div class="flex items-center justify-between border-b border-gray-100 pb-3">
        <h2 class="text-lg font-semibold text-gray-900">{{ t('admin.singleFaktura.title') }}</h2>
        <button type="button" class="text-gray-400 hover:text-gray-600" @click="emit('close')">
          <X class="h-5 w-5" />
        </button>
      </div>

      <form class="mt-4 space-y-4" @submit.prevent="submit">
        <div>
          <label class="block text-xs font-medium text-gray-700">{{ t('admin.singleFaktura.member') }}</label>
          <MemberSearch v-model="member" :placeholder="t('admin.singleFaktura.memberPlaceholder')" class="mt-1" />
        </div>

        <div>
          <label class="block text-xs font-medium text-gray-700">{{ t('admin.singleFaktura.dueDate') }}</label>
          <input
            v-model="dueDate"
            type="date"
            required
            class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm"
          />
        </div>

        <div>
          <div class="flex items-center justify-between">
            <label class="text-xs font-medium text-gray-700">{{ t('admin.singleFaktura.lines') }}</label>
            <button
              type="button"
              class="inline-flex items-center gap-1 rounded-md border border-gray-300 bg-white px-2 py-1 text-xs hover:bg-gray-50"
              @click="addLine"
            >
              <Plus class="h-3.5 w-3.5" /> {{ t('admin.singleFaktura.addLine') }}
            </button>
          </div>
          <div class="mt-2 space-y-3">
            <div v-for="(l, i) in lines" :key="i" class="rounded-md border border-gray-200 p-3">
              <div class="grid grid-cols-12 gap-2">
                <input
                  v-model="l.description"
                  type="text"
                  :placeholder="t('admin.singleFaktura.descriptionPlaceholder')"
                  class="col-span-7 rounded-md border border-gray-300 px-2 py-1 text-sm"
                />
                <input
                  v-model.number="l.quantity"
                  type="number"
                  min="1"
                  class="col-span-2 rounded-md border border-gray-300 px-2 py-1 text-sm tabular-nums"
                  :title="t('admin.singleFaktura.quantity')"
                />
                <input
                  v-model.number="l.unit_price"
                  type="number"
                  min="0"
                  step="0.01"
                  class="col-span-2 rounded-md border border-gray-300 px-2 py-1 text-sm tabular-nums"
                  :title="t('admin.singleFaktura.unitPrice')"
                />
                <button
                  type="button"
                  class="col-span-1 text-red-500 hover:text-red-700 disabled:opacity-30"
                  :disabled="lines.length === 1"
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
                  class="col-span-7 rounded-md border border-gray-200 px-2 py-1 text-xs text-gray-700"
                />
                <select
                  v-model="l.account_id"
                  required
                  class="col-span-5 rounded-md border border-gray-300 px-2 py-1 text-xs"
                >
                  <option value="">{{ t('admin.singleFaktura.accountPlaceholder') }}</option>
                  <option v-for="a in revenueAccounts" :key="a.id" :value="a.id">
                    {{ a.code }} — {{ a.name }}
                  </option>
                </select>
              </div>
            </div>
          </div>
          <p class="mt-2 text-right text-sm font-semibold text-gray-700">
            {{ t('admin.singleFaktura.total') }}: {{ formatNOK(total) }}
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
    </div>
  </div>
</template>
