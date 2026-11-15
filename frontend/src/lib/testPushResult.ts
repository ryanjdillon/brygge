import { ApiError } from '@/lib/errors'

/** The counts `POST /api/v1/notifications/test` answers with. */
export interface TestPushCounts {
  sent: number
  removed: number
  failed: number
  total: number
}

export interface TestPushMessage {
  kind: 'success' | 'warning' | 'error'
  lines: string[]
}

type Translate = (key: string, ...args: unknown[]) => string

/**
 * What to show after "Send testvarsel" (BRY-682), shared by the member's
 * notifications page and the admin notifications page. Nothing sent is an
 * error even on a 200; a removed or failed device is a warning.
 */
export function testPushOutcome(t: Translate, res: TestPushCounts): TestPushMessage {
  const lines = [t('notifications.test.sent', { sent: res.sent, total: res.total }, res.total)]
  if (res.removed > 0) lines.push(t('notifications.test.removed', { count: res.removed }))
  if (res.failed > 0) lines.push(t('notifications.test.failed', { count: res.failed }))
  if (res.sent > 0) lines.push(t('notifications.test.hint'))
  const kind = res.sent === 0 ? 'error' : res.failed > 0 || res.removed > 0 ? 'warning' : 'success'
  return { kind, lines }
}

/** The message for a refused test push, from the error code the API sends. */
export function testPushFailure(t: Translate, err: unknown): TestPushMessage {
  const api = err instanceof ApiError ? err : undefined
  let key = 'notifications.test.error'
  if (api?.code === 'no_push_subscription') key = 'notifications.test.noSubscription'
  else if (api?.code === 'push_not_configured') key = 'notifications.test.notConfigured'
  else if (api?.status === 429) key = 'notifications.test.rateLimited'
  return { kind: 'error', lines: [t(key)] }
}
