<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { MapPin, Phone, Radio, Mail, MessageCircle, Map } from 'lucide-vue-next'
import { useClubStore } from '@/stores/club'
import heroImg from '@/assets/hero.jpg'
import EmailLink from '@/components/EmailLink.vue'
import Input from '@/components/ui/form/Input.vue'
import Textarea from '@/components/ui/form/Textarea.vue'
import FormField from '@/components/ui/form/FormField.vue'

const { t } = useI18n()
const club = useClubStore()
club.ensureLoaded()

// Matrix/forum chat is momentarily disabled (BRY-191); hide the row so
// the page doesn't promise a chat that isn't running.
const showMatrix = false

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

// `website` is the honeypot: it is hidden from people, so only a script
// that fills every input sets it, and the server then drops the message.
const emptyForm = () => ({ name: '', email: '', subject: '', message: '', website: '' })
const form = ref(emptyForm())
const sending = ref(false)
const sent = ref(false)
const errorKey = ref<string | null>(null)

// The server never claims success for a message that was not delivered, so
// each failure says what the visitor can do about it.
function errorKeyFor(status: number): string {
  if (status === 400 || status === 413) return 'contact.checkInput'
  if (status === 429) return 'contact.rateLimited'
  if (status === 502) return 'contact.deliveryFailed'
  if (status === 503) return 'contact.unavailable'
  return 'contact.sendError'
}

async function handleSubmit() {
  sending.value = true
  sent.value = false
  errorKey.value = null
  try {
    const response = await fetch('/api/v1/contact', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(form.value),
    })
    if (!response.ok) {
      errorKey.value = errorKeyFor(response.status)
      return
    }
    sent.value = true
    form.value = emptyForm()
  } catch {
    errorKey.value = 'contact.sendError'
  } finally {
    sending.value = false
  }
}
</script>

