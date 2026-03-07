<script setup lang="ts">
import { onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { Loader2 } from 'lucide-vue-next'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

onMounted(async () => {
  const at = route.query.access_token as string
  const rt = route.query.refresh_token as string

  if (at && rt) {
    await auth.setTokens(at, rt)
    // Clear tokens from URL
    window.history.replaceState({}, '', '/auth/callback')
    const redirect = auth.hasRole('admin') || auth.hasRole('styre') ? '/admin' : '/portal'
    router.replace(redirect)
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
