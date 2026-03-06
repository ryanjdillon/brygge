import { vi } from 'vitest'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) => {
      if (params) {
        return Object.entries(params).reduce(
          (str, [k, v]) => str.replace(`{${k}}`, String(v)),
          key,
        )
      }
      return key
    },
    locale: { value: 'en' },
  }),
  createI18n: () => ({
    global: {
      t: (key: string) => key,
    },
    install: vi.fn(),
  }),
}))

vi.mock('@tanstack/vue-query', () => {
  const ref = (val: unknown) => ({ value: val })
  return {
    useQuery: vi.fn(() => ({
      data: ref(null),
      isLoading: ref(false),
      isError: ref(false),
      error: ref(null),
      refetch: vi.fn(),
    })),
    useMutation: vi.fn(() => ({
      mutate: vi.fn(),
      mutateAsync: vi.fn(),
      isPending: ref(false),
      isError: ref(false),
      error: ref(null),
    })),
    useQueryClient: vi.fn(() => ({
      invalidateQueries: vi.fn(),
    })),
    VueQueryPlugin: { install: vi.fn() },
    QueryClient: vi.fn(() => ({
      invalidateQueries: vi.fn(),
    })),
  }
})
