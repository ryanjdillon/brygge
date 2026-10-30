import { describe, it, expect } from 'vitest'
import { userLifecycleErrorKey } from '../userLifecycleErrors'
import nb from '@/locales/nb.json'
import en from '@/locales/en.json'

const locales: Record<string, any> = { nb, en }

describe('userLifecycleErrorKey', () => {
  it('maps the typed refusals and falls back to a generic message', () => {
    expect(userLifecycleErrorKey('last_admin')).toBe('admin.users.lastAdminError')
    expect(userLifecycleErrorKey('admin_required')).toBe('admin.users.adminRequiredError')
    expect(userLifecycleErrorKey('something_else')).toBe('admin.users.actionError')
    expect(userLifecycleErrorKey(undefined)).toBe('admin.users.actionError')
  })

  it('has a translation for every key in the shipped locales', () => {
    for (const [name, messages] of Object.entries(locales)) {
      for (const code of ['last_admin', 'admin_required', 'x']) {
        const key = userLifecycleErrorKey(code).split('.')
        const text = key.reduce((node: any, part) => node?.[part], messages)
        expect(typeof text, `${name}:${key.join('.')}`).toBe('string')
      }
    }
  })
})