<template>
  <div>
    <!-- Photo header band: documentary harbour photo behind a dark scrim,
         holding the eyebrow, title and intro. -->
    <section class="relative overflow-hidden">
      <img :src="heroImg" alt="" class="absolute inset-0 h-full w-full object-cover" />
      <div
        class="absolute inset-0"
        style="background: linear-gradient(to bottom, rgba(8, 20, 42, 0.78), rgba(8, 20, 42, 0.55))"
      />
      <div class="relative mx-auto max-w-7xl px-4 py-16 sm:px-6 lg:px-8">
        <p class="text-xs font-semibold uppercase tracking-[0.15em] text-white/85">
          {{ t('contact.eyebrow') }}
        </p>
        <h1 class="mt-2 text-4xl font-bold tracking-tight text-white">{{ t('contact.title') }}</h1>
        <p class="mt-3 max-w-2xl text-white/90">{{ t('contact.intro') }}</p>
      </div>
    </section>

    <div class="mx-auto max-w-7xl px-4 py-12 sm:px-6 lg:px-8">
      <div class="grid gap-10 lg:grid-cols-2">
        <div class="space-y-6">
          <div v-if="club.address" class="flex items-start gap-4">
            <span class="flex h-10 w-10 flex-none items-center justify-center rounded-xl bg-brand-100">
              <MapPin class="h-5 w-5 text-brand-600" />
            </span>
            <div>
              <p class="font-medium text-slate-900">{{ t('contact.address') }}</p>
              <p class="whitespace-pre-line text-slate-600">{{ club.address }}</p>
            </div>
          </div>

          <div v-if="club.phone" class="flex items-start gap-4">
            <span class="flex h-10 w-10 flex-none items-center justify-center rounded-xl bg-brand-100">
              <Phone class="h-5 w-5 text-brand-600" />
            </span>
            <div>
              <p class="font-medium text-slate-900">{{ t('contact.phone') }}</p>
              <p class="text-slate-600">{{ club.phone }}</p>
            </div>
          </div>

          <div v-if="club.vhfChannel" class="flex items-start gap-4">
            <span class="flex h-10 w-10 flex-none items-center justify-center rounded-xl bg-brand-100">
              <Radio class="h-5 w-5 text-brand-600" />
            </span>
            <div>
              <p class="font-medium text-slate-900">{{ t('contact.vhf') }}</p>
              <p class="text-slate-600">{{ club.vhfChannel }}</p>
            </div>
          </div>

          <div v-if="boardContacts.length" class="flex items-start gap-4">
            <span class="flex h-10 w-10 flex-none items-center justify-center rounded-xl bg-brand-100">
              <Mail class="h-5 w-5 text-brand-600" />
            </span>
            <div class="min-w-0 flex-1">
              <p class="font-medium text-slate-900">{{ t('contact.email') }}</p>
              <!-- Two-column grid: role on the left, email on the right.
                   Both columns are individually left-aligned so the emails
                   line up regardless of role label width. -->
              <dl class="mt-2 grid grid-cols-[max-content_1fr] gap-x-6 gap-y-1.5">
                <template v-for="c in boardContacts" :key="c.email">
                  <dt class="text-slate-700">{{ t(c.roleKey) }}</dt>
                  <dd>
                    <EmailLink :address="c.email" class-name="text-brand-600 hover:underline" />
                  </dd>
                </template>
              </dl>
            </div>
          </div>

          <div v-if="showMatrix" class="flex items-start gap-4">
            <span class="flex h-10 w-10 flex-none items-center justify-center rounded-xl bg-brand-100">
              <MessageCircle class="h-5 w-5 text-brand-600" />
            </span>
            <div>
              <p class="font-medium text-slate-900">{{ t('contact.matrixRoom') }}</p>
              <a
                href="https://matrix.to/#/#brygge:matrix.org"
                target="_blank"
                rel="noopener"
                class="text-brand-600 hover:underline"
              >
                #brygge:matrix.org
              </a>
            </div>
          </div>
        </div>

        <form
          class="relative space-y-4 rounded-2xl border border-slate-200 bg-white p-7 shadow-md"
          @submit.prevent="handleSubmit"
        >
          <FormField :label="t('contact.formName')" for="contact-name" required>
            <Input id="contact-name" v-model="form.name" type="text" required :maxlength="100" />
          </FormField>

          <FormField :label="t('contact.formEmail')" for="contact-email" required>
            <Input id="contact-email" v-model="form.email" type="email" required :maxlength="254" />
          </FormField>

          <FormField :label="t('contact.formSubject')" for="contact-subject" required>
            <Input id="contact-subject" v-model="form.subject" type="text" required :maxlength="200" />
          </FormField>

          <FormField :label="t('contact.formMessage')" for="contact-message" required>
            <Textarea id="contact-message" v-model="form.message" :rows="5" required :minlength="10" :maxlength="5000" />
          </FormField>

          <div v-if="sent" class="rounded-md bg-green-50 p-3 text-sm text-green-700">
            {{ t('contact.sent') }}
          </div>
          <div v-if="errorKey" role="alert" class="rounded-md bg-red-50 p-3 text-sm text-red-700">
            {{ t(errorKey) }}
          </div>

          <!-- Honeypot: off-screen rather than display:none, which some bots
               skip; out of the tab order and the accessibility tree. -->
          <div aria-hidden="true" class="absolute -left-[9999px] h-0 w-0 overflow-hidden">
            <input
              id="contact-website"
              v-model="form.website"
              type="text"
              name="website"
              tabindex="-1"
              autocomplete="off"
            />
          </div>

          <button
            type="submit"
            :disabled="sending"
            class="min-h-[44px] w-full rounded-md bg-brand-600 px-4 py-2 text-sm font-medium text-white hover:bg-brand-700 disabled:opacity-50"
          >
            {{ sending ? t('contact.sending') : t('contact.send') }}
          </button>
          <p class="text-center text-xs text-slate-500">{{ t('contact.replyTime') }}</p>
        </form>
      </div>

      <!-- Map panel -->
      <section class="mt-12">
        <div class="mb-3 flex items-center gap-2 text-sm font-medium text-slate-700">
          <Map class="h-4 w-4 text-brand-600" />
          {{ t('contact.findMarina') }}
        </div>
        <div class="h-[280px] w-full rounded-2xl bg-slate-100 ring-1 ring-inset ring-slate-200" />
      </section>
    </div>
  </div>
</template>
