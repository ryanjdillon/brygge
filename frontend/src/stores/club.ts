import { defineStore } from 'pinia'
import { ref } from 'vue'

interface ClubInfo {
  name: string
  slug: string
  domain: string
}

export const useClubStore = defineStore('club', () => {
  const name = ref<string>('')
  const slug = ref<string>('')
  const domain = ref<string>('')
  const loaded = ref(false)
  let inflight: Promise<void> | null = null

  async function ensureLoaded() {
    if (loaded.value) return
    if (inflight) return inflight
    inflight = (async () => {
      try {
        const res = await fetch('/api/v1/club', { credentials: 'include' })
        if (!res.ok) return
        const info = (await res.json()) as ClubInfo
        name.value = info.name || ''
        slug.value = info.slug || ''
        domain.value = info.domain || ''
        loaded.value = true
      } finally {
        inflight = null
      }
    })()
    return inflight
  }

  return { name, slug, domain, loaded, ensureLoaded }
})
