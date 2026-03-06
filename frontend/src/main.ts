import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { VueQueryPlugin } from '@tanstack/vue-query'
import { createI18n } from 'vue-i18n'
import App from '@/App.vue'
import { router } from '@/router'
import nb from '@/locales/nb.json'
import en from '@/locales/en.json'
import '@/app.css'

const i18n = createI18n({
  legacy: false,
  locale: 'nb',
  fallbackLocale: 'en',
  messages: { nb, en },
})

const app = createApp(App)

app.use(createPinia())
app.use(router)
app.use(VueQueryPlugin)
app.use(i18n)

app.mount('#app')
