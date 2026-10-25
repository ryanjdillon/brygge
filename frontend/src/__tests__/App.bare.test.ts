import { describe, it, expect, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createTestingPinia } from '@pinia/testing'
import { createRouter, createMemoryHistory } from 'vue-router'
import App from '@/App.vue'

const stubs = {
  NavBar: { template: '<nav data-testid="navbar" />' },
  Footer: { template: '<footer data-testid="footer" />' },
  ToastContainer: { template: '<div data-testid="toasts" />' },
  ConfirmDialog: true,
  NetworkStatus: true,
  FreshTotpModal: true,
  TotpExpiringModal: true,
}

async function mountAt(path: string) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: { template: '<p>home</p>' } },
      { path: '/sheet', component: { template: '<p>sheet</p>' }, meta: { bare: true } },
    ],
  })
  router.push(path)
  await router.isReady()
  const wrapper = mount(App, {
    global: { plugins: [createTestingPinia({ createSpy: vi.fn }), router], stubs },
  })
  await flushPromises()
  return wrapper
}

describe('App bare routes', () => {
  it('renders the navbar and footer on normal routes', async () => {
    const wrapper = await mountAt('/')
    expect(wrapper.find('[data-testid="navbar"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="footer"]').exists()).toBe(true)
  })

  it('drops the navbar and footer on a bare route but keeps global overlays', async () => {
    const wrapper = await mountAt('/sheet')
    expect(wrapper.text()).toContain('sheet')
    expect(wrapper.find('[data-testid="navbar"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="footer"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="toasts"]').exists()).toBe(true)
  })
})
