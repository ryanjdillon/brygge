<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useApi } from '@/composables/useApi'
import { useCan } from '@/composables/useCan'
import { formatNOK as nok } from '@/lib/format'
import { UserPlus, Tag, Trash2, RefreshCw, Inbox } from 'lucide-vue-next'

const { fetchApi } = useApi()
// Aliases and rules are ledger settings; viewers without the role read them only (BRY-372).
const { can } = useCan()
const canEdit = can('ledger')

interface QueueRow {
  id: string
  tx_at?: string
  booking_date?: string
  amount: number
  customer_name: string
  message: string
  msn: string
  import_id: string
}

interface CustomerAlias {
  id: string
  customer_name: string
  user_id: string
  created_at: string
}

interface MessageRule {
  id: string
  pattern: string
  account_code: string
  priority: number
  created_at: string
}

interface Member {
  id: string
  full_name: string
  email: string
}

// ── State ───────────────────────────────────────────────────────────────────

const loading = ref(false)
const error = ref<string | null>(null)
const queue = ref<QueueRow[]>([])
const aliases = ref<CustomerAlias[]>([])
const rules = ref<MessageRule[]>([])
const members = ref<Member[]>([])

// Modal state
const aliasModal = ref<{ row: QueueRow } | null>(null)
const ruleModal = ref<{ row: QueueRow } | null>(null)
const aliasUserID = ref('')
const rulePattern = ref('')
const ruleAccountCode = ref('')
const rulePriority = ref(0)
const saving = ref(false)

// ── Load ────────────────────────────────────────────────────────────────────

async function load() {
  loading.value = true
  error.value = null
  try {
    const [q, a, r, m] = await Promise.all([
      fetchApi<QueueRow[]>('/api/v1/admin/accounting/vipps/queue'),
      fetchApi<CustomerAlias[]>('/api/v1/admin/accounting/vipps/customer-aliases'),
      fetchApi<MessageRule[]>('/api/v1/admin/accounting/vipps/message-rules'),
      fetchApi<{ users?: Member[] } | Member[]>('/api/v1/admin/users'),
    ])
    queue.value = q ?? []
    aliases.value = a ?? []
    rules.value = r ?? []
    members.value = (Array.isArray(m) ? m : (m?.users ?? [])) as Member[]
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    loading.value = false
  }
}

onMounted(load)

// ── Alias modal ─────────────────────────────────────────────────────────────

function openAliasModal(row: QueueRow) {
  aliasModal.value = { row }
  aliasUserID.value = ''
}

async function saveAlias() {
  if (!aliasModal.value || !aliasUserID.value) return
  saving.value = true
  try {
    await fetchApi('/api/v1/admin/accounting/vipps/customer-aliases', {
      method: 'POST',
      body: JSON.stringify({
        customer_name: aliasModal.value.row.customer_name,
        user_id: aliasUserID.value,
      }),
    })
    aliasModal.value = null
    await load()
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    saving.value = false
  }
}

async function deleteAlias(id: string) {
  await fetchApi(`/api/v1/admin/accounting/vipps/customer-aliases/${id}`, { method: 'DELETE' })
  await load()
}

// ── Rule modal ───────────────────────────────────────────────────────────────

function openRuleModal(row: QueueRow) {
  ruleModal.value = { row }
  rulePattern.value = row.message
  ruleAccountCode.value = '3900'
  rulePriority.value = 0
}

async function saveRule() {
  if (!ruleModal.value || !rulePattern.value || !ruleAccountCode.value) return
  saving.value = true
  try {
    await fetchApi('/api/v1/admin/accounting/vipps/message-rules', {
      method: 'POST',
      body: JSON.stringify({
        pattern: rulePattern.value,
        account_code: ruleAccountCode.value,
        priority: rulePriority.value,
      }),
    })
    ruleModal.value = null
    await load()
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    saving.value = false
  }
}

async function deleteRule(id: string) {
  await fetchApi(`/api/v1/admin/accounting/vipps/message-rules/${id}`, { method: 'DELETE' })
  await load()
}

// ── Helpers ──────────────────────────────────────────────────────────────────

const memberOptions = computed(() =>
  members.value.map((m) => ({ value: m.id, label: `${m.full_name} (${m.email})` })),
)

function aliasForRow(row: QueueRow): CustomerAlias | undefined {
  return aliases.value.find(
    (a) => a.customer_name.toLowerCase() === row.customer_name.toLowerCase(),
  )
}

