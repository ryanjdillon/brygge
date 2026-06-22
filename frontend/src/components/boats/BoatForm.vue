<script setup lang="ts">
import { ref, watch, reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import { Search } from 'lucide-vue-next'
import { useApiClient, unwrap } from '@/lib/apiClient'
import type { components } from '@/types/api'

type BoatModel = components['schemas']['BoatModel']

export interface BoatFormValue {
  name: string
  type: string
  manufacturer: string
  model: string
  length_m?: number | null
  beam_m?: number | null
  draft_m?: number | null
  weight_kg?: number | null
  registration_number: string
  mmsi: string
  call_sign: string
  boat_model_id?: string | null
}

const props = defineProps<{
  /** Initial values when editing; undefined when creating. */
  initial?: Partial<BoatFormValue> | null
  /** Whether this is an edit (changes save-button label + dim-warning). */
  editing?: boolean
  /** Whether the previously-saved boat had measurements_confirmed=true.
   * Used to warn before submitting dimension changes (member-side only).
   */
  confirmed?: boolean
  /** Show the admin-only "Approve measurements" toggle. */
  showApprove?: boolean
  /** Saving spinner state. */
  saving?: boolean
  /** Error to surface above the buttons. */
  error?: string | null
  /** Confirmation message used when changing dimensions on a confirmed boat. */
  dimChangeConfirmMessage?: string
}>()

const emit = defineEmits<{
  (e: 'submit', value: BoatFormValue & { approve?: boolean }): void
  (e: 'cancel'): void
}>()

const { t } = useI18n()
const client = useApiClient()

const form = reactive<BoatFormValue>({
  name: '',
  type: '',
  manufacturer: '',
  model: '',
  length_m: undefined,
  beam_m: undefined,
  draft_m: undefined,
  weight_kg: undefined,
  registration_number: '',
  mmsi: '',
  call_sign: '',
  boat_model_id: undefined,
})
const approve = ref(false)

const modelQuery = ref('')
const modelResults = ref<BoatModel[]>([])
const showModelResults = ref(false)
let searchTimeout: ReturnType<typeof setTimeout> | null = null

function reset() {
  form.name = props.initial?.name ?? ''
  form.type = props.initial?.type ?? ''
  form.manufacturer = props.initial?.manufacturer ?? ''
  form.model = props.initial?.model ?? ''
  form.length_m = props.initial?.length_m ?? undefined
  form.beam_m = props.initial?.beam_m ?? undefined
  form.draft_m = props.initial?.draft_m ?? undefined
  form.weight_kg = props.initial?.weight_kg ?? undefined
  form.registration_number = props.initial?.registration_number ?? ''
  form.mmsi = props.initial?.mmsi ?? ''
  form.call_sign = props.initial?.call_sign ?? ''
  form.boat_model_id = props.initial?.boat_model_id ?? undefined
  approve.value = false
  modelQuery.value =
    form.manufacturer && form.model ? `${form.manufacturer} ${form.model}` : ''
}
reset()
watch(() => props.initial, reset, { deep: true })

watch(modelQuery, (q) => {
  if (searchTimeout) clearTimeout(searchTimeout)
  if (q.length < 2) {
    modelResults.value = []
    showModelResults.value = false
    return
  }
  searchTimeout = setTimeout(async () => {
    try {
      const results = unwrap(await client.GET('/api/v1/boat-models', { params: { query: { q } } }))
      modelResults.value = results ?? []
      showModelResults.value = (results ?? []).length > 0
    } catch {
      modelResults.value = []
    }
  }, 300)
})

function delayHideResults() {
  setTimeout(() => (showModelResults.value = false), 200)
}

function selectModel(m: BoatModel) {
  form.manufacturer = m.manufacturer
  form.model = m.model
  form.type = m.boat_type
  form.length_m = m.length_m
  form.beam_m = m.beam_m
  form.draft_m = m.draft_m
  form.weight_kg = m.weight_kg
  form.boat_model_id = m.id
  modelQuery.value = `${m.manufacturer} ${m.model}`
  showModelResults.value = false
}

function submit() {
  // Dimension-change warning when editing a member-confirmed boat.
  if (props.editing && props.confirmed && props.initial) {
    const dimsChanged =
      form.length_m !== (props.initial.length_m ?? undefined) ||
      form.beam_m !== (props.initial.beam_m ?? undefined) ||
      form.draft_m !== (props.initial.draft_m ?? undefined)
    if (dimsChanged) {
      const msg = props.dimChangeConfirmMessage ??
        'Endring av mål vil kreve ny godkjenning fra styret. Vil du fortsette?'
      if (!confirm(msg)) return
    }
  }
  const out: BoatFormValue & { approve?: boolean } = { ...form }
  if (props.showApprove) out.approve = approve.value
  emit('submit', out)
}
</script>

<template>
  <form class="space-y-4" @submit.prevent="submit">
    <!-- Boat-model search -->
    <div class="relative">
      <label for="bf-search" class="block text-sm font-medium text-gray-700">
        {{ t('portal.boats.searchModel') }}
      </label>
      <div class="relative mt-1">
        <Search class="absolute left-3 top-2.5 h-4 w-4 text-gray-400" />
        <input
          id="bf-search"
          v-model="modelQuery"
          type="text"
          :placeholder="t('portal.boats.searchModelPlaceholder')"
          class="block w-full rounded-md border border-gray-300 py-2 pl-9 pr-3 shadow-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
          @focus="showModelResults = modelResults.length > 0"
          @blur="delayHideResults()"
        />
      </div>
      <div
        v-if="showModelResults"
        class="absolute z-10 mt-1 max-h-60 w-full overflow-auto rounded-md border border-gray-200 bg-white shadow-lg"
      >
        <button
          v-for="m in modelResults"
          :key="m.id"
          type="button"
          class="flex w-full items-center justify-between px-4 py-2.5 text-left text-sm hover:bg-brand-50"
          @mousedown.prevent="selectModel(m)"
        >
          <div>
            <span class="font-medium text-gray-900">{{ m.manufacturer }} {{ m.model }}</span>
            <span v-if="m.year_from" class="ml-1 text-gray-500">
              ({{ m.year_from }}{{ m.year_to ? `–${m.year_to}` : '+' }})
            </span>
          </div>
          <span class="text-xs text-gray-400">
            {{ m.length_m }}×{{ m.beam_m }}×{{ m.draft_m }} m
          </span>
        </button>
      </div>
    </div>

    <div class="grid grid-cols-2 gap-3">
      <div>
        <label for="bf-mfg" class="block text-sm font-medium text-gray-700">{{ t('portal.boats.manufacturer') }}</label>
        <input id="bf-mfg" v-model="form.manufacturer" type="text" class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500" />
      </div>
      <div>
        <label for="bf-model" class="block text-sm font-medium text-gray-700">{{ t('portal.boats.model') }}</label>
        <input id="bf-model" v-model="form.model" type="text" class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500" />
      </div>
    </div>

    <div>
      <label for="bf-name" class="block text-sm font-medium text-gray-700">
        {{ t('portal.boats.name') }}
        <span class="text-xs font-normal text-gray-400">{{ t('common.optional') }}</span>
      </label>
      <input id="bf-name" v-model="form.name" type="text" class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500" />
    </div>

    <div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
      <div>
        <label for="bf-length" class="block text-sm font-medium text-gray-700">{{ t('portal.boats.length') }}</label>
        <input id="bf-length" v-model.number="form.length_m" type="number" step="0.01" min="0" class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500" />
      </div>
      <div>
        <label for="bf-beam" class="block text-sm font-medium text-gray-700">{{ t('portal.boats.beam') }}</label>
        <input id="bf-beam" v-model.number="form.beam_m" type="number" step="0.01" min="0" class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500" />
      </div>
      <div>
        <label for="bf-draft" class="block text-sm font-medium text-gray-700">{{ t('portal.boats.draft') }}</label>
        <input id="bf-draft" v-model.number="form.draft_m" type="number" step="0.01" min="0" class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500" />
      </div>
      <div>
        <label for="bf-weight" class="block text-sm font-medium text-gray-700">{{ t('portal.boats.weight') }}</label>
        <input id="bf-weight" v-model.number="form.weight_kg" type="number" step="1" min="0" class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500" />
      </div>
    </div>

    <fieldset class="rounded-md border border-gray-200 bg-gray-50 p-3">
      <legend class="px-1 text-sm font-semibold text-gray-700">{{ t('portal.boats.safety.title') }}</legend>
      <p class="mb-2 text-xs text-gray-500">{{ t('portal.boats.safety.hint') }}</p>
      <div class="grid gap-3 sm:grid-cols-3">
        <div>
          <label for="bf-reg" class="block text-sm font-medium text-gray-700">{{ t('portal.boats.registrationNumber') }}</label>
          <input id="bf-reg" v-model="form.registration_number" type="text" class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500" />
        </div>
        <div>
          <label for="bf-mmsi" class="block text-sm font-medium text-gray-700">{{ t('portal.boats.mmsi') }}</label>
          <input id="bf-mmsi" v-model="form.mmsi" type="text" inputmode="numeric" pattern="[0-9]*" maxlength="9" class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500" />
        </div>
        <div>
          <label for="bf-callsign" class="block text-sm font-medium text-gray-700">{{ t('portal.boats.callSign') }}</label>
          <input id="bf-callsign" v-model="form.call_sign" type="text" class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500" />
        </div>
      </div>
    </fieldset>

    <label v-if="showApprove" class="flex items-center gap-2 rounded-md bg-emerald-50 px-3 py-2 text-sm text-emerald-900">
      <input v-model="approve" type="checkbox" class="rounded border-emerald-300" />
      <span>{{ t('admin.users.boatApprove') }}</span>
      <span class="ml-1 text-xs text-emerald-700/70">{{ t('admin.users.boatApproveHint') }}</span>
    </label>

    <p v-if="error" class="rounded-md bg-red-50 px-2 py-1 text-xs text-red-700">{{ error }}</p>

    <div class="flex gap-3">
      <button
        type="submit"
        :disabled="saving"
        class="rounded-md bg-brand-600 px-4 py-2 text-sm font-semibold text-white shadow-sm hover:bg-brand-700 disabled:opacity-50"
      >
        {{ saving ? t('common.loading') : t('common.save') }}
      </button>
      <button
        type="button"
        class="rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-semibold text-gray-700 shadow-sm hover:bg-gray-50"
        @click="emit('cancel')"
      >
        {{ t('common.cancel') }}
      </button>
    </div>
  </form>
</template>
