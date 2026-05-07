<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter, RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ArrowLeft, FilePlus, Users, Receipt, Send, Ban, Inbox, ArrowLeft as Back } from 'lucide-vue-next'
import FakturaList from '@/components/admin/FakturaList.vue'
import SingleFakturaModal from '@/components/admin/SingleFakturaModal.vue'
import GroupFakturaTab from '@/components/admin/GroupFakturaTab.vue'
import { useTotpGateStore } from '@/stores/totpGate'
import { useAuthStore } from '@/stores/auth'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

type Tab = 'create' | 'drafts' | 'voided' | 'sent'
const validTabs: Tab[] = ['create', 'drafts', 'voided', 'sent']

const activeTab = computed<Tab>(() => {
  const q = route.query.tab
  const t = typeof q === 'string' ? q : ''
  return (validTabs as string[]).includes(t) ? (t as Tab) : 'create'
})

watch(
  () => route.query.tab,
  (q) => {
    if (typeof q !== 'string' || !(validTabs as string[]).includes(q)) {
      router.replace({ query: { ...route.query, tab: 'create' } })
    }
  },
  { immediate: true },
)

function setTab(tab: Tab) {
  if (tab === activeTab.value) return
  router.push({ query: { ...route.query, tab } })
}

type CreateMode = '' | 'single' | 'group'
const createMode = ref<CreateMode>('')
const singleOpen = ref(false)

watch(activeTab, (t) => {
  if (t !== 'create') {
    createMode.value = ''
    singleOpen.value = false
  }
})

const auth = useAuthStore()
const totpGate = useTotpGateStore()

async function ensureFreshTotp(): Promise<boolean> {
  if (auth.hasFreshTotp) return true
  return totpGate.open()
}

// TOTP prompt fires at action-button click time so the user re-verifies
// before the form opens, not when they hit Submit. The backend
// RequireFreshTOTP middleware is the hard gate either way.
async function openSingle() {
  if (!(await ensureFreshTotp())) return
  createMode.value = 'single'
  singleOpen.value = true
}
async function openGroup() {
  if (!(await ensureFreshTotp())) return
  createMode.value = 'group'
}
function backToCreate() {
  createMode.value = ''
  singleOpen.value = false
}
function onCreated() {
  router.push({ query: { ...route.query, tab: 'drafts' } })
}

const tabs: { id: Tab; icon: typeof FilePlus; labelKey: string }[] = [
  { id: 'create', icon: FilePlus, labelKey: 'admin.faktura.tabs.create' },
  { id: 'drafts', icon: Inbox, labelKey: 'admin.faktura.tabs.drafts' },
  { id: 'voided', icon: Ban, labelKey: 'admin.faktura.tabs.voided' },
  { id: 'sent', icon: Send, labelKey: 'admin.faktura.tabs.sent' },
]
</script>

<template>
  <div>
    <div class="mb-3 flex items-center gap-2">
      <RouterLink to="/admin/accounting" class="text-sm text-gray-600 hover:text-gray-900">
        <ArrowLeft class="inline h-4 w-4" /> {{ t('admin.accounting.title') }}
      </RouterLink>
    </div>

    <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.faktura.title') }}</h1>
    <p class="mt-1 text-sm text-gray-600">{{ t('admin.faktura.subtitle') }}</p>

    <div class="mt-4 border-b border-gray-200">
      <nav class="-mb-px flex gap-2 overflow-x-auto" :aria-label="t('admin.faktura.tabsAria')">
        <button
          v-for="tab in tabs"
          :key="tab.id"
          type="button"
          :class="[
            'inline-flex items-center gap-2 whitespace-nowrap border-b-2 px-3 py-2 text-sm font-medium',
            activeTab === tab.id
              ? 'border-blue-600 text-blue-700'
              : 'border-transparent text-gray-500 hover:border-gray-300 hover:text-gray-700',
          ]"
          @click="setTab(tab.id)"
        >
          <component :is="tab.icon" class="h-4 w-4" />
          {{ t(tab.labelKey) }}
        </button>
      </nav>
    </div>

    <div class="mt-5">
      <div v-if="activeTab === 'create'">
        <div v-if="createMode === ''" class="grid gap-4 sm:grid-cols-2">
          <button
            type="button"
            class="flex items-center gap-3 rounded-lg border border-gray-200 bg-white p-5 text-left transition hover:border-blue-300 hover:shadow-sm"
            @click="openSingle"
          >
            <Receipt class="h-8 w-8 text-blue-600" />
            <div>
              <p class="font-semibold text-gray-900">{{ t('admin.faktura.create.singleTitle') }}</p>
              <p class="text-sm text-gray-500">{{ t('admin.faktura.create.singleDesc') }}</p>
            </div>
          </button>
          <button
            type="button"
            class="flex items-center gap-3 rounded-lg border border-gray-200 bg-white p-5 text-left transition hover:border-blue-300 hover:shadow-sm"
            @click="openGroup"
          >
            <Users class="h-8 w-8 text-blue-600" />
            <div>
              <p class="font-semibold text-gray-900">{{ t('admin.faktura.create.groupTitle') }}</p>
              <p class="text-sm text-gray-500">{{ t('admin.faktura.create.groupDesc') }}</p>
            </div>
          </button>
        </div>

        <div v-else-if="createMode === 'group'">
          <button
            type="button"
            class="mb-3 inline-flex items-center gap-1 text-sm text-gray-600 hover:text-gray-900"
            @click="backToCreate"
          >
            <Back class="h-4 w-4" /> {{ t('admin.faktura.create.back') }}
          </button>
          <GroupFakturaTab @completed="onCreated" />
        </div>

        <SingleFakturaModal
          v-if="createMode === 'single'"
          :open="singleOpen"
          @close="backToCreate"
          @created="onCreated"
        />
      </div>

      <FakturaList v-else-if="activeTab === 'drafts'" status="draft" />
      <FakturaList v-else-if="activeTab === 'sent'" status="sent" />
      <FakturaList v-else-if="activeTab === 'voided'" status="voided" />
    </div>
  </div>
</template>
