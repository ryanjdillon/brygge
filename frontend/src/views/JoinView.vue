<script setup lang="ts">
import { ref, reactive } from 'vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

const form = reactive({
  fullName: '',
  email: '',
  phone: '',
  boatName: '',
  boatType: '',
  boatLength: '',
  boatBeam: '',
  boatDraft: '',
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
    <h1 class="text-3xl font-bold text-gray-900">{{ t('join.title') }}</h1>
    <p class="mt-2 text-gray-600">{{ t('join.subtitle') }}</p>

    <div v-if="submitted && queuePosition" class="mt-8 rounded-lg border border-green-200 bg-green-50 p-6 text-center">
      <p class="text-lg font-semibold text-green-800">{{ t('join.submitted') }}</p>
      <p class="mt-2 text-green-700">{{ t('join.queueMessage', { position: queuePosition }) }}</p>
      <a
        href="#"
        class="mt-4 inline-block rounded-md bg-orange-500 px-6 py-3 text-sm font-semibold text-white hover:bg-orange-600"
      >
        {{ t('join.payWithVipps') }}
      </a>
    </div>

    <form v-else class="mt-8 space-y-6" @submit.prevent="handleSubmit">
      <div class="space-y-4">
        <div>
          <label for="join-name" class="block text-sm font-medium text-gray-700">
            {{ t('join.fullName') }}
          </label>
          <input
            id="join-name"
            v-model="form.fullName"
            type="text"
            required
            class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>

        <div>
          <label for="join-email" class="block text-sm font-medium text-gray-700">
            {{ t('join.email') }}
          </label>
          <input
            id="join-email"
            v-model="form.email"
            type="email"
            required
            class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>

        <div>
          <label for="join-phone" class="block text-sm font-medium text-gray-700">
            {{ t('join.phone') }}
          </label>
          <input
            id="join-phone"
            v-model="form.phone"
            type="tel"
            required
            class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>
      </div>

      <fieldset class="space-y-4 rounded-lg border border-gray-200 p-4">
        <legend class="px-2 text-sm font-semibold text-gray-900">
          {{ t('join.boatDetails') }}
        </legend>

        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <label for="join-boat-name" class="block text-sm font-medium text-gray-700">
              {{ t('join.boatName') }}
            </label>
            <input
              id="join-boat-name"
              v-model="form.boatName"
              type="text"
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <div>
            <label for="join-boat-type" class="block text-sm font-medium text-gray-700">
              {{ t('join.boatType') }}
            </label>
            <input
              id="join-boat-type"
              v-model="form.boatType"
              type="text"
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <div>
            <label for="join-boat-length" class="block text-sm font-medium text-gray-700">
              {{ t('join.boatLength') }}
            </label>
            <input
              id="join-boat-length"
              v-model="form.boatLength"
              type="number"
              step="0.1"
              min="0"
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <div>
            <label for="join-boat-beam" class="block text-sm font-medium text-gray-700">
              {{ t('join.boatBeam') }}
            </label>
            <input
              id="join-boat-beam"
              v-model="form.boatBeam"
              type="number"
              step="0.1"
              min="0"
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <div>
            <label for="join-boat-draft" class="block text-sm font-medium text-gray-700">
              {{ t('join.boatDraft') }}
            </label>
            <input
              id="join-boat-draft"
              v-model="form.boatDraft"
              type="number"
              step="0.1"
              min="0"
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>
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
        {{ submitting ? t('join.submitting') : t('join.payWithVipps') }}
      </button>
    </form>
  </div>
</template>
