<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useFreshTotp } from '@/composables/useFreshTotp'

// One on/off club setting in its own box with its own Save (BRY-568): the
// bank auto-assign rule and the automatic booking master switch. Save asks
// for a fresh TOTP at click time, then PATCHes only this field of
// /admin/settings/site. A refused step-up or a failed save puts the box back
// to the last stored value (as the rejoin_blocked box does), so it never
// shows a state the server does not hold; a failure is shown in the box and
// also emitted for the page's banner (null clears it when a save starts).
// Read-only users get it disabled by the page's surrounding fieldset; `ready`
// keeps Save off until the stored value has loaded, so a failed load can
// never save `false` over it. Attributes such as data-testid fall through to
// the fieldset.
const props = defineProps<{
  field: string
  legend: string
  label: string
  hint: string
  ready: boolean
}>()
const value = defineModel<boolean>({ required: true })
const emit = defineEmits<{ error: [message: string | null] }>()

const { t } = useI18n()
const { ensureFreshTotp, totpAwareFetch } = useFreshTotp()

const saving = ref(false)
const saved = ref(false)
const saveError = ref<string | null>(null)
const hintId = `setting-switch-${props.field}-hint`
const errorId = `setting-switch-${props.field}-error`

// The value the server holds: taken when the settings have loaded and after
// each successful save.
const stored = ref(value.value)
watch(() => props.ready, (ready) => {
  if (ready) stored.value = value.value
}, { immediate: true })

function edited() {
  saved.value = false
  saveError.value = null
}

async function save() {
  if (saving.value || !props.ready) return
  // Busy before the step-up, so a second click cannot start a second save.
  saving.value = true
  saved.value = false
  saveError.value = null
  try {
    if (!(await ensureFreshTotp())) {
      value.value = stored.value
      return
    }
    emit('error', null)
    const res = await totpAwareFetch('/api/v1/admin/settings/site', {
      method: 'PATCH',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ [props.field]: value.value }),
    })
    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      throw new Error(`${res.status} ${txt}`)
    }
    stored.value = value.value
    saved.value = true
  } catch (e) {
    value.value = stored.value
    saveError.value = (e as Error).message
    emit('error', saveError.value)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <fieldset class="rounded-md border border-slate-200 bg-slate-50 p-3 space-y-3">
    <legend class="px-1 text-xs font-semibold text-slate-700">{{ legend }}</legend>
    <div class="flex flex-wrap items-center gap-3">
      <label class="flex min-h-[44px] items-center gap-2 text-sm text-slate-700">
        <input
          v-model="value"
          type="checkbox"
          class="h-5 w-5"
          :aria-describedby="saveError ? `${hintId} ${errorId}` : hintId"
          :aria-invalid="saveError ? 'true' : undefined"
          @change="edited"
        />
        {{ label }}
      </label>
      <button
        type="button"
        class="min-h-[44px] rounded-md bg-brand-600 px-4 py-2 text-sm font-medium text-white hover:bg-brand-700 disabled:opacity-50"
        :disabled="saving || !ready"
        @click="save"
      >
        {{ t('common.save') }}
      </button>
      <span v-if="saved" role="status" class="text-sm font-medium text-green-600">{{ t('common.saved') }}</span>
    </div>
    <p :id="hintId" class="text-xs text-slate-500">{{ hint }}</p>
    <p v-if="saveError" :id="errorId" role="alert" class="rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">
      {{ saveError }}
    </p>
  </fieldset>
</template>
