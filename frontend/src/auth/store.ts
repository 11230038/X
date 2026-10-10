import { reactive } from 'vue'

import {
  clearToken,
  fetchSession,
  getToken,
  login as loginRequest,
  register as registerRequest,
  setToken,
  setUnauthorizedHandler,
} from './client'
import type { User } from './types'

interface AuthState {
  user: User | null
  /** False until the stored token has been checked against the backend. */
  ready: boolean
}

export const authState = reactive<AuthState>({ user: null, ready: false })

setUnauthorizedHandler(() => {
  authState.user = null
})

function accept(user: User, token: string): User {
  setToken(token)
  authState.user = user
  authState.ready = true
  return user
}

export async function register(username: string, password: string): Promise<User> {
  const response = await registerRequest(username, password)
  return accept(response.user, response.token)
}

export async function login(username: string, password: string): Promise<User> {
  const response = await loginRequest(username, password)
  return accept(response.user, response.token)
}

export function logout(): void {
  // The backend has no revocation list, so signing out only discards the token
  // on this device; it stays valid until it expires.
  clearToken()
  authState.user = null
}

/**
 * ensureSession checks the stored token against the backend exactly once. A
 * missing or rejected token is not an error — it means the visitor is
 * anonymous, and the router sends them to the login page.
 */
export async function ensureSession(): Promise<void> {
  if (authState.ready) {
    return
  }
  authState.ready = true
  if (!getToken()) {
    authState.user = null
    return
  }
  try {
    const response = await fetchSession()
    authState.user = response.user
  } catch {
    authState.user = null
  }
}
