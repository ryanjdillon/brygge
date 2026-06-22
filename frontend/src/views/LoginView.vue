<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { LogIn, Users } from 'lucide-vue-next'
import Input from '@/components/ui/form/Input.vue'
import FormField from '@/components/ui/form/FormField.vue'

const { t } = useI18n()
const router = useRouter()
const auth = useAuthStore()

const email = ref('')
const submitting = ref(false)
const magicLinkSent = ref(false)

interface DemoUser {
  email: string
  name: string
  roles: string[]
  description: string
}

const demoUsers = ref<DemoUser[]>([])
const demoLoading = ref('')

onMounted(async () => {
  // Try to fetch demo users — endpoint only exists when FEATURE_DEMO_AUTH is enabled
  try {
    const res = await fetch('/api/v1/auth/demo/users', { credentials: 'include' })
    if (res.ok) {
      demoUsers.value = await res.json()
    }
  } catch {
    // demo auth not available
  }
})

async function handleSubmit() {
  submitting.value = true
  const ok = await auth.requestMagicLink(email.value)
  submitting.value = false
  if (ok) {
    magicLinkSent.value = true
  }
}

async function handleDemoLogin(user: DemoUser) {
  demoLoading.value = user.email
  try {
    const res = await fetch('/api/v1/auth/demo/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify({ email: user.email }),
    })
    if (res.ok) {
      await auth.checkSession()
      const redirect = user.roles.includes('admin') || user.roles.includes('board') ? '/admin' : '/portal'
      router.push(redirect)
    }
  } catch {
    auth.loginError = 'Demo login failed'
  } finally {
    demoLoading.value = ''
  }
}

function roleColor(role: string): string {
  switch (role) {
    case 'admin': return 'bg-red-100 text-red-700'
    case 'board': return 'bg-purple-100 text-purple-700'
    case 'member': return 'bg-brand-100 text-brand-700'
    default: return 'bg-gray-100 text-gray-700'
  }
}
</script>

<template>
  <div class="flex min-h-[calc(100vh-8rem)] items-center justify-center px-4">
    <div class="w-full max-w-sm">
      <div class="text-center">
        <LogIn class="mx-auto h-10 w-10 text-brand-600" />
        <h1 class="mt-4 text-2xl font-bold text-gray-900">{{ t('login.title') }}</h1>
      </div>

      <div class="mt-8 space-y-5">
        <!-- Demo login buttons -->
        <div v-if="demoUsers.length > 0" class="space-y-3">
          <div class="flex items-center gap-2 text-sm font-medium text-gray-500">
            <Users class="h-4 w-4" />
            Demo / Test
          </div>
          <button
            v-for="user in demoUsers"
            :key="user.email"
            :disabled="demoLoading === user.email"
            class="flex w-full items-center justify-between rounded-md border border-gray-200 px-4 py-3 text-left text-sm transition hover:border-brand-300 hover:bg-brand-50 disabled:opacity-50"
            @click="handleDemoLogin(user)"
          >
            <div>
              <div class="font-medium text-gray-900">{{ user.name }}</div>
              <div class="text-xs text-gray-500">{{ user.description }}</div>
            </div>
            <div class="flex gap-1">
              <span
                v-for="role in user.roles"
                :key="role"
                class="rounded-full px-2 py-0.5 text-xs font-medium"
                :class="roleColor(role)"
              >
                {{ role }}
              </span>
            </div>
          </button>

          <div class="relative">
            <div class="absolute inset-0 flex items-center">
              <div class="w-full border-t border-gray-200" />
            </div>
            <div class="relative flex justify-center text-sm">
              <span class="bg-white px-3 text-gray-400">{{ t('login.or') }}</span>
            </div>
          </div>
        </div>

        <div
          v-if="magicLinkSent"
          class="rounded-md bg-green-50 px-4 py-3 text-sm text-green-700"
          role="status"
        >
          {{ t('login.magicLinkSent') }}
        </div>

        <div
          v-if="auth.loginError"
          class="rounded-md bg-red-50 px-4 py-3 text-sm text-red-700"
          role="alert"
        >
          {{ auth.loginError }}
        </div>

        <form
          v-if="!magicLinkSent"
          class="space-y-5"
          @submit.prevent="handleSubmit"
        >
          <FormField :label="t('login.email')" for="email" required>
            <Input
              id="email"
              v-model="email"
              type="email"
              autocomplete="email"
              :placeholder="t('login.emailPlaceholder')"
              required
            />
          </FormField>

          <button
            type="submit"
            :disabled="submitting"
            class="w-full rounded-md bg-brand-600 px-4 py-2.5 text-sm font-medium text-white hover:bg-brand-700 focus:outline-none focus:ring-2 focus:ring-brand-500 focus:ring-offset-2 disabled:opacity-50"
          >
            {{ submitting ? t('login.sending') : t('login.sendLink') }}
          </button>
        </form>
      </div>

      <p class="mt-6 text-center text-sm text-gray-500">
        {{ t('login.noAccount') }}
        <router-link to="/join" class="font-medium text-brand-600 hover:text-brand-500">
          {{ t('login.joinLink') }}
        </router-link>
      </p>
    </div>
  </div>
</template>
