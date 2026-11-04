import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import FilterSelect from '@/components/admin/FilterSelect.vue'
import HarborShareFilter from '@/components/admin/HarborShareFilter.vue'
import SpotFilter from '@/components/admin/SpotFilter.vue'

// The i18n mock in src/test/setup.ts returns keys, so labels show as keys.
describe('FilterSelect', () => {
  const options = [
    { value: '', labelKey: 'x.all' },
    { value: 'a', labelKey: 'x.a' },
  ]

  it('prefixes each option with the filter label and labels the select', () => {
    const w = mount(FilterSelect, { props: { modelValue: 'a', options, labelKey: 'x.label', id: 'x-filter' } })
    const select = w.find('select')
    expect(select.attributes('id')).toBe('x-filter')
    expect(w.find('label').attributes('for')).toBe('x-filter')
    expect(w.findAll('option').map((o) => o.text())).toEqual(['x.label: x.all', 'x.label: x.a'])
    expect((select.element as HTMLSelectElement).value).toBe('a')
  })

  it('emits the picked value', async () => {
    const w = mount(FilterSelect, { props: { modelValue: '', options, labelKey: 'x.label', id: 'x-filter' } })
    await w.find('select').setValue('a')
    expect(w.emitted('update:modelValue')).toEqual([['a']])
  })
})

describe('filters built on FilterSelect', () => {
  it('HarborShareFilter offers all, owners and not owners', async () => {
    const w = mount(HarborShareFilter, { props: { modelValue: '' } })
    expect(w.findAll('option').map((o) => o.attributes('value'))).toEqual(['', 'owner', 'not_owner'])
    expect(w.find('select').attributes('id')).toBe('harbor-share-filter')
    await w.find('select').setValue('not_owner')
    expect(w.emitted('update:modelValue')).toEqual([['not_owner']])
  })

  it('SpotFilter keeps its options and default id', () => {
    const w = mount(SpotFilter, { props: { modelValue: 'none' } })
    expect(w.findAll('option').map((o) => o.attributes('value'))).toEqual(['', 'permanent', 'seasonal', 'none'])
    expect(w.find('select').attributes('id')).toBe('spot-filter')
  })
})
