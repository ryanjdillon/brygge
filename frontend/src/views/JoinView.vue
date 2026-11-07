<script setup lang="ts">
import { ref, reactive, computed, onMounted, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import { Loader2, MailCheck, UserCheck, Clock, ExternalLink } from 'lucide-vue-next'
import Input from '@/components/ui/form/Input.vue'
import FormField from '@/components/ui/form/FormField.vue'
import AddressCombobox, { type RegisterAddress } from '@/components/address/AddressCombobox.vue'
import { useApiClient, unwrap, ApiError } from '@/lib/apiClient'
import { useAuthStore } from '@/stores/auth'
import { formatNOK } from '@/lib/format'
import { APPLICANT_COMPLETE_PATH } from '@/lib/applicantAccess'
import { JOIN_LEGAL_DOCS, legalDocPath, type JoinLegalDoc } from '@/lib/joinFlow'
import { normalizePhone } from '@/lib/phone'

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
  acceptTerms: false,
  newsletter: false,
  // Honeypot: hidden from people, filled only by scripts.
  website: '',
})

// The home address is searched in Kartverket's register and picked, or typed
// by hand when it can't be found or the register is unavailable. Residency is
// derived from a picked address on the server; it is never asked.
const addressMode = ref<'search' | 'picked' | 'manual'>('search')
const picked = ref<RegisterAddress | null>(null)
const addressUnavailable = ref(false)
const addressSearch = ref<InstanceType<typeof AddressCombobox> | null>(null)

type Field = 'firstName' | 'lastName' | 'email' | 'phone' | 'address' | 'addressLine' | 'postalCode' | 'city' | 'acceptTerms'
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

async function selectAddress(a: RegisterAddress) {
  picked.value = a
  addressMode.value = 'picked'
  delete errors.value.address
  await nextTick()
  document.getElementById('join-address-change')?.focus()
}

async function searchAgain() {
  picked.value = null
  addressMode.value = 'search'
  await nextTick()
  addressSearch.value?.focus()
}

async function enterManually(unavailable = false) {
  addressUnavailable.value = unavailable
  picked.value = null
  addressMode.value = 'manual'
  delete errors.value.address
  await nextTick()
  if (!unavailable) document.getElementById('join-address')?.focus()
}

function validate(): boolean {
  const e: Partial<Record<Field, string>> = {}
  if (!form.firstName.trim()) e.firstName = t('join.form.required')
  if (!form.lastName.trim()) e.lastName = t('join.form.required')
  if (!form.email.trim()) e.email = t('join.form.required')
  else if (!EMAIL_RE.test(form.email.trim())) e.email = t('join.form.invalidEmail')
  if (!form.phone.trim()) e.phone = t('join.form.required')
  else if (!normalizePhone(form.phone)) e.phone = t('join.form.invalidPhone')
  if (addressMode.value === 'search') e.address = t('join.form.address.pickRequired')
  if (addressMode.value === 'manual') {
    if (!form.addressLine.trim()) e.addressLine = t('join.form.required')
    if (!form.postalCode.trim()) e.postalCode = t('join.form.required')
    if (!form.city.trim()) e.city = t('join.form.required')
  }
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
  ['phone', 'join-phone'],
  ['address', 'join-address-search'],
  ['addressLine', 'join-address'],
  ['postalCode', 'join-postal-code'],
  ['city', 'join-city'],
  ['acceptTerms', 'join-accept'],
]

async function focusFirstError() {
  await nextTick()
  const first = FIELD_FOCUS.find(([f]) => errors.value[f])
  if (first) document.getElementById(first[1])?.focus()
}

