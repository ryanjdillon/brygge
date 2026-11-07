<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { Loader2, LogIn, AlertTriangle } from 'lucide-vue-next'
import { useApiClient, unwrap, ApiError } from '@/lib/apiClient'
import { useAuthStore } from '@/stores/auth'

// The page the login email links to. Opening it changes nothing: a mail
// scanner that prefetches the link must not use the token up, so only the
// button logs in (POST). The token is kept in memory and dropped from the
// address bar and the back/forward history (router.replace). The browser's
// global history still records the URL that was opened; that is acceptable
// because the token is single-use and expires after 15 minutes. A reload
// after the token was dropped therefore has no token, and the page asks the
// member to open the link from the email again.

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const client = useApiClient()
const auth = useAuthStore()

const token = typeof route.query.token === 'string' ? route.query.token : ''

type Problem =
  | 'login_token_missing'
  | 'login_token_invalid'
  | 'login_token_used'
  | 'login_token_expired'
  | 'rate_limited'
  | 'unavailable'
  | 'failed'

const KNOWN: readonly string[] = ['login_token_invalid', 'login_token_used', 'login_token_expired']

// The strict limiter refuses before the token is read (429 over the budget,
// 503 while it cannot read Redis), so the token kept in memory still works:
// these offer the login button again instead of only a new link.
const RETRYABLE: readonly Problem[] = ['rate_limited', 'unavailable']

// 'session': the POST logged in but the session could not be loaded.
const state = ref<'ready' | 'verifying' | 'problem' | 'session'>(token ? 'ready' : 'problem')
const problem = ref<Problem>('login_token_missing')
const retrying = ref(false)

function asProblem(err: unknown): Problem {
  if (!(err instanceof ApiError)) return 'failed'
  if (err.status === 429) return 'rate_limited'
  if (err.status === 503) return 'unavailable'
  return err.code && KNOWN.includes(err.code) ? (err.code as Problem) : 'failed'
}

async function login() {
  state.value = 'verifying'
  try {
    unwrap(await client.POST('/api/v1/auth/verify', { body: { token } }))
  } catch (err) {
    problem.value = asProblem(err)
    state.value = 'problem'
    return
  }
  await openPortal()
}

// The POST set the session cookie: load the session so the router lets the
// member into the portal (an applicant is sent on to finish joining). If it
// cannot be loaded, say so here rather than let the guard bounce to /login.
async function openPortal() {
  await auth.checkSession()
  if (!auth.isAuthenticated) {
    state.value = 'session'
    return
  }
  await router.replace('/portal')
}

async function retrySession() {
  retrying.value = true
  try {
    await openPortal()
  } finally {
    retrying.value = false
  }
}

onMounted(() => {
  if (route.query.token !== undefined) {
    const query = { ...route.query }
    delete query.token
    void router.replace({ path: route.path, query, hash: route.hash })
  }
})
</script>

<template>
  <div class="mx-auto max-w-xl px-4 py-8 sm:px-6 sm:py-12">
    <div v-if="state === 'problem'" class="rounded-lg border border-slate-200 bg-white p-6" role="alert" data-testid="verify-problem">
      <AlertTriangle class="h-8 w-8 text-amber-500" aria-hidden="true" />
      <h1 class="mt-3 text-xl font-bold text-slate-900">{{ t(`login.verify.problems.${problem}.title`) }}</h1>
      <p class="mt-2 text-slate-600" :data-testid="`verify-problem-${problem}`">
        {{ t(`login.verify.problems.${problem}.body`) }}
      </p>
      <div v-if="RETRYABLE.includes(problem)" class="mt-4 flex flex-col gap-2 sm:flex-row">
        <button
          type="button"
          class="inline-flex min-h-11 w-full items-center justify-center gap-2 rounded-md bg-brand-600 px-4 py-2 text-sm font-semibold text-white hover:bg-brand-700 sm:w-auto"
          data-testid="verify-retry"
          @click="login"
        >
          {{ t('login.verify.retry') }}
        </button>
        <RouterLink
          to="/login"
          class="inline-flex min-h-11 w-full items-center justify-center rounded-md border border-slate-300 px-4 py-2 text-sm font-semibold text-slate-700 hover:bg-slate-50 sm:w-auto"
          data-testid="verify-request-new"
        >
          {{ t('login.verify.requestNew') }}
        </RouterLink>
      </div>
      <RouterLink
        v-else
        to="/login"
        class="mt-4 inline-flex min-h-11 w-full items-center justify-center rounded-md bg-brand-600 px-4 py-2 text-sm font-semibold text-white hover:bg-brand-700 sm:w-auto"
        data-testid="verify-request-new"
      >
        {{ t('login.verify.requestNew') }}
      </RouterLink>
    </div>

    <div
      v-else-if="state === 'session'"
      class="rounded-lg border border-slate-200 bg-white p-6"
      role="alert"
      data-testid="verify-session-failed"
    >
      <AlertTriangle class="h-8 w-8 text-amber-500" aria-hidden="true" />
      <h1 class="mt-3 text-xl font-bold text-slate-900">{{ t('login.verify.sessionFailed.title') }}</h1>
      <p class="mt-2 text-slate-600">{{ t('login.verify.sessionFailed.body') }}</p>
      <div class="mt-4 flex flex-col gap-2 sm:flex-row">
        <button
          type="button"
          :disabled="retrying"
          class="inline-flex min-h-11 w-full items-center justify-center gap-2 rounded-md bg-brand-600 px-4 py-2 text-sm font-semibold text-white hover:bg-brand-700 disabled:cursor-not-allowed disabled:opacity-60 sm:w-auto"
          data-testid="verify-session-retry"
          @click="retrySession"
        >
          <Loader2 v-if="retrying" class="h-4 w-4 animate-spin" aria-hidden="true" />
          {{ t('login.verify.sessionFailed.retry') }}
        </button>
        <RouterLink
          to="/portal"
          class="inline-flex min-h-11 w-full items-center justify-center rounded-md border border-slate-300 px-4 py-2 text-sm font-semibold text-slate-700 hover:bg-slate-50 sm:w-auto"
          data-testid="verify-session-portal"
        >
          {{ t('login.verify.sessionFailed.portal') }}
        </RouterLink>
      </div>
    </div>

    <div v-else class="rounded-lg border border-slate-200 bg-white p-6" data-testid="verify-ready">
      <LogIn class="h-8 w-8 text-brand-600" aria-hidden="true" />
      <h1 class="mt-3 text-xl font-bold text-slate-900 sm:text-2xl">{{ t('login.verify.title') }}</h1>
      <p class="mt-2 text-slate-600">{{ t('login.verify.body') }}</p>
      <button
        type="button"
        :disabled="state === 'verifying'"
        class="mt-6 inline-flex min-h-11 w-full items-center justify-center gap-2 rounded-md bg-brand-600 px-6 py-3 text-base font-semibold text-white hover:bg-brand-700 disabled:cursor-not-allowed disabled:opacity-60"
        data-testid="verify-button"
        @click="login"
      >
        <Loader2 v-if="state === 'verifying'" class="h-4 w-4 animate-spin" aria-hidden="true" />
        {{ t('login.verify.button') }}
      </button>
    </div>
  </div>
</template>
