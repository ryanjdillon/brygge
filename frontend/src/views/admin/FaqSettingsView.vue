<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'
import { Plus, Pencil, Trash2, X, ChevronUp, ChevronDown } from 'lucide-vue-next'
import { FAQ_GROUP_ORDER, faqGroupLabelKey, type FaqItem } from '@/lib/faqData'
import Input from '@/components/ui/form/Input.vue'
import Textarea from '@/components/ui/form/Textarea.vue'
import Select from '@/components/ui/form/Select.vue'
import Checkbox from '@/components/ui/form/Checkbox.vue'

const { t } = useI18n()
const client = useApiClient()
const queryClient = useQueryClient()

const { data: response, isLoading } = useQuery({
  queryKey: ['admin', 'faqs'],
  queryFn: async () => unwrap(await client.GET('/api/v1/admin/faqs')),
})

const items = computed<FaqItem[]>(() => (response.value?.items ?? []) as FaqItem[])

const groups = computed(() =>
  FAQ_GROUP_ORDER.map((key) => ({
    key,
    items: items.value.filter((it) => it.group_key === key),
  })),
)

const groupOptions = computed(() =>
  FAQ_GROUP_ORDER.map((key) => ({ value: key, label: t(faqGroupLabelKey(key)) })),
)

interface FormData {
  id?: string
  group_key: string
  question: string
  answer: string
  link_url: string
  link_label: string
  published: boolean
}

const emptyForm: FormData = {
  group_key: 'newcomers',
  question: '',
  answer: '',
  link_url: '',
  link_label: '',
  published: true,
}

const showForm = ref(false)
const form = ref<FormData>({ ...emptyForm })
const toast = ref<{ type: 'success' | 'error'; message: string } | null>(null)

function showToast(type: 'success' | 'error', message: string) {
  toast.value = { type, message }
  setTimeout(() => (toast.value = null), 3000)
}

function openCreate() {
  form.value = { ...emptyForm }
  showForm.value = true
}

function openEdit(item: FaqItem) {
  form.value = {
    id: item.id,
    group_key: item.group_key,
    question: item.question,
    answer: item.answer,
    link_url: item.link_url ?? '',
    link_label: item.link_label ?? '',
    published: item.published,
  }
  showForm.value = true
}

function buildPayload() {
  return {
    group_key: form.value.group_key,
    question: form.value.question,
    answer: form.value.answer,
    link_url: form.value.link_url.trim() || null,
    link_label: form.value.link_label.trim() || null,
    published: form.value.published,
  }
}

const { mutate: saveItem, isPending: isSaving } = useMutation({
  mutationFn: async () => {
    const payload = buildPayload()
    if (form.value.id) {
      return unwrap(
        await client.PUT('/api/v1/admin/faqs/{faqID}', {
          params: { path: { faqID: form.value.id } },
          body: payload as never,
        }),
      )
    }
    return unwrap(await client.POST('/api/v1/admin/faqs', { body: payload as never }))
  },
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['admin', 'faqs'] })
    showForm.value = false
    showToast('success', form.value.id ? t('admin.faq.updated') : t('admin.faq.created'))
  },
  onError: () => showToast('error', t('admin.faq.saveError')),
})

const { mutate: deleteItem } = useMutation({
  mutationFn: async (id: string) =>
    unwrap(await client.DELETE('/api/v1/admin/faqs/{faqID}', { params: { path: { faqID: id } } })),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['admin', 'faqs'] })
    showToast('success', t('admin.faq.deleted'))
  },
  onError: () => showToast('error', t('admin.faq.deleteError')),
})

function confirmDelete(id: string) {
  if (confirm(t('admin.faq.deleteConfirm'))) deleteItem(id)
}

const { mutate: reorder } = useMutation({
  mutationFn: async (payload: { group_key: string; ids: string[] }) =>
    unwrap(await client.PUT('/api/v1/admin/faqs/reorder', { body: payload as never })),
  onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admin', 'faqs'] }),
  onError: () => showToast('error', t('admin.faq.saveError')),
})

// Move an item up/down within its group by swapping and resending the order.
function move(groupKey: string, index: number, delta: number) {
  const groupItems = groups.value.find((g) => g.key === groupKey)?.items ?? []
  const target = index + delta
  if (target < 0 || target >= groupItems.length) return
  const ids = groupItems.map((it) => it.id)
  ;[ids[index], ids[target]] = [ids[target], ids[index]]
  reorder({ group_key: groupKey, ids })
}
</script>

