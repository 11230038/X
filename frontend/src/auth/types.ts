export interface User {
  user_id: number
  username: string
  role: string
}

/** SuccessEnvelope mirrors the backend success shape: `{data, request_id}`. */
export interface SuccessEnvelope<T> {
  data: T
  request_id?: string
}

export interface AuthResponse {
  user: User
  token: string
  token_type: string
  expires_at: string
}

export interface SessionResponse {
  user: User
}

export interface ApiErrorBody {
  code: string
  message: string
}

/** ApiError carries the backend error envelope so forms can render a message. */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly requestId: string

  constructor(status: number, code: string, message: string, requestId: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.requestId = requestId
  }
}
