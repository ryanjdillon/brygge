import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { VueQueryPlugin } from '@tanstack/vue-query'
import { createI18n } from 'vue-i18n'
import App from '@/App.vue'
import { router } from '@/router'
import nb from '@/locales/nb.json'
import en from '@/locales/en.json'
import de from '@/locales/de.json'
import fr from '@/locales/fr.json'
import nl from '@/locales/nl.json'
import it from '@/locales/it.json'
import pl from '@/locales/pl.json'
import '@/app.css'

const savedLocale = localStorage.getItem('brygge-locale')
const supportedLocales = ['nb', 'en', 'de', 'fr', 'nl', 'it', 'pl']
const locale = savedLocale && supportedLocales.includes(savedLocale) ? savedLocale : 'nb'

const i18n = createI18n({
  legacy: false,
  locale,
  fallbackLocale: 'en',
  messages: { nb, en, de, fr, nl, it, pl },
})

const app = createApp(App)

app.use(createPinia())
app.use(router)
app.use(VueQueryPlugin)
app.use(i18n)

app.mount('#app')

if ('serviceWorker' in navigator) {
  navigator.serviceWorker.register('/sw.js').catch(() => {
    // Service worker registration failed - push notifications won't work
  })
}
