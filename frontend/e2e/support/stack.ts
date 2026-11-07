// Helpers for E2E specs that need the local dev stack's database, mail
// catcher and rate limiter (BRY-661). They talk to the docker-compose
// containers `just up` starts, so they need no npm dependency:
//
//   E2E_DB_CONTAINER  postgres container (default deploy-db-1)
//   E2E_DB_NAME       database the API under test uses (default brygge)
//   E2E_DB_USER       role to connect as (default brygge)
//   E2E_CLUB_SLUG     the API's CLUB_SLUG (default brygge)
//   MAILPIT_URL       Mailpit web/API base (default http://localhost:8025)
//
// They write to the database, so they refuse to run against anything but a
// local stack (assertLocalStack). Only synthetic data: users get a random
// address under example.test.

import { execFileSync } from 'node:child_process'
import { createHash, randomBytes } from 'node:crypto'
import type { BrowserContext } from '@playwright/test'

const DB_CONTAINER = process.env.E2E_DB_CONTAINER || 'deploy-db-1'
const DB_NAME = process.env.E2E_DB_NAME || 'brygge'
const DB_USER = process.env.E2E_DB_USER || 'brygge'
const CLUB_SLUG = process.env.E2E_CLUB_SLUG || 'brygge'
// Same default as playwright.config.ts.
export const BASE_URL = (process.env.BASE_URL || 'http://localhost:5173').replace(/\/$/, '')
export const MAILPIT_URL = (process.env.MAILPIT_URL || 'http://localhost:8025').replace(/\/$/, '')

const LOCAL_HOSTS = new Set(['localhost', '127.0.0.1', '[::1]'])

let localChecked = false

/**
 * Throws unless the helpers would reach a local stack: docker talks to the
 * local daemon (DOCKER_HOST unset or unix://, context `default`) and the
 * app and Mailpit URLs are on this machine. The helpers insert users and
 * login links, so a remote docker context or a BASE_URL pointing at a real
 * club must stop them before the first write.
 */
export function assertLocalStack(): void {
  if (localChecked) return
  const dockerHost = process.env.DOCKER_HOST
  if (dockerHost && !dockerHost.startsWith('unix://')) {
    throw new Error(`E2E stack helpers refuse to run: DOCKER_HOST=${dockerHost} is not a local unix socket`)
  }
  const context = execFileSync('docker', ['context', 'show'], { encoding: 'utf8' }).trim()
  if (context !== 'default') {
    throw new Error(`E2E stack helpers refuse to run: docker context is "${context}", not "default"`)
  }
  for (const [name, url] of [
    ['BASE_URL', BASE_URL],
    ['MAILPIT_URL', MAILPIT_URL],
  ]) {
    const host = new URL(url).hostname
    if (!LOCAL_HOSTS.has(host)) {
      throw new Error(`E2E stack helpers refuse to run: ${name} host "${host}" is not localhost`)
    }
  }
  localChecked = true
}

/** A SQL string literal. Inputs are test-generated, but quote them anyway. */
function lit(value: string): string {
  return `'${value.replace(/'/g, "''")}'`
}

/** Runs SQL through psql in the db container and returns its unaligned output. */
export function psql(sql: string): string {
  assertLocalStack()
  return execFileSync(
    'docker',
    ['exec', '-i', DB_CONTAINER, 'psql', '-U', DB_USER, '-d', DB_NAME, '-v', 'ON_ERROR_STOP=1', '-Atq', '-f', '-'],
    { input: sql, encoding: 'utf8' },
  ).trim()
}

export interface TestUser {
  id: string
  email: string
}

/** Creates a person user with the given roles in the API's club. */
export function createUser(roles: string[], label = 'login'): TestUser {
  const email = `e2e-${label}-${randomBytes(6).toString('hex')}@example.test`
  const roleArray = `ARRAY[${roles.map(lit).join(', ')}]::user_role[]`
  const id = psql(`
    WITH u AS (
      INSERT INTO users (club_id, email, first_name, last_name)
      SELECT id, ${lit(email)}, 'E2E', 'Testbruker' FROM clubs WHERE slug = ${lit(CLUB_SLUG)}
      RETURNING id, club_id
    ), r AS (
      INSERT INTO user_roles (user_id, club_id, role)
      SELECT u.id, u.club_id, role FROM u, unnest(${roleArray}) AS role
    )
    SELECT id FROM u;
  `)
  if (!/^[0-9a-f-]{36}$/.test(id)) {
    throw new Error(`createUser: no club with slug ${CLUB_SLUG} in ${DB_NAME}? got ${JSON.stringify(id)}`)
  }
  return { id, email }
}

