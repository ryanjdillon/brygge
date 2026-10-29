import { describe, it, expect, vi, beforeEach } from 'vitest'
import { defineComponent } from 'vue'
import { mount } from '@vue/test-utils'
import { LOCALE_OPTIONS, i18n, setLocale } from '@/i18n'
import LanguageSwitcher from '@/components/layout/LanguageSwitcher.vue'

// The global test setup stubs vue-i18n; this spec drives the real plugin
// to prove a locale switch re-renders translated text.
vi.unmock('vue-i18n')

vi.mock('lucide-vue-next', () => ({ ChevronDown: { template: '<span />' } }))

const Host = defineComponent({
  components: { LanguageSwitcher },
  template: `<div><LanguageSwitcher /><p data-test="probe">{{ $t("common.save") }}|{{ $t("no.such.key") }}</p></div>`,
})

describe('LanguageSwitcher with the real i18n plugin', () => {
  beforeEach(() => {
    localStorage.clear()
    setLocale('nb')
  })

  it('switches through every locale and re-renders translated text', async () => {
    const wrapper = mount(Host, { global: { plugins: [i18n] } })
    const saveByLocale = new Map<string, string>()

    for (const { code, label } of LOCALE_OPTIONS) {
      await wrapper.find('button').trigger('click')
      // Skip the trigger: it carries the active locale's label too.
      const option = wrapper.findAll('button').slice(1).find((b) => b.text() === label)
      expect(option, label).toBeDefined()
      await option!.trigger('click')

      expect(i18n.global.locale.value).toBe(code)
      expect(localStorage.getItem('brygge-locale')).toBe(code)
      expect(wrapper.find('button').text()).toBe(label)
      saveByLocale.set(code, wrapper.get('[data-test="probe"]').text().split('|')[0]!)
    }

    expect(saveByLocale.get('nb')).toBe('Lagre')
    expect(saveByLocale.get('en')).toBe('Save')
    expect(saveByLocale.get('pl')).toBe('Zapisz')
    expect(saveByLocale.get('de')).toBe('Speichern')
  })

  it('renders the key itself when a message is missing in every locale', () => {
    const wrapper = mount(Host, { global: { plugins: [i18n] } })
    expect(wrapper.get('[data-test="probe"]').text()).toContain('|no.such.key')
  })

  it('falls back to English for a key missing from the active locale', () => {
    i18n.global.mergeLocaleMessage('en', { only: { in: { english: 'English text' } } })
    setLocale('pl')
    expect(i18n.global.t('only.in.english')).toBe('English text')
  })
})
