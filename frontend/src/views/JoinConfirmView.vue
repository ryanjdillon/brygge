<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { Loader2, CheckCircle, AlertTriangle } from 'lucide-vue-next'
import { useApiClient, unwrap, ApiError } from '@/lib/apiClient'
import { useAuthStore } from '@/stores/auth'
import { openPayLink } from '@/lib/joinFlow'

// The page the confirmation email links to. Opening it changes nothing: a
// mail scanner that prefetches the link must not use the token up, so only
// the button confirms (POST).

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const client = useApiClient()
const auth = useAuthStore()

const token = typeof route.query.token === 'string' ? route.query.token : ''

type Problem =
  | 'join_token_invalid'
  | 'join_token_used'
  | 'join_token_expired'
  | 'join_closed'
  | 'already_member'
  | 'rejoin_blocked'
  | 'failed'

const KNOWN: readonly string[] = [
  'join_token_invalid',
  'join_token_used',
  'join_token_expired',
  'join_closed',
  'already_member',
  'rejoin_blocked',
]

const state = ref<'loading' | 'ready' | 'confirming' | 'problem'>('loading')
const firstName = ref('')
const problem = ref<Problem>('failed')

function asProblem(code: string | undefined): Problem {
  return code && KNOWN.includes(code) ? (code as Problem) : 'failed'
}

// Each problem gets its own message and the one next step that helps.
const action = computed<{ to: string; label: string } | null>(() => {
  switch (problem.value) {
    case 'join_token_invalid':
    case 'join_token_expired':
      return { to: '/join', label: t('join.confirm.registerAgain') }
    case 'join_token_used':
    case 'already_member':
      return { to: '/login', label: t('join.confirm.login') }
    case 'join_closed':
    case 'rejoin_blocked':
      return { to: '/contact', label: t('join.confirm.contact') }
    default:
      return null
  }
})

async function load() {
  if (!token) {
    problem.value = 'join_token_invalid'
    state.value = 'problem'
    return
  }
  try {
    const res = unwrap(
      await client.GET('/api/v1/join/confirm/{token}', { params: { path: { token } } }),
    )
    if (!res.valid) {
      problem.value = asProblem(res.code)
      state.value = 'problem'
      return
    }
    firstName.value = res.first_name
    state.value = 'ready'
  } catch {
    problem.value = 'failed'
    state.value = 'problem'
  }
}

async function confirm() {
  state.value = 'confirming'
  let checkoutUrl: string
  try {
    const res = unwrap(await client.POST('/api/v1/join/confirm', { body: { token } }))
    checkoutUrl = res.checkout_url
  } catch (err) {
    problem.value = err instanceof ApiError ? asProblem(err.code) : 'failed'
    state.value = 'problem'
    return
  }
  // The confirm set the session cookie: load the applicant's session so the
  // router lets them reach the pages that finish joining.
  await auth.checkSession()
  await openPayLink(router, checkoutUrl)
}

onMounted(load)
</script>

<template>
  <div class="mx-auto max-w-xl px-4 py-8 sm:px-6 sm:py-12">
    <div v-if="state === 'loading'" class="py-16 text-center" role="status">
      <Loader2 class="mx-auto h-8 w-8 animate-spin text-slate-400" aria-hidden="true" />
      <span class="sr-only">{{ t('common.loading') }}</span>
    </div>

    <div v-else-if="state === 'problem'" class="rounded-lg border border-slate-200 bg-white p-6" role="alert" data-testid="confirm-problem">
      <AlertTriangle class="h-8 w-8 text-amber-500" aria-hidden="true" />
      <h1 class="mt-3 text-xl font-bold text-slate-900">{{ t(`join.confirm.problems.${problem}.title`) }}</h1>
      <p class="mt-2 text-slate-600" :data-testid="`confirm-problem-${problem}`">
        {{ t(`join.confirm.problems.${problem}.body`) }}
      </p>
      <RouterLink
        v-if="action"
        :to="action.to"
        class="mt-4 inline-flex min-h-11 w-full items-center justify-center rounded-md bg-brand-600 px-4 py-2 text-sm font-semibold text-white hover:bg-brand-700 sm:w-auto"
        data-testid="confirm-action"
      >
        {{ action.label }}
      </RouterLink>
    </div>

    <div v-else class="rounded-lg border border-slate-200 bg-white p-6" data-testid="confirm-ready">
      <CheckCircle class="h-8 w-8 text-brand-600" aria-hidden="true" />
      <h1 class="mt-3 text-xl font-bold text-slate-900 sm:text-2xl">
        {{ t('join.confirm.title', { name: firstName }) }}
      </h1>
      <p class="mt-2 text-slate-600">{{ t('join.confirm.body') }}</p>
      <button
        type="button"
        :disabled="state === 'confirming'"
        class="mt-6 inline-flex min-h-11 w-full items-center justify-center gap-2 rounded-md bg-brand-600 px-6 py-3 text-base font-semibold text-white hover:bg-brand-700 disabled:cursor-not-allowed disabled:opacity-60"
        data-testid="confirm-button"
        @click="confirm"
      >
        <Loader2 v-if="state === 'confirming'" class="h-4 w-4 animate-spin" aria-hidden="true" />
        {{ t('join.confirm.button') }}
      </button>
    </div>
  </div>
</template>
