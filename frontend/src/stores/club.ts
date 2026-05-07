import { defineStore } from 'pinia'
import { ref } from 'vue'

interface ClubInfo {
  name: string
  slug: string
  domain: string
  address?: string
  phone?: string
  vhf_channel?: string
  latitude?: number | null
  longitude?: number | null
  website_url?: string
  chairman_email?: string
  vice_chairman_email?: string
  treasurer_email?: string
  secretary_email?: string
  harbor_master_email?: string
  has_logo?: boolean
  has_site_logo?: boolean
  has_faktura_logo?: boolean
  harbor_approach?: string
  harbor_depth?: string
  harbor_vhf?: string
  harbor_cta_title?: string
  harbor_cta_description?: string
  motorhome_power?: string
  motorhome_facilities?: string
  motorhome_checkin?: string
  motorhome_rules?: string
  motorhome_cta_title?: string
  motorhome_cta_description?: string
}

export const useClubStore = defineStore('club', () => {
  const name = ref<string>('')
  const slug = ref<string>('')
  const domain = ref<string>('')
  const address = ref<string>('')
  const phone = ref<string>('')
  const vhfChannel = ref<string>('')
  const latitude = ref<number | null>(null)
  const longitude = ref<number | null>(null)
  const websiteUrl = ref<string>('')
  const harborApproach = ref<string>('')
  const harborDepth = ref<string>('')
  const harborVhf = ref<string>('')
  const harborCtaTitle = ref<string>('')
  const harborCtaDescription = ref<string>('')
  const motorhomePower = ref<string>('')
  const motorhomeFacilities = ref<string>('')
  const motorhomeCheckin = ref<string>('')
  const motorhomeRules = ref<string>('')
  const motorhomeCtaTitle = ref<string>('')
  const motorhomeCtaDescription = ref<string>('')
  const chairmanEmail = ref<string>('')
  const viceChairmanEmail = ref<string>('')
  const treasurerEmail = ref<string>('')
  const secretaryEmail = ref<string>('')
  const harborMasterEmail = ref<string>('')
  const hasLogo = ref<boolean>(false)
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
        address.value = info.address || ''
        phone.value = info.phone || ''
        vhfChannel.value = info.vhf_channel || ''
        latitude.value = info.latitude ?? null
        longitude.value = info.longitude ?? null
        websiteUrl.value = info.website_url || ''
        harborApproach.value = info.harbor_approach || ''
        harborDepth.value = info.harbor_depth || ''
        harborVhf.value = info.harbor_vhf || ''
        harborCtaTitle.value = info.harbor_cta_title || ''
        harborCtaDescription.value = info.harbor_cta_description || ''
        motorhomePower.value = info.motorhome_power || ''
        motorhomeFacilities.value = info.motorhome_facilities || ''
        motorhomeCheckin.value = info.motorhome_checkin || ''
        motorhomeRules.value = info.motorhome_rules || ''
        motorhomeCtaTitle.value = info.motorhome_cta_title || ''
        motorhomeCtaDescription.value = info.motorhome_cta_description || ''
        chairmanEmail.value = info.chairman_email || ''
        viceChairmanEmail.value = info.vice_chairman_email || ''
        treasurerEmail.value = info.treasurer_email || ''
        secretaryEmail.value = info.secretary_email || ''
        harborMasterEmail.value = info.harbor_master_email || ''
        // The public /club JSON now exposes has_site_logo since the
        // navbar consumes the site (SVG) variant. has_logo is kept
        // around as a synonym for backwards-compat with any callsite
        // that hasn't migrated yet.
        hasLogo.value = info.has_site_logo === true || info.has_logo === true
        loaded.value = true
      } finally {
        inflight = null
      }
    })()
    return inflight
  }

  return {
    name,
    slug,
    domain,
    address,
    phone,
    vhfChannel,
    latitude,
    longitude,
    websiteUrl,
    chairmanEmail,
    viceChairmanEmail,
    treasurerEmail,
    secretaryEmail,
    harborMasterEmail,
    hasLogo,
    harborApproach,
    harborDepth,
    harborVhf,
    harborCtaTitle,
    harborCtaDescription,
    motorhomePower,
    motorhomeFacilities,
    motorhomeCheckin,
    motorhomeRules,
    motorhomeCtaTitle,
    motorhomeCtaDescription,
    loaded,
    ensureLoaded,
  }
})
