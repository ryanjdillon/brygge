<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { X } from 'lucide-vue-next'
import { useFreshTotp } from '@/composables/useFreshTotp'

// Role-based price exemptions: which board roles are not charged for which
// price items or whole price categories. Fakturas still show the full price
// with an offsetting "Fritak" line, booked to the chosen expense account.

interface Rule {
  role: string
  category?: string
  price_item_id?: string
}

interface PriceItem {
  id: string
  category: string
  name: string
  is_active: boolean
}

interface Account {
  id: string
  code: string
  name: string
  account_type: string
  is_active: boolean
}

const { t } = useI18n()
const { ensureFreshTotp, totpAwareFetch } = useFreshTotp()

const priceCategories = [
  'membership', 'harbor_membership', 'slip_fee', 'electricity', 'seasonal_rental',
  'guest', 'motorhome', 'room_hire', 'service', 'other',
] as const

function categoryLabel(c: string) {
  return t(`admin.pricing.category${c.split('_').map((p) => p[0].toUpperCase() + p.slice(1)).join('')}`)
}

const loading = ref(true)
const saving = ref(false)
const saved = ref(false)
const error = ref<string | null>(null)

const boardRoles = ref<string[]>([])
const rules = ref<Rule[]>([])
const expenseAccount = ref('')
const items = ref<PriceItem[]>([])
const accounts = ref<Account[]>([])

const expenseAccounts = computed(() =>
  accounts.value.filter((a) => a.account_type === 'expense' && a.is_active),
)

const needsAccount = computed(() => rules.value.length > 0 && !expenseAccount.value)

function rulesFor(role: string) {
  return rules.value.filter((r) => r.role === role)
}

function targetLabel(r: Rule): string {
  if (r.category) return categoryLabel(r.category)
  return items.value.find((i) => i.id === r.price_item_id)?.name ?? '—'
}

// Targets not yet exempted for the role, as "c:<category>" / "i:<id>" keys.
function availableTargets(role: string) {
  const taken = new Set(rulesFor(role).map((r) => (r.category ? `c:${r.category}` : `i:${r.price_item_id}`)))
  return {
    categories: priceCategories.filter((c) => !taken.has(`c:${c}`)),
    items: items.value.filter((i) => i.is_active && !taken.has(`i:${i.id}`)),
  }
}

function addRule(role: string, key: string) {
  if (!key) return
  const [kind, value] = [key.slice(0, 1), key.slice(2)]
  rules.value.push(kind === 'c' ? { role, category: value } : { role, price_item_id: value })
  saved.value = false
}

function removeRule(rule: Rule) {
  rules.value = rules.value.filter((r) => r !== rule)
  saved.value = false
}

async function load() {
  loading.value = true
  error.value = null
  try {
    const [exRes, itemsRes, accRes] = await Promise.all([
      fetch('/api/v1/admin/settings/economy/exemptions', { credentials: 'include' }),
      fetch('/api/v1/admin/pricing', { credentials: 'include' }),
      fetch('/api/v1/admin/accounting/accounts', { credentials: 'include' }),
    ])
    if (!exRes.ok) throw new Error(`${exRes.status} ${exRes.statusText}`)
    const ex = await exRes.json()
    boardRoles.value = ex.board_roles ?? []
    rules.value = ex.rules ?? []
    expenseAccount.value = ex.expense_account ?? ''
    if (itemsRes.ok) {
      const body = await itemsRes.json()
      items.value = (body.items ?? body ?? []) as PriceItem[]
    }
    if (accRes.ok) accounts.value = ((await accRes.json()) ?? []) as Account[]
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    loading.value = false
  }
}

async function save() {
  if (needsAccount.value) {
    error.value = t('admin.economySettings.exemptions.accountRequired')
    return
  }
  if (!(await ensureFreshTotp())) return
  saving.value = true
  saved.value = false
  error.value = null
  try {
    const res = await totpAwareFetch('/api/v1/admin/settings/economy/exemptions', {
      method: 'PUT',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ expense_account: expenseAccount.value, rules: rules.value }),
    })
    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      throw new Error(`${res.status} ${txt}`)
    }
    const body = await res.json()
    rules.value = body.rules ?? []
    expenseAccount.value = body.expense_account ?? ''
    saved.value = true
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