/**
 * Deactivates the users and drops their login links and join requests. They
 * are not deleted: audit_log rows (auth.login_success etc.) reference them.
 */
export function retireUsers(users: TestUser[]): void {
  if (users.length === 0) return
  const emails = users.map((u) => lit(u.email)).join(', ')
  psql(`
    DELETE FROM magic_links WHERE email IN (${emails});
    DELETE FROM membership_applications WHERE email IN (${emails});
    UPDATE users SET deactivated_at = now() WHERE email IN (${emails}) AND deactivated_at IS NULL;
  `)
}

/**
 * A synthetic address under example.test for a join request, which needs no
 * user row. Pass it to retireUsers (with an empty id) to drop the request.
 */
export function joinAddress(label = 'join', localPartLength = 0): TestUser {
  const pad = localPartLength > 0 ? `-${'x'.repeat(localPartLength)}` : ''
  return { id: '', email: `e2e-${label}${pad}-${randomBytes(6).toString('hex')}@example.test` }
}

/**
 * Stores an unconfirmed join request for email the way POST /join does (only
 * the token's sha256 hex, valid 24 hours) and returns the raw token for
 * /join/confirm?token=….
 */
export function insertJoinApplication(email: string, firstName = 'E2E'): string {
  const token = randomBytes(32).toString('base64url')
  const out = psql(`
    INSERT INTO membership_applications (club_id, first_name, last_name, email, token_hash, consent_versions, expires_at)
    SELECT id, ${lit(firstName)}, 'Testsøker', ${lit(email)}, ${lit(tokenHash(token))}, '{}', now() + interval '24 hours'
    FROM clubs WHERE slug = ${lit(CLUB_SLUG)}
    RETURNING 1;
  `)
  if (out !== '1') {
    throw new Error(`insertJoinApplication: no club with slug ${CLUB_SLUG} in ${DB_NAME}`)
  }
  return token
}

/**
 * Retires E2E users left active by an interrupted run. Only users older
 * than an hour, so a parallel worker's fresh users are untouched.
 */
export function sweepStaleUsers(): void {
  psql(`
    DELETE FROM magic_links WHERE email LIKE 'e2e-%@example.test' AND created_at < now() - interval '1 hour';
    DELETE FROM membership_applications WHERE email LIKE 'e2e-%@example.test' AND created_at < now() - interval '1 hour';
    UPDATE users SET deactivated_at = now()
     WHERE email LIKE 'e2e-%@example.test' AND deactivated_at IS NULL AND created_at < now() - interval '1 hour';
  `)
}

function tokenHash(token: string): string {
  return createHash('sha256').update(token, 'utf8').digest('hex')
}

/**
 * Stores a login link for email the way issueMagicLink does (only the
 * token's sha256 hex) and returns the raw token. expiresInMinutes < 0 gives
 * an already expired link.
 */
export function insertLoginToken(email: string, expiresInMinutes = 15): string {
  const token = randomBytes(32).toString('base64url')
  const out = psql(`
    INSERT INTO magic_links (token_hash, email, club_id, expires_at)
    SELECT ${lit(tokenHash(token))}, ${lit(email)}, id, now() + make_interval(mins => ${Math.trunc(expiresInMinutes)})
    FROM clubs WHERE slug = ${lit(CLUB_SLUG)}
    RETURNING 1;
  `)
  if (out !== '1') {
    throw new Error(`insertLoginToken: no club with slug ${CLUB_SLUG} in ${DB_NAME}`)
  }
  return token
}

/** The used flag of the login link with this raw token, or null if none. */
export function loginTokenUsed(token: string): boolean | null {
  const out = psql(`SELECT used FROM magic_links WHERE token_hash = ${lit(tokenHash(token))};`)
  return out === '' ? null : out === 't'
}

export interface AuthAuditRow {
  action: string
  method: string
  reason: string
  // 'peek' when the login page's peek refused the token (BRY-660), else ''.
  stage: string
  ip: string
}

/** The auth.* audit rows recorded for the user, oldest first. */
export function authAudit(userId: string): AuthAuditRow[] {
  const out = psql(`
    SELECT action, coalesce(details->>'method', ''), coalesce(details->>'reason', ''),
           coalesce(details->>'stage', ''), actor_ip
    FROM audit_log WHERE actor_id = ${lit(userId)} AND action LIKE 'auth.%' ORDER BY created_at;
  `)
  if (out === '') return []
  return out.split('\n').map((line) => {
    const [action, method, reason, stage, ip] = line.split('|')
    return { action, method, reason, stage, ip }
  })
}

