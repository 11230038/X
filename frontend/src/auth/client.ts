import { ApiError } from './types'
import type { ApiErrorBody, AuthResponse, SessionResponse, SuccessEnvelope } from './types'

// The token lives in localStorage, which any script on the page can read, so a
// successful XSS means a stolen session. The alternative — an httpOnly cookie —
// needs CSRF protection and server-side cookie handling that this slice does
// not have. See README for the trade-off.
const tokenKey = 'auth_token'

export function getToken(): string | null {
  return window.localStorage.getItem(tokenKey)
}

export function setToken(token: string): void {
  window.localStorage.setItem(tokenKey, token)
}

export function clearToken(): void {
  window.localStorage.removeItem(tokenKey)
}

let onUnauthorized: (() => void) | null = null

/** onUnauthorized is notified when a request that carried a token is rejected. */
export function setUnauthorizedHandler(handler: () => void): void {
  onUnauthorized = handler
}

function apiBaseUrl(): string {
  const base = import.meta.env.VITE_API_BASE_URL
  if (!base) {
    throw new Error('VITE_API_BASE_URL is not configured')
  }
  return base
}

interface RequestOptions {
  method: 'GET' | 'POST'
  body?: unknown
  /** Sends the stored bearer token. Defaults to false so login stays anonymous. */
  auth?: boolean
}

async function apiFetch<T>(path: string, options: RequestOptions): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  const token = options.auth ? getToken() : null
  if (token) {
    headers.Authorization = `Bearer ${token}`
  }
  if (options.body !== undefined) {
    headers['Content-Type'] = 'application/json'
  }

  let response: Response
  try {
    response = await fetch(`${apiBaseUrl()}${path}`, {
      method: options.method,
      headers,
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
    })
  } catch {
    throw new ApiError(0, 'network_error', '无法连接服务器', '')
  }

  const requestId = response.headers.get('X-Request-ID') ?? ''

  if (!response.ok) {
    const body = await readErrorBody(response)
    // Only an authenticated request proves the stored token is no longer good.
    // A 401 from login or register must reach the form instead of triggering a
    // redirect loop.
    if (response.status === 401 && token) {
      clearToken()
      onUnauthorized?.()
    }
    throw new ApiError(response.status, body.code, body.message, requestId)
  }

  // The backend wraps every success in {data, request_id}; callers only ever
  // see the payload.
  const envelope = (await response.json()) as SuccessEnvelope<T>
  return envelope.data
}

async function readErrorBody(response: Response): Promise<ApiErrorBody> {
  try {
    const parsed = (await response.json()) as { error?: ApiErrorBody }
    if (parsed.error?.code) {
      return { code: parsed.error.code, message: parsed.error.message ?? '请求失败' }
    }
  } catch {
    // Fall through to the generic error below; the status is already known.
  }
  return { code: 'unknown_error', message: '请求失败' }
}

export function register(username: string, password: string): Promise<AuthResponse> {
  return apiFetch<AuthResponse>('/auth/register', {
    method: 'POST',
    body: { username, password },
  })
}

export function login(username: string, password: string): Promise<AuthResponse> {
  return apiFetch<AuthResponse>('/auth/login', {
    method: 'POST',
    body: { username, password },
  })
}

export function fetchSession(): Promise<SessionResponse> {
  return apiFetch<SessionResponse>('/auth/me', { method: 'GET', auth: true })
}
