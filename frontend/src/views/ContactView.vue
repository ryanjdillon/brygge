<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { MapPin, Phone, Radio, Mail, MessageCircle } from 'lucide-vue-next'
import { useClubStore } from '@/stores/club'
import { useFeatures } from '@/composables/useFeatures'
import EmailLink from '@/components/EmailLink.vue'

const { t } = useI18n()
const club = useClubStore()
club.ensureLoaded()

const { isEnabled } = useFeatures()
// Matrix room is part of the Communications module — drop the row when
// the module is off so the page doesn't promise a chat that isn't
// running.
const showMatrix = computed(() => isEnabled('communications'))

interface BoardContact {
  roleKey: string
  email: string
}
// Order intentionally: leder → nestleder → havnesjef → sekretær →
// kasserer. The contact page treats this as the canonical board
// hierarchy, with the harbor master ranked above the secretarial /
// financial roles since members reach out to them most often.
const boardContacts = computed<BoardContact[]>(() =>
  [
    { roleKey: 'contact.boardChairman', email: club.chairmanEmail },
    { roleKey: 'contact.boardViceChairman', email: club.viceChairmanEmail },
    { roleKey: 'contact.boardHarborMaster', email: club.harborMasterEmail },
    { roleKey: 'contact.boardSecretary', email: club.secretaryEmail },
    { roleKey: 'contact.boardTreasurer', email: club.treasurerEmail },
  ].filter((c) => c.email),
)

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
    <h1 class="text-3xl font-bold text-slate-900">{{ t('contact.title') }}</h1>

    <div class="mt-10 grid gap-10 lg:grid-cols-2">
      <div class="space-y-6">
        <div v-if="club.address" class="flex items-start gap-3">
          <MapPin class="mt-1 h-5 w-5 text-blue-600" />
          <div>
            <p class="font-medium text-slate-900">{{ t('contact.address') }}</p>
            <p class="whitespace-pre-line text-slate-600">{{ club.address }}</p>
          </div>
        </div>

        <div v-if="club.phone" class="flex items-start gap-3">
          <Phone class="mt-1 h-5 w-5 text-blue-600" />
          <div>
            <p class="font-medium text-slate-900">{{ t('contact.phone') }}</p>
            <p class="text-slate-600">{{ club.phone }}</p>
          </div>
        </div>

        <div v-if="club.vhfChannel" class="flex items-start gap-3">
          <Radio class="mt-1 h-5 w-5 text-blue-600" />
          <div>
            <p class="font-medium text-slate-900">{{ t('contact.vhf') }}</p>
            <p class="text-slate-600">{{ club.vhfChannel }}</p>
          </div>
        </div>

        <div v-if="boardContacts.length" class="flex items-start gap-3">
          <Mail class="mt-1 h-5 w-5 text-blue-600" />
          <div class="min-w-0 flex-1">
            <p class="font-medium text-slate-900">{{ t('contact.email') }}</p>
            <!-- Two-column grid: role on the left, email on the right.
                 Both columns are individually left-aligned so the emails
                 line up regardless of role label width. Font size matches
                 the address row (text-base via the parent text-slate-600). -->
            <dl class="mt-2 grid grid-cols-[max-content_1fr] gap-x-6 gap-y-1.5">
              <template v-for="c in boardContacts" :key="c.email">
                <dt class="text-slate-700">{{ t(c.roleKey) }}</dt>
                <dd>
                  <EmailLink :address="c.email" class-name="text-blue-600 hover:underline" />
                </dd>
              </template>
            </dl>
          </div>
        </div>

        <div v-if="showMatrix" class="flex items-start gap-3">
          <MessageCircle class="mt-1 h-5 w-5 text-blue-600" />
          <div>
            <p class="font-medium text-slate-900">{{ t('contact.matrixRoom') }}</p>
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
          <label for="contact-name" class="block text-sm font-medium text-slate-700">
            {{ t('contact.formName') }}
          </label>
          <input
            id="contact-name"
            v-model="form.name"
            type="text"
            required
            class="mt-1 block w-full rounded-md border border-slate-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>

        <div>
          <label for="contact-email" class="block text-sm font-medium text-slate-700">
            {{ t('contact.formEmail') }}
          </label>
          <input
            id="contact-email"
            v-model="form.email"
            type="email"
            required
            class="mt-1 block w-full rounded-md border border-slate-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>

        <div>
          <label for="contact-subject" class="block text-sm font-medium text-slate-700">
            {{ t('contact.formSubject') }}
          </label>
          <input
            id="contact-subject"
            v-model="form.subject"
            type="text"
            required
            class="mt-1 block w-full rounded-md border border-slate-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>

        <div>
          <label for="contact-message" class="block text-sm font-medium text-slate-700">
            {{ t('contact.formMessage') }}
          </label>
          <textarea
            id="contact-message"
            v-model="form.message"
            rows="5"
            required
            class="mt-1 block w-full rounded-md border border-slate-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
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