/** How many login links are stored for email. */
export function loginLinkCount(email: string): number {
  return Number(psql(`SELECT count(*) FROM magic_links WHERE email = ${lit(email)};`))
}

// Client IPs for the rate limiter, from 198.18.0.0/15 (benchmarking, never a
// real client): unique within a run per worker and test, offset by a per-run
// salt that playwright.config.ts sets once for all workers, so a rerun inside
// the same minute does not reuse a spent budget.
const IP_SPACE = 1 << 17
const RUN_SALT = Number(process.env.E2E_RUN_SALT ?? 0) % IP_SPACE
let ipCounter = 0

export function nextClientIp(workerIndex: number): string {
  const n = (RUN_SALT + workerIndex * 1024 + ipCounter++) % IP_SPACE
  return `198.${18 + (n >> 16)}.${(n >> 8) & 0xff}.${n & 0xff}`
}

/**
 * Gives the context its own client IP for the API's per-IP rate limiters
 * and returns it. Locally the API's TCP peer is the Vite dev proxy, which
 * the API treats as a trusted proxy (TRUSTED_PROXY_CIDRS: loopback for a
 * native `go run`, plus the docker range the compose dev overlay adds), so
 * it takes the client IP from X-Forwarded-For. Each test then has its own
 * strict 5/min auth budget instead of all workers sharing the proxy's.
 */
export async function isolateRateLimit(context: BrowserContext, workerIndex: number): Promise<string> {
  const ip = nextClientIp(workerIndex)
  await context.setExtraHTTPHeaders({ 'X-Forwarded-For': ip })
  return ip
}

/**
 * Fails fast when the API ignores X-Forwarded-For (the proxy is not in
 * TRUSTED_PROXY_CIDRS), since every test would then share one budget and
 * 429 at random. It spends one IP's strict budget (6 requests) and checks a
 * second IP still gets through. The requests carry an empty token, which the
 * API refuses before touching the database or the audit log.
 */
export async function assertRateLimitIsolation(workerIndex: number): Promise<void> {
  assertLocalStack()
  const hit = (ip: string) =>
    fetch(`${BASE_URL}/api/v1/auth/verify`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Forwarded-For': ip },
      body: JSON.stringify({ token: '' }),
    }).then((res) => res.status)

  const first = nextClientIp(workerIndex)
  for (let i = 0; i < 6; i++) await hit(first)
  const second = await hit(nextClientIp(workerIndex))
  if (second === 429) {
    throw new Error(
      'The API ignores X-Forwarded-For from the dev proxy, so all tests share one 5/min auth budget. ' +
        'Add the proxy to TRUSTED_PROXY_CIDRS (the compose dev overlay sets 127.0.0.0/8,::1/128,172.16.0.0/12).',
    )
  }
  if (second !== 410) {
    throw new Error(`Rate-limit probe: expected 410 from POST /api/v1/auth/verify, got ${second}. Is the API up?`)
  }
}

/** True when Mailpit's API answers. */
export async function mailpitAvailable(): Promise<boolean> {
  assertLocalStack()
  try {
    const res = await fetch(`${MAILPIT_URL}/api/v1/messages?limit=1`)
    return res.ok
  } catch {
    return false
  }
}

/** Polls Mailpit for the login link mailed to email and returns its token. */
export async function loginTokenFromMail(email: string, timeoutMs = 10_000): Promise<string> {
  assertLocalStack()
  const deadline = Date.now() + timeoutMs
  const query = encodeURIComponent(`to:"${email}"`)
  while (Date.now() < deadline) {
    const res = await fetch(`${MAILPIT_URL}/api/v1/search?query=${query}&limit=1`)
    if (res.ok) {
      const list = (await res.json()) as { messages?: { ID: string }[] }
      const id = list.messages?.[0]?.ID
      if (id) {
        const msg = (await (await fetch(`${MAILPIT_URL}/api/v1/message/${id}`)).json()) as {
          HTML?: string
          Text?: string
        }
        const match = `${msg.HTML ?? ''}\n${msg.Text ?? ''}`.match(/\/login\/verify\?token=([A-Za-z0-9_-]+)/)
        if (match) return match[1]
        throw new Error(`login mail to ${email} has no /login/verify?token= link`)
      }
    }
    await new Promise((resolve) => setTimeout(resolve, 250))
  }
  throw new Error(`no login mail to ${email} in Mailpit within ${timeoutMs} ms`)
}