// A picked address goes with its register id: the server looks it up again
// and keeps the register's copy. The fields are sent too, so the join is
// saved (unverified) even if the register is down by then.
function addressBody() {
  const a = picked.value
  if (addressMode.value === 'picked' && a) {
    return { address_id: a.id, address_line: a.street, postal_code: a.postal_code, city: a.city }
  }
  return { address_line: form.addressLine.trim(), postal_code: form.postalCode.trim(), city: form.city.trim() }
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
          phone: normalizePhone(form.phone) ?? form.phone.trim(),
          ...addressBody(),
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

      <FormField :label="t('join.form.phone')" for="join-phone" required>
        <Input id="join-phone" v-model="form.phone" :error="errors.phone" type="tel" name="phone" autocomplete="tel" inputmode="tel" :maxlength="40" :input-class="inputClass" />
      </FormField>

      <fieldset class="space-y-3" data-testid="join-address">
        <legend class="mb-1 block text-xs font-medium text-gray-700">
          {{ t('join.form.address.label') }} <span class="text-red-500">*</span>
        </legend>
        <p id="join-address-helper" class="text-xs text-gray-500">{{ t('join.form.address.helper') }}</p>

        <template v-if="addressMode === 'search'">
          <label for="join-address-search" class="sr-only">{{ t('join.form.address.label') }}</label>
          <AddressCombobox
            id="join-address-search"
            ref="addressSearch"
            :error="errors.address"
            describedby="join-address-helper"
            @select="selectAddress"
            @unavailable="enterManually(true)"
          />
          <button
            type="button"
            class="inline-flex min-h-11 items-center text-left text-sm text-brand-700 underline"
            data-testid="join-address-manual"
            @click="enterManually()"
          >
            {{ t('join.form.address.manualLink') }}
          </button>
        </template>

        <div
          v-else-if="addressMode === 'picked' && picked"
          class="flex items-start justify-between gap-3 rounded-md border border-slate-200 bg-slate-50 px-4 py-3"
          data-testid="join-address-picked"
        >
          <p class="text-sm text-slate-900">
            <span class="block font-medium">{{ picked.street }}</span>
            <span class="block">{{ picked.postal_code }} {{ picked.city }}</span>
          </p>
          <button
            id="join-address-change"
            type="button"
            class="inline-flex min-h-11 flex-none items-center rounded-md border border-slate-300 bg-white px-4 text-sm font-semibold text-slate-700 hover:bg-slate-50"
            data-testid="join-address-change"
            @click="searchAgain"
          >
            {{ t('join.form.address.change') }}
          </button>
        </div>

        <template v-else>
          <p
            v-if="addressUnavailable"
            class="rounded-md bg-amber-50 p-3 text-sm text-amber-900"
            role="status"
            data-testid="join-address-unavailable"
          >
            {{ t('join.form.address.unavailable') }}
          </p>
          <FormField :label="t('join.form.addressLine')" for="join-address" required>
            <Input id="join-address" v-model="form.addressLine" :error="errors.addressLine" name="address_line" autocomplete="address-line1" :maxlength="200" :input-class="inputClass" />
          </FormField>
          <div class="grid grid-cols-1 gap-4 md:grid-cols-[10rem_1fr]">
            <FormField :label="t('join.form.postalCode')" for="join-postal-code" required>
              <Input id="join-postal-code" v-model="form.postalCode" :error="errors.postalCode" name="postal_code" autocomplete="postal-code" inputmode="numeric" :maxlength="20" :input-class="inputClass" />
            </FormField>
            <FormField :label="t('join.form.city')" for="join-city" required>
              <Input id="join-city" v-model="form.city" :error="errors.city" name="city" autocomplete="address-level2" :maxlength="100" :input-class="inputClass" />
            </FormField>
          </div>
          <button
            type="button"
            class="inline-flex min-h-11 items-center text-left text-sm text-brand-700 underline"
            data-testid="join-address-search-again"
            @click="searchAgain"
          >
            {{ t('join.form.address.searchLink') }}
          </button>
        </template>
        <p v-if="addressMode === 'picked'" class="text-xs text-gray-500">{{ t('join.form.address.attribution') }}</p>
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