<template>
  <fieldset
    class="rounded-md border border-slate-200 bg-slate-50 p-3 space-y-3"
    data-testid="exemptions-settings"
  >
    <legend class="px-1 text-xs font-semibold text-slate-700">
      {{ t('admin.economySettings.exemptions.label') }}
    </legend>
    <p class="text-xs text-slate-600">{{ t('admin.economySettings.exemptions.hint') }}</p>

    <p v-if="loading" class="text-xs text-slate-500">{{ t('common.loading') }}…</p>
    <template v-else>
      <table class="w-full text-sm">
        <tbody>
          <tr
            v-for="role in boardRoles"
            :key="role"
            class="border-t border-slate-200 align-top"
            :data-role="role"
          >
            <td class="w-40 py-2 pr-3 text-slate-700">{{ t(`admin.users.role.${role}`) }}</td>
            <td class="py-2">
              <div class="flex flex-wrap items-center gap-1.5">
                <span
                  v-for="(r, i) in rulesFor(role)"
                  :key="i"
                  class="inline-flex items-center gap-1 rounded-full bg-brand-50 px-2 py-0.5 text-xs text-brand-800"
                  data-testid="exemption-chip"
                >
                  {{ targetLabel(r) }}
                  <button
                    type="button"
                    class="text-brand-600 hover:text-brand-900"
                    :aria-label="t('common.delete')"
                    @click="removeRule(r)"
                  >
                    <X class="h-3 w-3" />
                  </button>
                </span>
                <select
                  class="rounded-md border border-slate-300 bg-white px-2 py-0.5 text-xs text-gray-700"
                  :value="''"
                  data-testid="exemption-add"
                  @change="(e) => { addRule(role, (e.target as HTMLSelectElement).value); (e.target as HTMLSelectElement).value = '' }"
                >
                  <option value="">{{ t('admin.economySettings.exemptions.add') }}</option>
                  <optgroup :label="t('admin.economySettings.exemptions.categories')">
                    <option v-for="c in availableTargets(role).categories" :key="c" :value="`c:${c}`">
                      {{ categoryLabel(c) }}
                    </option>
                  </optgroup>
                  <optgroup :label="t('admin.economySettings.exemptions.items')">
                    <option v-for="i in availableTargets(role).items" :key="i.id" :value="`i:${i.id}`">
                      {{ i.name }}
                    </option>
                  </optgroup>
                </select>
              </div>
            </td>
          </tr>
        </tbody>
      </table>

      <div class="flex flex-wrap items-end gap-3">
        <label class="flex flex-col gap-1 text-xs text-slate-700">
          {{ t('admin.economySettings.exemptions.expenseAccount') }}
          <select
            v-model="expenseAccount"
            class="rounded-md border border-slate-300 bg-white px-3 py-1.5 text-sm text-gray-900 focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
            data-testid="exemption-account"
            @change="saved = false"
          >
            <option value="">—</option>
            <option v-for="a in expenseAccounts" :key="a.id" :value="a.code">
              {{ a.code }} {{ a.name }}
            </option>
          </select>
        </label>
        <button
          type="button"
          class="rounded-md bg-brand-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-brand-700 disabled:opacity-50"
          :disabled="saving"
          data-testid="exemption-save"
          @click="save"
        >
          {{ t('common.save') }}
        </button>
        <span v-if="saved" class="text-sm font-medium text-green-600">{{ t('common.saved') }}</span>
      </div>
      <p class="text-xs text-slate-500">{{ t('admin.economySettings.exemptions.accountHint') }}</p>
      <p v-if="error" class="rounded-md bg-red-50 px-2 py-1 text-xs text-red-700">{{ error }}</p>
    </template>
  </fieldset>
</template>