<template>
  <div>
    <div class="flex items-start justify-between">
      <div>
        <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.faq.title') }}</h1>
        <p class="mt-1 text-sm text-gray-600">{{ t('admin.faq.subtitle') }}</p>
      </div>
      <button
        v-if="!showForm"
        class="flex flex-none items-center gap-1.5 rounded-md bg-brand-600 px-3 py-2 text-sm font-semibold text-white shadow-sm hover:bg-brand-700"
        @click="openCreate"
      >
        <Plus class="h-4 w-4" />
        {{ t('admin.faq.newItem') }}
      </button>
    </div>

    <div
      v-if="toast"
      :class="['mt-4 rounded-md p-3 text-sm', toast.type === 'success' ? 'bg-green-50 text-green-800' : 'bg-red-50 text-red-800']"
    >
      {{ toast.message }}
    </div>

    <!-- Form -->
    <form
      v-if="showForm"
      class="mt-6 max-w-2xl space-y-4 rounded-lg border border-gray-200 bg-white p-5"
      @submit.prevent="saveItem()"
    >
      <div class="flex items-center justify-between">
        <h2 class="text-lg font-semibold text-gray-900">
          {{ form.id ? t('admin.faq.editItem') : t('admin.faq.newItem') }}
        </h2>
        <button type="button" class="text-gray-400 hover:text-gray-600" @click="showForm = false">
          <X class="h-5 w-5" />
        </button>
      </div>

      <div>
        <label class="block text-sm font-medium text-gray-700">{{ t('admin.faq.group') }}</label>
        <Select v-model="form.group_key" :options="groupOptions" class="mt-1" />
      </div>

      <div>
        <label class="block text-sm font-medium text-gray-700">{{ t('admin.faq.question') }}</label>
        <Input v-model="form.question" class="mt-1" />
      </div>

      <div>
        <label class="block text-sm font-medium text-gray-700">{{ t('admin.faq.answer') }}</label>
        <Textarea v-model="form.answer" :rows="4" class="mt-1" />
      </div>

      <div class="rounded-md border border-gray-100 bg-gray-50 p-4">
        <p class="text-sm font-medium text-gray-700">{{ t('admin.faq.link') }}</p>
        <p class="mb-3 text-xs text-gray-500">{{ t('admin.faq.linkHint') }}</p>
        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="block text-xs text-gray-500">{{ t('admin.faq.linkUrl') }}</label>
            <Input v-model="form.link_url" placeholder="/pricing" class="mt-1" />
          </div>
          <div>
            <label class="block text-xs text-gray-500">{{ t('admin.faq.linkLabel') }}</label>
            <Input v-model="form.link_label" class="mt-1" />
          </div>
        </div>
      </div>

      <Checkbox v-model="form.published">{{ t('admin.faq.published') }}</Checkbox>

      <div class="flex gap-3 pt-2">
        <button
          type="submit"
          :disabled="isSaving"
          class="rounded-md bg-brand-600 px-4 py-2 text-sm font-semibold text-white shadow-sm hover:bg-brand-700 disabled:opacity-50"
        >
          {{ t('common.save') }}
        </button>
        <button
          type="button"
          class="rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-semibold text-gray-700 shadow-sm hover:bg-gray-50"
          @click="showForm = false"
        >
          {{ t('common.cancel') }}
        </button>
      </div>
    </form>

    <div v-if="isLoading" class="mt-6 text-gray-500">{{ t('common.loading') }}...</div>

    <p v-else-if="!items.length" class="mt-6 rounded-lg border border-dashed border-gray-300 p-8 text-center text-gray-500">
      {{ t('admin.faq.noItems') }}
    </p>

    <!-- Grouped list -->
    <div v-else class="mt-6 space-y-8">
      <section v-for="group in groups" v-show="group.items.length" :key="group.key">
        <h2 class="mb-2 text-xs font-semibold uppercase tracking-wider text-gray-500">
          {{ t(faqGroupLabelKey(group.key)) }}
        </h2>
        <ul class="divide-y divide-gray-200 rounded-lg border border-gray-200 bg-white">
          <li v-for="(item, idx) in group.items" :key="item.id" class="flex items-start gap-3 p-4">
            <div class="flex flex-col pt-0.5">
              <button
                class="text-gray-400 hover:text-brand-600 disabled:opacity-30"
                :disabled="idx === 0"
                :aria-label="t('admin.faq.moveUp')"
                @click="move(group.key, idx, -1)"
              >
                <ChevronUp class="h-4 w-4" />
              </button>
              <button
                class="text-gray-400 hover:text-brand-600 disabled:opacity-30"
                :disabled="idx === group.items.length - 1"
                :aria-label="t('admin.faq.moveDown')"
                @click="move(group.key, idx, 1)"
              >
                <ChevronDown class="h-4 w-4" />
              </button>
            </div>
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2">
                <p class="font-medium text-gray-900">{{ item.question }}</p>
                <span
                  v-if="!item.published"
                  class="rounded-full bg-gray-100 px-2 py-0.5 text-[10px] font-medium text-gray-500"
                >
                  {{ t('admin.faq.draft') }}
                </span>
              </div>
              <p class="mt-1 line-clamp-2 text-sm text-gray-500">{{ item.answer }}</p>
              <p v-if="item.link_url" class="mt-1 text-xs text-brand-600">
                {{ item.link_label || item.link_url }} → {{ item.link_url }}
              </p>
            </div>
            <div class="flex flex-none items-center gap-2">
              <button class="text-gray-500 hover:text-brand-600" :aria-label="t('common.edit')" @click="openEdit(item)">
                <Pencil class="h-4 w-4" />
              </button>
              <button class="text-gray-500 hover:text-red-600" :aria-label="t('common.delete')" @click="confirmDelete(item.id)">
                <Trash2 class="h-4 w-4" />
              </button>
            </div>
          </li>
        </ul>
      </section>
    </div>
  </div>
</template>
