import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { VueQueryPlugin } from '@tanstack/vue-query'
import App from '@/App.vue'
import { router } from '@/router'
import { i18n } from '@/i18n'
import '@/app.css'

const app = createApp(App)

app.use(createPinia())
app.use(router)
app.use(VueQueryPlugin)
app.use(i18n)

import { vBackdropClose } from '@/directives/backdropClose'
app.directive('backdrop-close', vBackdropClose)

app.config.errorHandler = (err, _instance, info) => {
  console.error('Global Vue error:', err, info)
}

app.mount('#app')

if ('serviceWorker' in navigator) {
  navigator.serviceWorker.register('/sw.js').catch(() => {
    // Service worker registration failed - push notifications won't work
  })
}
