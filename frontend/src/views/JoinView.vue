<script setup lang="ts">
import { ref, reactive, computed, onMounted, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import { Loader2, MailCheck, UserCheck, Clock, ExternalLink } from 'lucide-vue-next'
import Input from '@/components/ui/form/Input.vue'
import FormField from '@/components/ui/form/FormField.vue'
import { useApiClient, unwrap, ApiError } from '@/lib/apiClient'
import { useAuthStore } from '@/stores/auth'
import { formatNOK } from '@/lib/format'
import { APPLICANT_COMPLETE_PATH } from '@/lib/applicantAccess'
import { JOIN_LEGAL_DOCS, legalDocPath, type JoinLegalDoc } from '@/lib/joinFlow'

const { t } = useI18n()
const client = useApiClient()
const auth = useAuthStore()

type Price = { amount: number; currency: string; name: string; description: string }

const state = ref<'loading' | 'closed' | 'form' | 'sent' | 'error'>('loading')
const price = ref<Price | null>(null)
// The server's year when it gives one, so the label matches the dues it bills.
const year = ref(new Date().getFullYear())

// Versions of the documents shown, sent with the form so the server records
// consent to exactly these texts. Sent only when all of them loaded.
const versions = ref<Partial<Record<JoinLegalDoc, string>>>({})

const form = reactive({
  firstName: '',
  lastName: '',
  email: '',
  phone: '',
  addressLine: '',
  postalCode: '',
  city: '',
  isLocal: null as boolean | null,
  acceptTerms: false,
  newsletter: false,
  // Honeypot: hidden from people, filled only by scripts.
  website: '',
})

type Field = 'firstName' | 'lastName' | 'email' | 'postalCode' | 'city' | 'isLocal' | 'acceptTerms'
const errors = ref<Partial<Record<Field, string>>>({})
const submitting = ref(false)
const submitError = ref('')
const termsChanged = ref(false)

// A signed-in user is either finishing a join already or has an account.
const alreadyMember = computed(() => auth.isAuthenticated && !auth.isApplicant)
const applicant = computed(() => auth.isApplicant)

const inputClass = 'min-h-11 text-base sm:text-sm'

const docLabels = computed<Record<JoinLegalDoc, string>>(() => ({
  statutes: t('join.legal.statutes'),
  harbour_rules: t('join.legal.harbour_rules'),
  privacy: t('join.legal.privacy'),
  terms_of_sale: t('join.legal.terms_of_sale'),
}))

async function loadLegalVersions() {
  const loaded: Partial<Record<JoinLegalDoc, string>> = {}
  await Promise.all(
    JOIN_LEGAL_DOCS.map(async (doc) => {
      try {
        const res = unwrap(await client.GET('/api/v1/legal/{docType}', { params: { path: { docType: doc } } }))
        loaded[doc] = res.version
      } catch {
        // A missing document closes joining server-side; the form then
        // shows the closed state from /join/status instead.
      }
    }),
  )
  versions.value = loaded
}

async function load() {
  if (alreadyMember.value || applicant.value) return
  state.value = 'loading'
  try {
    const status = unwrap(await client.GET('/api/v1/join/status'))
    if (!status.open) {
      state.value = 'closed'
      return
    }
    price.value = status.price ?? null
    if (status.year) year.value = status.year
    await loadLegalVersions()
    state.value = 'form'
  } catch {
    state.value = 'error'
  }
}

function consentVersions(): Record<string, string> | undefined {
  const v = versions.value
  if (!JOIN_LEGAL_DOCS.every((d) => v[d])) return undefined
  return { ...v } as Record<string, string>
}

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

function validate(): boolean {
  const e: Partial<Record<Field, string>> = {}
  if (!form.firstName.trim()) e.firstName = t('join.form.required')
  if (!form.lastName.trim()) e.lastName = t('join.form.required')
  if (!form.email.trim()) e.email = t('join.form.required')
  else if (!EMAIL_RE.test(form.email.trim())) e.email = t('join.form.invalidEmail')
  if (!form.postalCode.trim()) e.postalCode = t('join.form.required')
  if (!form.city.trim()) e.city = t('join.form.required')
  if (form.isLocal === null) e.isLocal = t('join.form.chooseOne')
  if (!form.acceptTerms) e.acceptTerms = t('join.form.acceptRequired')
  errors.value = e
  return Object.keys(e).length === 0
}

// The element each field's error belongs to, in form order: the first one
// with an error gets focus after a failed submit.
const FIELD_FOCUS: [Field, string][] = [
  ['firstName', 'join-first-name'],
  ['lastName', 'join-last-name'],
  ['email', 'join-email'],
  ['postalCode', 'join-postal-code'],
  ['city', 'join-city'],
  ['isLocal', 'join-local-yes'],
  ['acceptTerms', 'join-accept'],
]

async function focusFirstError() {
  await nextTick()
  const first = FIELD_FOCUS.find(([f]) => errors.value[f])
  if (first) document.getElementById(first[1])?.focus()
}

async function handleSubmit() {
  submitError.value = ''
  if (!validate()) {
    await focusFirstError()
    return
  }
  submitting.value = true
  try {
    unwrap(
      await client.POST('/api/v1/join', {
        body: {
          first_name: form.firstName.trim(),
          last_name: form.lastName.trim(),
          email: form.email.trim(),
          phone: form.phone.trim() || undefined,
          address_line: form.addressLine.trim() || undefined,
          postal_code: form.postalCode.trim(),
          city: form.city.trim(),
          is_local: form.isLocal === true,
          newsletter: form.newsletter,
          accept_terms: form.acceptTerms,
          consent_versions: consentVersions(),
          website: form.website || undefined,
        },
      }),
    )
    state.value = 'sent'
  } catch (err) {
    await handleSubmitError(err)
  } finally {
    submitting.value = false
  }
}

async function handleSubmitError(err: unknown) {
  if (err instanceof ApiError) {
    if (err.status === 409 && err.code === 'legal_document_stale') {
      // The texts changed after the page loaded: show the new ones and
      // ask for the tick again, so the consent matches what was shown.
      await loadLegalVersions()
      form.acceptTerms = false
      termsChanged.value = true
      return
    }
    if (err.status === 409 && err.code === 'join_closed') {
      state.value = 'closed'
      return
    }
    if (err.status === 429) {
      submitError.value = t('join.form.rateLimited')
      return
    }
    if (err.status === 400) {
      submitError.value = t('join.form.badRequest')
      return
    }
  }
  submitError.value = t('join.form.failed')
}

onMounted(load)
</script>

<template>
  <div class="mx-auto max-w-xl px-4 py-8 sm:px-6 sm:py-12">
    <h1 class="text-2xl font-bold text-slate-900 sm:text-3xl">{{ t('join.title') }}</h1>

    <div v-if="alreadyMember" class="mt-6 rounded-lg border border-slate-200 bg-white p-6" data-testid="join-already-member">
      <UserCheck class="h-8 w-8 text-brand-600" aria-hidden="true" />
      <h2 class="mt-3 text-lg font-semibold text-slate-900">{{ t('join.alreadyMember.title') }}</h2>
      <p class="mt-2 text-slate-600">{{ t('join.alreadyMember.body') }}</p>
      <RouterLink
        to="/portal"
        class="mt-4 inline-flex min-h-11 w-full items-center justify-center rounded-md bg-brand-600 px-4 py-2 text-sm font-semibold text-white hover:bg-brand-700 sm:w-auto"
      >
        {{ t('join.alreadyMember.portal') }}
      </RouterLink>
    </div>

    <div v-else-if="applicant" class="mt-6 rounded-lg border border-slate-200 bg-white p-6" data-testid="join-applicant">
      <Clock class="h-8 w-8 text-brand-600" aria-hidden="true" />
      <h2 class="mt-3 text-lg font-semibold text-slate-900">{{ t('join.applicant.title') }}</h2>
      <p class="mt-2 text-slate-600">{{ t('join.applicant.body') }}</p>
      <RouterLink
        :to="APPLICANT_COMPLETE_PATH"
        class="mt-4 inline-flex min-h-11 w-full items-center justify-center rounded-md bg-brand-600 px-4 py-2 text-sm font-semibold text-white hover:bg-brand-700 sm:w-auto"
      >
        {{ t('join.applicant.continue') }}
      </RouterLink>
    </div>

    <div v-else-if="state === 'loading'" class="py-16 text-center" role="status">
      <Loader2 class="mx-auto h-8 w-8 animate-spin text-slate-400" aria-hidden="true" />
      <span class="sr-only">{{ t('common.loading') }}</span>
    </div>

    <div v-else-if="state === 'closed'" class="mt-6 rounded-lg border border-slate-200 bg-white p-6" data-testid="join-closed">
      <h2 class="text-lg font-semibold text-slate-900">{{ t('join.closed.title') }}</h2>
      <p class="mt-2 text-slate-600">{{ t('join.closed.body') }}</p>
      <RouterLink
        to="/contact"
        class="mt-4 inline-flex min-h-11 w-full items-center justify-center rounded-md border border-slate-300 px-4 py-2 text-sm font-semibold text-slate-700 hover:bg-slate-50 sm:w-auto"
      >
        {{ t('join.closed.contact') }}
      </RouterLink>
    </div>

    <div v-else-if="state === 'error'" class="mt-6 rounded-lg border border-red-200 bg-red-50 p-6" data-testid="join-error">
      <p class="text-red-800">{{ t('join.loadFailed') }}</p>
      <button
        type="button"
        class="mt-4 inline-flex min-h-11 items-center justify-center rounded-md border border-red-300 bg-white px-4 py-2 text-sm font-semibold text-red-800"
        @click="load"
      >
        {{ t('join.retry') }}
      </button>
    </div>

    <div v-else-if="state === 'sent'" class="mt-6 rounded-lg border border-green-200 bg-green-50 p-6" role="status" data-testid="join-sent">
      <MailCheck class="h-8 w-8 text-green-700" aria-hidden="true" />
      <h2 class="mt-3 text-lg font-semibold text-green-900">{{ t('join.sent.title') }}</h2>
      <p class="mt-2 text-green-800">{{ t('join.sent.body') }}</p>
      <p class="mt-2 text-sm text-green-800">{{ t('join.sent.spam') }}</p>
    </div>

    <form v-else class="mt-6 space-y-5" novalidate data-testid="join-form" @submit.prevent="handleSubmit">
      <p class="text-slate-600">{{ t('join.intro') }}</p>

      <p
        v-if="Object.keys(errors).length"
        class="rounded-md bg-red-50 p-3 text-sm font-medium text-red-800"
        role="alert"
        data-testid="join-error-summary"
      >
        {{ t('join.form.checkFields') }}
      </p>

      <p v-if="price" class="rounded-md bg-slate-50 px-4 py-3 text-sm font-medium text-slate-800" data-testid="join-price">
        {{ t('join.form.price', { year, amount: formatNOK(price.amount) }) }}
      </p>

      <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
        <FormField :label="t('join.form.firstName')" for="join-first-name" required>
          <Input id="join-first-name" v-model="form.firstName" :error="errors.firstName" name="first_name" autocomplete="given-name" autocapitalize="words" :maxlength="100" :input-class="inputClass" />
        </FormField>
        <FormField :label="t('join.form.lastName')" for="join-last-name" required>
          <Input id="join-last-name" v-model="form.lastName" :error="errors.lastName" name="last_name" autocomplete="family-name" autocapitalize="words" :maxlength="100" :input-class="inputClass" />
        </FormField>
      </div>

      <FormField :label="t('join.form.email')" for="join-email" required>
        <Input id="join-email" v-model="form.email" :error="errors.email" type="email" name="email" autocomplete="email" inputmode="email" autocapitalize="off" spellcheck="false" :maxlength="254" :input-class="inputClass" />
      </FormField>

      <FormField :label="t('join.form.phone')" for="join-phone" :helper-text="t('join.form.optional')">
        <Input id="join-phone" v-model="form.phone" type="tel" name="phone" autocomplete="tel" inputmode="tel" :maxlength="40" :input-class="inputClass" />
      </FormField>

      <FormField :label="t('join.form.addressLine')" for="join-address" :helper-text="t('join.form.optional')">
        <Input id="join-address" v-model="form.addressLine" name="address_line" autocomplete="street-address" :maxlength="200" :input-class="inputClass" />
      </FormField>

      <div class="grid grid-cols-1 gap-4 md:grid-cols-[10rem_1fr]">
        <FormField :label="t('join.form.postalCode')" for="join-postal-code" required>
          <Input id="join-postal-code" v-model="form.postalCode" :error="errors.postalCode" name="postal_code" autocomplete="postal-code" inputmode="numeric" :maxlength="20" :input-class="inputClass" />
        </FormField>
        <FormField :label="t('join.form.city')" for="join-city" required>
          <Input id="join-city" v-model="form.city" :error="errors.city" name="city" autocomplete="address-level2" :maxlength="100" :input-class="inputClass" />
        </FormField>
      </div>

      <fieldset
        role="radiogroup"
        :aria-invalid="!!errors.isLocal"
        :aria-describedby="errors.isLocal ? 'join-local-error' : undefined"
      >
        <legend class="mb-2 block text-xs font-medium text-gray-700">
          {{ t('join.form.isLocal') }} <span class="text-red-500">*</span>
        </legend>
        <div class="grid grid-cols-2 gap-3">
          <label
            v-for="opt in [{ v: true, l: t('common.yes'), id: 'join-local-yes' }, { v: false, l: t('common.no'), id: 'join-local-no' }]"
            :key="opt.id"
            :for="opt.id"
            class="flex min-h-11 cursor-pointer items-center gap-3 rounded-md border px-4 text-sm"
            :class="form.isLocal === opt.v ? 'border-brand-500 bg-brand-50 text-brand-900' : 'border-gray-300 text-gray-800'"
          >
            <input
              :id="opt.id"
              type="radio"
              name="is_local"
              class="h-4 w-4 border-gray-300 text-brand-600 focus:ring-1 focus:ring-brand-500"
              :checked="form.isLocal === opt.v"
              @change="form.isLocal = opt.v"
            />
            {{ opt.l }}
          </label>
        </div>
        <p v-if="errors.isLocal" id="join-local-error" class="mt-1 text-xs text-red-600">{{ errors.isLocal }}</p>
      </fieldset>

      <div class="space-y-3 rounded-md border border-slate-200 p-4">
        <p v-if="termsChanged" class="rounded-md bg-amber-50 p-3 text-sm text-amber-900" role="alert" data-testid="join-terms-changed">
          {{ t('join.form.termsChanged') }}
        </p>
        <ul class="flex flex-wrap gap-x-4 gap-y-1 text-sm" :aria-label="t('join.form.readDocuments')">
          <li v-for="doc in JOIN_LEGAL_DOCS" :key="doc">
            <a
              :href="legalDocPath(doc)"
              target="_blank"
              rel="noopener noreferrer"
              class="inline-flex min-h-11 items-center gap-1 text-brand-700 underline"
              :data-testid="`join-doc-${doc}`"
            >
              {{ docLabels[doc] }}
              <ExternalLink class="h-3.5 w-3.5" aria-hidden="true" />
              <span class="sr-only">{{ t('join.form.opensNewTab') }}</span>
            </a>
          </li>
        </ul>
        <label for="join-accept" class="flex min-h-11 cursor-pointer items-start gap-3 text-sm text-gray-800">
          <input
            id="join-accept"
            v-model="form.acceptTerms"
            type="checkbox"
            name="accept_terms"
            class="mt-0.5 h-5 w-5 flex-none rounded border-gray-300 text-brand-600 focus:ring-1 focus:ring-brand-500"
            :aria-invalid="!!errors.acceptTerms"
            :aria-describedby="errors.acceptTerms ? 'join-accept-error' : undefined"
          />
          <span>{{ t('join.form.acceptTerms') }} <span class="text-red-500">*</span></span>
        </label>
        <p v-if="errors.acceptTerms" id="join-accept-error" class="text-xs text-red-600">{{ errors.acceptTerms }}</p>
        <label for="join-newsletter" class="flex min-h-11 cursor-pointer items-start gap-3 text-sm text-gray-800">
          <input
            id="join-newsletter"
            v-model="form.newsletter"
            type="checkbox"
            name="newsletter"
            class="mt-0.5 h-5 w-5 flex-none rounded border-gray-300 text-brand-600 focus:ring-1 focus:ring-brand-500"
          />
          <span>{{ t('join.form.newsletter') }}</span>
        </label>
      </div>

      <div class="absolute -left-[9999px] h-px w-px overflow-hidden" aria-hidden="true">
        <label for="join-website">Website</label>
        <input
          id="join-website"
          v-model="form.website"
          type="text"
          name="website"
          tabindex="-1"
          autocomplete="new-password"
          data-1p-ignore
          data-lpignore="true"
        />
      </div>

      <p v-if="submitError" class="rounded-md bg-red-50 p-3 text-sm text-red-800" role="alert" data-testid="join-submit-error">
        {{ submitError }}
      </p>

      <button
        type="submit"
        :disabled="submitting"
        class="inline-flex min-h-11 w-full items-center justify-center gap-2 rounded-md bg-brand-600 px-6 py-3 text-base font-semibold text-white hover:bg-brand-700 disabled:cursor-not-allowed disabled:opacity-60"
        data-testid="join-submit"
      >
        <Loader2 v-if="submitting" class="h-4 w-4 animate-spin" aria-hidden="true" />
        {{ submitting ? t('join.form.submitting') : t('join.form.submit') }}
      </button>
    </form>
  </div>
</template>
