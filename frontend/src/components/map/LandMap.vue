<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import maplibregl from 'maplibre-gl'
import 'maplibre-gl/dist/maplibre-gl.css'
import { ExternalLink } from 'lucide-vue-next'

const props = defineProps<{
  lat: number
  lng: number
  clubName?: string
}>()

const { t } = useI18n()
const container = ref<HTMLDivElement>()
let map: maplibregl.Map | null = null

const googleMapsUrl = `https://www.google.com/maps/dir/?api=1&destination=${props.lat},${props.lng}`

onMounted(() => {
  if (!container.value) return

  map = new maplibregl.Map({
    container: container.value,
    style: {
      version: 8,
      sources: {
        osm: {
          type: 'raster',
          tiles: ['https://tile.openstreetmap.org/{z}/{x}/{y}.png'],
          tileSize: 256,
          attribution:
            '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> | <a href="https://maplibre.org/">MapLibre</a>',
        },
      },
      layers: [
        {
          id: 'osm',
          type: 'raster',
          source: 'osm',
        },
      ],
    },
    center: [props.lng, props.lat],
    zoom: 14,
  })

  map.addControl(new maplibregl.NavigationControl(), 'top-right')

  map.on('load', () => {
    if (!map) return
    new maplibregl.Marker({ color: '#1e3a5f' })
      .setLngLat([props.lng, props.lat])
      .setPopup(
        new maplibregl.Popup({ offset: 25 }).setHTML(
          `<strong>${props.clubName || 'Klubb'}</strong>`
        )
      )
      .addTo(map)
  })
})

onUnmounted(() => {
  map?.remove()
  map = null
})
</script>

<template>
  <div class="relative h-full">
    <div ref="container" class="h-full w-full rounded-lg" style="min-height: 256px" />
    <a
      :href="googleMapsUrl"
      target="_blank"
      rel="noopener noreferrer"
      class="absolute bottom-3 left-3 z-10 flex items-center gap-2 rounded-lg bg-white px-4 py-2.5 text-sm font-medium text-gray-900 shadow-lg transition hover:bg-gray-50"
    >
      <ExternalLink class="h-4 w-4" aria-hidden="true" />
      {{ t('directions.openGoogleMaps') }}
    </a>
  </div>
</template>
