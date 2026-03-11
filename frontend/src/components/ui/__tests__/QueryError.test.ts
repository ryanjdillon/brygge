import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import QueryError from '@/components/ui/QueryError.vue'
import { ApiError } from '@/lib/errors'

describe('QueryError', () => {
  it('renders nothing when error is null', () => {
    const wrapper = mount(QueryError, { props: { error: null } })
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
  })

  it('renders error message for generic Error', () => {
    const wrapper = mount(QueryError, {
      props: { error: new Error('Something failed') },
    })
    expect(wrapper.text()).toContain('Something failed')
  })

  it('renders translated message for ApiError with code', () => {
    const err = new ApiError(403, 'Forbidden', 'FORBIDDEN')
    const wrapper = mount(QueryError, { props: { error: err } })
    expect(wrapper.text()).toContain('error.forbidden')
  })

  it('falls back to message for ApiError without code', () => {
    const err = new ApiError(500, 'Server error')
    const wrapper = mount(QueryError, { props: { error: err } })
    expect(wrapper.text()).toContain('Server error')
  })

  it('falls back to message for unknown code', () => {
    const err = new ApiError(422, 'Bad data', 'UNKNOWN_CODE')
    const wrapper = mount(QueryError, { props: { error: err } })
    expect(wrapper.text()).toContain('Bad data')
  })

  it('has role="alert"', () => {
    const wrapper = mount(QueryError, {
      props: { error: new Error('test') },
    })
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
  })
})
