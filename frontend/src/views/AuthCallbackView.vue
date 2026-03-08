<script setup lang="ts">
import { onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { Loader2 } from 'lucide-vue-next'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

onMounted(async () => {
  const code = route.query.code as string

  if (code) {
    // Clear code from URL immediately
    window.history.replaceState({}, '', '/auth/callback')

    try {
      const res = await fetch('/api/v1/auth/exchange', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ code }),
      })

      if (!res.ok) {
        router.replace('/login')
        return
      }

      const data = await res.json()
      await auth.setTokens(data.access_token, data.refresh_token)
      const redirect = auth.hasRole('admin') || auth.hasRole('styre') ? '/admin' : '/portal'
      router.replace(redirect)
    } catch {
      router.replace('/login')
    }
  } else {
    router.replace('/login')
  }
})
</script>

<template>
  <div class="flex min-h-[calc(100vh-8rem)] items-center justify-center">
    <Loader2 class="h-8 w-8 animate-spin text-blue-600" />
  </div>
</template>
