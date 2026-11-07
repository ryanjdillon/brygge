<script setup lang="ts">
// Address search against Kartverket's register, through the API's proxy
// (GET /join/address-search). An ARIA 1.2 combobox: the input owns a listbox
// of matches, arrow keys move through them, Enter picks and Escape closes.
// It emits `select` with the picked address, and `unavailable` when the
// register cannot be searched (an error, a timeout, or the API's fallback
// flag), so the owner can switch to typing the address by hand. Too many
// searches (429) is transient: the search stays, with a hint to wait.
import { ref, computed, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { Loader2, Search } from 'lucide-vue-next'
import { useApiClient, unwrap, ApiError } from '@/lib/apiClient'
import type { components } from '@/types/api'

export type RegisterAddress = components['schemas']['JoinAddress']

const props = withDefaults(
  defineProps<{
    id: string
    error?: string
    // Extra ids the input is described by (a helper text, say).
    describedby?: string
    debounceMs?: number
  }>(),
  { error: undefined, describedby: undefined, debounceMs: 250 },
)

const emit = defineEmits<{
  (e: 'select', address: RegisterAddress): void
  (e: 'unavailable'): void
}>()

const MIN_CHARS = 3

const { t } = useI18n()
const client = useApiClient()

const query = ref('')
const results = ref<RegisterAddress[]>([])
const open = ref(false)
const active = ref(-1)
const loading = ref(false)
// searched is set once a search for the current text has answered, so "no
// matches" is not shown while the visitor is still typing.
const searched = ref(false)
const rateLimited = ref(false)

let timer: ReturnType<typeof setTimeout> | undefined
// Answers can arrive out of order; only the newest request's counts.
let seq = 0

const listboxId = computed(() => `${props.id}-listbox`)
const statusId = computed(() => `${props.id}-status`)
const errorId = computed(() => `${props.id}-error`)
const optionId = (i: number) => `${props.id}-option-${i}`
const activeId = computed(() => (open.value && active.value >= 0 ? optionId(active.value) : undefined))
const describedBy = computed(
  () => [props.error ? errorId.value : '', props.describedby ?? ''].filter(Boolean).join(' ') || undefined,
)

const statusText = computed(() => {
  if (loading.value) return t('join.form.address.searching')
  if (!searched.value) return ''
  return results.value.length ? t('join.form.address.results', { count: results.value.length }) : t('join.form.address.noResults')
})

async function search(q: string) {
  const mine = ++seq
  loading.value = true
  try {
    const res = unwrap(await client.GET('/api/v1/join/address-search', { params: { query: { q } } }))
    if (mine !== seq) return
    if (res.fallback) {
      emit('unavailable')
      return
    }
    rateLimited.value = false
    results.value = res.addresses ?? []
    active.value = -1
    searched.value = true
    open.value = true
  } catch (err) {
    if (mine !== seq) return
    if (err instanceof ApiError && err.status === 429) {
      rateLimited.value = true
      open.value = false
      return
    }
    emit('unavailable')
  } finally {
    if (mine === seq) loading.value = false
  }
}

function onInput(e: Event) {
  query.value = (e.target as HTMLInputElement).value
  clearTimeout(timer)
  searched.value = false
  const q = query.value.trim()
  if (q.length < MIN_CHARS) {
    seq++
    loading.value = false
    results.value = []
    open.value = false
    return
  }
  timer = setTimeout(() => search(q), props.debounceMs)
}

function pick(i: number) {
  const a = results.value[i]
  if (!a) return
  // A search still waiting on the debounce, or in flight, must not reopen
  // the list after the pick.
  clearTimeout(timer)
  seq++
  loading.value = false
  open.value = false
  emit('select', a)
}

function move(delta: number) {
  if (!results.value.length) return
  open.value = true
  const n = results.value.length
  active.value = active.value < 0 ? (delta > 0 ? 0 : n - 1) : (active.value + delta + n) % n
  document.getElementById(optionId(active.value))?.scrollIntoView?.({ block: 'nearest' })
}

function onKeydown(e: KeyboardEvent) {
  switch (e.key) {
    case 'ArrowDown':
      e.preventDefault()
      move(1)
      break
    case 'ArrowUp':
      e.preventDefault()
      move(-1)
      break
    case 'Enter':
      // Enter in the search never submits the form: with an option active
      // it picks it, otherwise it does nothing.
      e.preventDefault()
      if (open.value && active.value >= 0) pick(active.value)
      break
    case 'Escape':
      // The first Escape closes the list, a second clears the search.
      e.preventDefault()
      if (open.value) {
        open.value = false
        active.value = -1
      } else {
        clearTimeout(timer)
        seq++
        loading.value = false
        query.value = ''
        results.value = []
        searched.value = false
      }
      break
  }
}

function onBlur() {
  open.value = false
}

const inputEl = ref<HTMLInputElement | null>(null)
defineExpose({ focus: () => inputEl.value?.focus() })

onBeforeUnmount(() => clearTimeout(timer))
</script>

<template>
  <div class="relative">
    <div class="relative flex items-center">
      <span class="pointer-events-none absolute left-3 flex items-center text-gray-400">
        <Loader2 v-if="loading" class="h-4 w-4 animate-spin" aria-hidden="true" />
        <Search v-else class="h-4 w-4" aria-hidden="true" />
      </span>
      <input
        :id="id"
        ref="inputEl"
        type="text"
        role="combobox"
        name="address_search"
        autocomplete="off"
        autocapitalize="words"
        spellcheck="false"
        aria-autocomplete="list"
        :aria-expanded="open"
        :aria-controls="listboxId"
        :aria-activedescendant="activeId"
        :aria-invalid="!!error"
        :aria-describedby="describedBy"
        :value="query"
        :placeholder="t('join.form.address.placeholder')"
        :maxlength="100"
        class="block min-h-11 w-full rounded-md border bg-white py-1.5 pl-9 pr-3 text-base focus:outline-none focus:ring-1 sm:text-sm"
        :class="error ? 'border-red-400 focus:border-red-500 focus:ring-red-500' : 'border-gray-300 hover:border-gray-400 focus:border-brand-500 focus:ring-brand-500'"
        data-testid="address-search-input"
        @input="onInput"
        @keydown="onKeydown"
        @blur="onBlur"
      />
    </div>
    <ul
      v-show="open && results.length > 0"
      :id="listboxId"
      role="listbox"
      :aria-label="t('join.form.address.label')"
      class="absolute z-20 mt-1 max-h-72 w-full overflow-y-auto rounded-md border border-gray-200 bg-white py-1 shadow-lg"
      data-testid="address-search-listbox"
    >
      <li
        v-for="(a, i) in results"
        :id="optionId(i)"
        :key="a.id"
        role="option"
        :aria-selected="i === active"
        class="flex min-h-11 cursor-pointer flex-col justify-center px-3 py-1.5 text-sm"
        :class="i === active ? 'bg-brand-50 text-brand-900' : 'text-gray-800 hover:bg-gray-50'"
        @mousedown.prevent="pick(i)"
        @mousemove="active = i"
      >
        <span class="font-medium">{{ a.street }}</span>
        <span class="text-xs text-gray-600">{{ a.postal_code }} {{ a.city }}</span>
      </li>
    </ul>
    <p v-if="error" :id="errorId" class="mt-1 text-xs text-red-600">{{ error }}</p>
    <p v-if="rateLimited" class="mt-1 text-xs text-amber-800" role="status" data-testid="address-search-rate-limited">
      {{ t('join.form.address.rateLimited') }}
    </p>
    <p v-if="searched && !loading && results.length === 0" class="mt-1 text-xs text-gray-600" data-testid="address-search-empty">
      {{ t('join.form.address.noResults') }}
    </p>
    <p :id="statusId" class="sr-only" role="status" aria-live="polite">{{ statusText }}</p>
    <p class="mt-1 text-xs text-gray-500" data-testid="address-attribution">{{ t('join.form.address.attribution') }}</p>
  </div>
</template>