function dateLabel(row: QueueRow): string {
  return row.booking_date ?? row.tx_at?.slice(0, 10) ?? ''
}
</script>

<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between">
      <div>
        <h1 class="text-2xl font-bold text-gray-900">Vipps-kø</h1>
        <p class="mt-1 text-sm text-gray-500">
          Uklassifiserte Vipps-innbetalingar som treng gjennomgang.
        </p>
      </div>
      <button
        class="inline-flex items-center gap-2 rounded-md border border-gray-300 bg-white px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
        @click="load"
      >
        <RefreshCw class="h-4 w-4" :class="{ 'animate-spin': loading }" />
        Oppdater
      </button>
    </div>

    <div v-if="error" class="rounded-md border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
      {{ error }}
    </div>

    <!-- Queue -->
    <section>
      <h2 class="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500">
        Uklassifiserte innbetalingar ({{ queue.length }})
      </h2>

      <div v-if="queue.length === 0 && !loading" class="flex flex-col items-center gap-2 rounded-lg border border-dashed border-gray-300 py-12 text-gray-400">
        <Inbox class="h-8 w-8" />
        <p class="text-sm">Ingen rader i køen — alt er klassifisert.</p>
      </div>

      <div v-else class="overflow-x-auto rounded-lg border border-gray-200 bg-white">
        <table class="min-w-full divide-y divide-gray-200 text-sm">
          <thead class="bg-gray-50 text-left text-xs font-medium uppercase tracking-wide text-gray-500">
            <tr>
              <th class="px-3 py-2">Dato</th>
              <th class="px-3 py-2">Betalar</th>
              <th class="px-3 py-2">Melding</th>
              <th class="px-3 py-2 text-right">Beløp</th>
              <th class="px-3 py-2">Status</th>
              <th class="px-3 py-2"></th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100">
            <tr v-for="row in queue" :key="row.id" class="hover:bg-gray-50">
              <td class="whitespace-nowrap px-3 py-2 tabular-nums text-gray-600">{{ dateLabel(row) }}</td>
              <td class="max-w-[180px] truncate px-3 py-2 font-medium" :title="row.customer_name">
                {{ row.customer_name }}
                <span v-if="aliasForRow(row)" class="ml-1 inline-flex items-center gap-0.5 rounded bg-green-100 px-1 py-0.5 text-xs text-green-700">
                  alias
                </span>
              </td>
              <td class="max-w-[200px] truncate px-3 py-2 text-gray-500" :title="row.message">{{ row.message }}</td>
              <td class="whitespace-nowrap px-3 py-2 text-right tabular-nums font-medium">{{ nok(row.amount) }}</td>
              <td class="px-3 py-2">
                <span class="inline-flex rounded bg-yellow-100 px-2 py-0.5 text-xs font-medium text-yellow-800">Uklassifisert</span>
              </td>
              <td class="whitespace-nowrap px-3 py-2">
                <div v-if="canEdit" class="flex gap-1">
                  <button
                    class="inline-flex items-center gap-1 rounded border border-gray-200 px-2 py-1 text-xs text-gray-600 hover:bg-gray-100"
                    title="Knytt betalar til medlem"
                    @click="openAliasModal(row)"
                  >
                    <UserPlus class="h-3.5 w-3.5" />
                    Alias
                  </button>
                  <button
                    v-if="row.message"
                    class="inline-flex items-center gap-1 rounded border border-gray-200 px-2 py-1 text-xs text-gray-600 hover:bg-gray-100"
                    title="Lag meldingsregel"
                    @click="openRuleModal(row)"
                  >
                    <Tag class="h-3.5 w-3.5" />
                    Regel
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <!-- Learned aliases -->
    <section>
      <h2 class="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500">
        Lærte alias ({{ aliases.length }})
      </h2>
      <div v-if="aliases.length === 0" class="text-sm text-gray-400">Ingen alias ennå.</div>
      <div v-else class="overflow-x-auto rounded-lg border border-gray-200 bg-white">
        <table class="min-w-full divide-y divide-gray-200 text-sm">
          <thead class="bg-gray-50 text-left text-xs font-medium uppercase tracking-wide text-gray-500">
            <tr>
              <th class="px-3 py-2">Vipps-namn</th>
              <th class="px-3 py-2">Medlem-ID</th>
              <th class="px-3 py-2">Oppretta</th>
              <th class="px-3 py-2"></th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100">
            <tr v-for="a in aliases" :key="a.id">
              <td class="px-3 py-2 font-medium">{{ a.customer_name }}</td>
              <td class="px-3 py-2 font-mono text-xs text-gray-500">{{ a.user_id }}</td>
              <td class="whitespace-nowrap px-3 py-2 text-gray-500">{{ a.created_at.slice(0, 10) }}</td>
              <td class="px-3 py-2">
                <button v-if="canEdit" class="text-red-500 hover:text-red-700" @click="deleteAlias(a.id)">
                  <Trash2 class="h-4 w-4" />
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <!-- Message rules -->
    <section>
      <h2 class="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500">
        Meldingsreglar ({{ rules.length }})
      </h2>
      <div v-if="rules.length === 0" class="text-sm text-gray-400">Ingen reglar ennå.</div>
      <div v-else class="overflow-x-auto rounded-lg border border-gray-200 bg-white">
        <table class="min-w-full divide-y divide-gray-200 text-sm">
          <thead class="bg-gray-50 text-left text-xs font-medium uppercase tracking-wide text-gray-500">
            <tr>
              <th class="px-3 py-2">Mønster</th>
              <th class="px-3 py-2">Konto</th>
              <th class="px-3 py-2">Prioritet</th>
              <th class="px-3 py-2"></th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100">
            <tr v-for="r in rules" :key="r.id">
              <td class="px-3 py-2 font-mono text-xs">{{ r.pattern }}</td>
              <td class="px-3 py-2 tabular-nums">{{ r.account_code }}</td>
              <td class="px-3 py-2 tabular-nums text-gray-500">{{ r.priority }}</td>
              <td class="px-3 py-2">
                <button v-if="canEdit" class="text-red-500 hover:text-red-700" @click="deleteRule(r.id)">
                  <Trash2 class="h-4 w-4" />
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <!-- Alias modal -->
    <Teleport to="body">
      <div
        v-if="aliasModal"
        class="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
        @click.self="aliasModal = null"
      >
        <div class="w-full max-w-md rounded-xl bg-white p-6 shadow-xl">
          <h3 class="mb-4 text-base font-semibold text-gray-900">Knytt betalar til medlem</h3>
          <p class="mb-4 text-sm text-gray-600">
            Vipps-namn: <strong>{{ aliasModal.row.customer_name }}</strong>
          </p>
          <label class="mb-1 block text-xs font-medium text-gray-700">Vel medlem</label>
          <select
            v-model="aliasUserID"
            class="w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none"
          >
            <option value="">— vel —</option>
            <option v-for="m in memberOptions" :key="m.value" :value="m.value">{{ m.label }}</option>
          </select>
          <div class="mt-4 flex justify-end gap-2">
            <button
              class="rounded-md border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
              @click="aliasModal = null"
            >Avbryt</button>
            <button
              :disabled="!aliasUserID || saving"
              class="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40"
              @click="saveAlias"
            >{{ saving ? 'Lagrar…' : 'Lagre alias' }}</button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- Rule modal -->
    <Teleport to="body">
      <div
        v-if="ruleModal"
        class="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
        @click.self="ruleModal = null"
      >
        <div class="w-full max-w-md rounded-xl bg-white p-6 shadow-xl">
          <h3 class="mb-4 text-base font-semibold text-gray-900">Lag meldingsregel</h3>
          <p class="mb-4 text-sm text-gray-600">
            Melding: <em>{{ ruleModal.row.message }}</em>
          </p>
          <div class="space-y-3">
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-700">Mønster (del av melding)</label>
              <input
                v-model="rulePattern"
                type="text"
                class="w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none"
              />
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-700">Kontokode</label>
              <input
                v-model="ruleAccountCode"
                type="text"
                placeholder="3200"
                class="w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none"
              />
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-700">Prioritet</label>
              <input
                v-model.number="rulePriority"
                type="number"
                class="w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none"
              />
            </div>
          </div>
          <div class="mt-4 flex justify-end gap-2">
            <button
              class="rounded-md border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
              @click="ruleModal = null"
            >Avbryt</button>
            <button
              :disabled="!rulePattern || !ruleAccountCode || saving"
              class="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40"
              @click="saveRule"
            >{{ saving ? 'Lagrar…' : 'Lagre regel' }}</button>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>
