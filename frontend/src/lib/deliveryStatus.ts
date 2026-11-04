// Delivery statuses of a faktura email, shared by the delivery log and the
// faktura list badge. Journal-era statuses (no_record, pending,
// beyond_retention, unavailable) come from the mail server log; the rest are
// email_sends statuses (sender states and SES delivery events).
export type SendStatus =
  | 'accepted'
  | 'deferred'
  | 'rejected'
  | 'queued'
  | 'no_record'
  | 'pending'
  | 'beyond_retention'
  | 'unavailable'
  | 'sending'
  | 'submitted'
  | 'failed'
  | 'unknown'
  | 'rejected_temporary'
  | 'suppressed'
  | 'complaint'

const red = 'bg-red-50 text-red-800 ring-red-200'
const amber = 'bg-amber-50 text-amber-800 ring-amber-200'
const gray = 'bg-gray-50 text-gray-700 ring-gray-200'

export const statusClass: Record<SendStatus, string> = {
  accepted: 'bg-emerald-50 text-emerald-800 ring-emerald-200',
  deferred: amber,
  rejected: red,
  no_record: red,
  queued: gray,
  pending: gray,
  beyond_retention: gray,
  unavailable: gray,
  sending: gray,
  submitted: gray,
  failed: red,
  unknown: amber,
  rejected_temporary: red,
  suppressed: red,
  complaint: red,
}

// Statuses that carry an explanation in the delivery log.
export const statusWithHelp = new Set<SendStatus>([
  'deferred',
  'rejected',
  'no_record',
  'pending',
  'beyond_retention',
  'unavailable',
  'sending',
  'submitted',
  'failed',
  'unknown',
  'rejected_temporary',
  'suppressed',
  'complaint',
])

// Statuses worth a badge on the faktura list: the mail did not (or may not)
// reach the member and someone should look.
export const problemStatuses = new Set<SendStatus>([
  'deferred',
  'rejected',
  'failed',
  'unknown',
  'rejected_temporary',
  'suppressed',
  'complaint',
])
