import { describe, it, expect, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import ErrorBoundary from '@/components/ui/ErrorBoundary.vue'

vi.mock('lucide-vue-next', () => ({}))

describe('ErrorBoundary', () => {
  it('renders slot content when no error', () => {
    const wrapper = mount(ErrorBoundary, {
      slots: { default: '<p>Hello</p>' },
    })
    expect(wrapper.text()).toContain('Hello')
  })

  it('shows error UI when error state is set', async () => {
    const wrapper = mount(ErrorBoundary, {
      slots: { default: '<p>Content</p>' },
    })

    const vm = wrapper.vm as unknown as { error: Error | null }
    vm.error = new Error('test crash')
    await nextTick()

    expect(wrapper.text()).toContain('error.title')
    expect(wrapper.text()).toContain('error.retry')
    expect(wrapper.text()).not.toContain('Content')
  })

  it('recovers when retry is clicked', async () => {
    const wrapper = mount(ErrorBoundary, {
      slots: { default: '<p>Content</p>' },
    })

    // Manually trigger error state via exposed
    const vm = wrapper.vm as unknown as { error: Error | null; retry: () => void }
    vm.error = new Error('simulated')
    await nextTick()
    expect(wrapper.text()).toContain('error.title')

    await wrapper.find('button').trigger('click')
    await nextTick()
    expect(wrapper.text()).toContain('Content')
  })

  it('has role="alert" on error state', async () => {
    const wrapper = mount(ErrorBoundary, {
      slots: { default: '<p>Content</p>' },
    })
    const vm = wrapper.vm as unknown as { error: Error | null }
    vm.error = new Error('test')
    await nextTick()
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
  })
})
