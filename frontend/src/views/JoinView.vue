<script setup lang="ts">
import { ref, reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import Input from '@/components/ui/form/Input.vue'
import NumberInput from '@/components/ui/form/NumberInput.vue'
import FormField from '@/components/ui/form/FormField.vue'

const { t } = useI18n()

const form = reactive<{
  fullName: string
  email: string
  phone: string
  boatName: string
  boatType: string
  boatLength: number | null
  boatBeam: number | null
  boatDraft: number | null
}>({
  fullName: '',
  email: '',
  phone: '',
  boatName: '',
  boatType: '',
  boatLength: null,
  boatBeam: null,
  boatDraft: null,
})

const submitting = ref(false)
const submitted = ref(false)
const queuePosition = ref<number | null>(null)
const error = ref(false)

async function handleSubmit() {
  submitting.value = true
  error.value = false

  try {
    const response = await fetch('/api/v1/join', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(form),
    })
    if (!response.ok) throw new Error('Submit failed')
    const data = await response.json()
    queuePosition.value = data.queuePosition
    submitted.value = true
  } catch {
    error.value = true
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-2xl px-4 py-12 sm:px-6">
    <h1 class="text-3xl font-bold text-slate-900">{{ t('join.title') }}</h1>
    <p class="mt-2 text-slate-600">{{ t('join.subtitle') }}</p>

    <div v-if="submitted && queuePosition" class="mt-8 rounded-lg border border-green-200 bg-green-50 p-6 text-center">
      <p class="text-lg font-semibold text-green-800">{{ t('join.submitted') }}</p>
      <p class="mt-2 text-green-700">{{ t('join.queueMessage', { position: queuePosition }) }}</p>
      <button
        type="button"
        disabled
        class="mt-4 inline-block cursor-not-allowed rounded-md bg-slate-300 px-6 py-3 text-sm font-semibold text-slate-600"
        :aria-label="t('join.payComingSoon')"
      >
        {{ t('join.payComingSoon') }}
      </button>
    </div>

    <form v-else class="mt-8 space-y-6" @submit.prevent="handleSubmit">
      <div class="space-y-4">
        <FormField :label="t('join.fullName')" for="join-name" required>
          <Input id="join-name" v-model="form.fullName" type="text" required />
        </FormField>

        <FormField :label="t('join.email')" for="join-email" required>
          <Input id="join-email" v-model="form.email" type="email" required />
        </FormField>

        <FormField :label="t('join.phone')" for="join-phone" required>
          <Input id="join-phone" v-model="form.phone" type="tel" required />
        </FormField>
      </div>

      <fieldset class="space-y-4 rounded-lg border border-slate-200 p-4">
        <legend class="px-2 text-sm font-semibold text-slate-900">
          {{ t('join.boatDetails') }}
        </legend>

        <div class="grid gap-4 sm:grid-cols-2">
          <FormField :label="t('join.boatName')" for="join-boat-name">
            <Input id="join-boat-name" v-model="form.boatName" type="text" />
          </FormField>

          <FormField :label="t('join.boatType')" for="join-boat-type">
            <Input id="join-boat-type" v-model="form.boatType" type="text" />
          </FormField>

          <FormField :label="t('join.boatLength')" for="join-boat-length">
            <NumberInput id="join-boat-length" v-model="form.boatLength" :step="0.1" :min="0" />
          </FormField>

          <FormField :label="t('join.boatBeam')" for="join-boat-beam">
            <NumberInput id="join-boat-beam" v-model="form.boatBeam" :step="0.1" :min="0" />
          </FormField>

          <FormField :label="t('join.boatDraft')" for="join-boat-draft">
            <NumberInput id="join-boat-draft" v-model="form.boatDraft" :step="0.1" :min="0" />
          </FormField>
        </div>
      </fieldset>

      <div v-if="error" class="rounded-md bg-red-50 p-3 text-sm text-red-700">
        {{ t('common.error') }}
      </div>

      <button
        type="submit"
        :disabled="submitting"
        class="w-full rounded-md bg-blue-600 px-4 py-3 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
      >
        {{ submitting ? t('join.submitting') : t('join.submit') }}
      </button>
    </form>
  </div>
</template>
