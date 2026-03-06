<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { MapPin, Phone, Radio, Mail, MessageCircle } from 'lucide-vue-next'

const { t } = useI18n()

const form = ref({
  name: '',
  email: '',
  subject: '',
  message: '',
})

const sending = ref(false)
const sent = ref(false)
const error = ref(false)

async function handleSubmit() {
  sending.value = true
  sent.value = false
  error.value = false

  try {
    const response = await fetch('/api/v1/contact', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(form.value),
    })
    if (!response.ok) throw new Error('Send failed')
    sent.value = true
    form.value = { name: '', email: '', subject: '', message: '' }
  } catch {
    error.value = true
  } finally {
    sending.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-7xl px-4 py-12 sm:px-6 lg:px-8">
    <h1 class="text-3xl font-bold text-gray-900">{{ t('contact.title') }}</h1>

    <div class="mt-10 grid gap-10 lg:grid-cols-2">
      <div class="space-y-6">
        <div class="flex items-start gap-3">
          <MapPin class="mt-1 h-5 w-5 text-blue-600" />
          <div>
            <p class="font-medium text-gray-900">{{ t('contact.address') }}</p>
            <p class="text-gray-600">Havneveien 1, 0001 Oslo</p>
          </div>
        </div>

        <div class="flex items-start gap-3">
          <Phone class="mt-1 h-5 w-5 text-blue-600" />
          <div>
            <p class="font-medium text-gray-900">{{ t('contact.phone') }}</p>
            <p class="text-gray-600">+47 22 00 00 00</p>
          </div>
        </div>

        <div class="flex items-start gap-3">
          <Radio class="mt-1 h-5 w-5 text-blue-600" />
          <div>
            <p class="font-medium text-gray-900">{{ t('contact.vhf') }}</p>
            <p class="text-gray-600">Ch 73</p>
          </div>
        </div>

        <div class="flex items-start gap-3">
          <Mail class="mt-1 h-5 w-5 text-blue-600" />
          <div>
            <p class="font-medium text-gray-900">{{ t('contact.email') }}</p>
            <p class="text-gray-600">post@brygge.no</p>
          </div>
        </div>

        <div class="flex items-start gap-3">
          <MessageCircle class="mt-1 h-5 w-5 text-blue-600" />
          <div>
            <p class="font-medium text-gray-900">{{ t('contact.matrixRoom') }}</p>
            <a
              href="https://matrix.to/#/#brygge:matrix.org"
              target="_blank"
              rel="noopener"
              class="text-blue-600 hover:underline"
            >
              #brygge:matrix.org
            </a>
          </div>
        </div>
      </div>

      <form class="space-y-4" @submit.prevent="handleSubmit">
        <div>
          <label for="contact-name" class="block text-sm font-medium text-gray-700">
            {{ t('contact.formName') }}
          </label>
          <input
            id="contact-name"
            v-model="form.name"
            type="text"
            required
            class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>

        <div>
          <label for="contact-email" class="block text-sm font-medium text-gray-700">
            {{ t('contact.formEmail') }}
          </label>
          <input
            id="contact-email"
            v-model="form.email"
            type="email"
            required
            class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>

        <div>
          <label for="contact-subject" class="block text-sm font-medium text-gray-700">
            {{ t('contact.formSubject') }}
          </label>
          <input
            id="contact-subject"
            v-model="form.subject"
            type="text"
            required
            class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>

        <div>
          <label for="contact-message" class="block text-sm font-medium text-gray-700">
            {{ t('contact.formMessage') }}
          </label>
          <textarea
            id="contact-message"
            v-model="form.message"
            rows="5"
            required
            class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>

        <div v-if="sent" class="rounded-md bg-green-50 p-3 text-sm text-green-700">
          {{ t('contact.sent') }}
        </div>
        <div v-if="error" class="rounded-md bg-red-50 p-3 text-sm text-red-700">
          {{ t('contact.sendError') }}
        </div>

        <button
          type="submit"
          :disabled="sending"
          class="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
        >
          {{ sending ? t('contact.sending') : t('contact.send') }}
        </button>
      </form>
    </div>
  </div>
</template>
