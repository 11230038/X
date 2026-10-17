/**
 * 请求失败的分类与构造。
 *
 * 401、429、额度用尽、断网与读不懂的响应各有各的出口；JSON、XHR 上传与裸
 * 文件下载三条路径共用这里的判定，避免在同一个状态码上分叉。
 */

/**
 * 一个请求失败的分类。界面按 `kind` 分支，而不是按 HTTP 状态码 —— 状态码
 * 是后端的实现细节，`kind` 才是"我该做什么"。
 */
export type FailureKind =
  /** 凭据没了或被撤回。→ 拆 socket、回登录页（§1.3）。 */
  | 'unauthorized'
  /** 服务端让等几秒。→ 倒计时，不是"网络不通"（§4.2）。 */
  | 'rate_limited'
  /**
   * 本期额度用完了。它也是 429，但与限流不是一件事：没有 `Retry-After`，
   * 要等的是额度周期重置（`details.resets_at`），重试不会更快恢复。
   */
  | 'quota'
  /** 后端用 `error.code` 说的一件具体的事。→ 按 code 映射文案。 */
  | 'api'
  /** 根本没到服务端：断网、代理挂了、DNS 失败。 */
  | 'network'
  /** 到了服务端但回的是一堆读不懂的东西。这是 bug，不是用户的问题。 */
  | 'malformed'

export type ApiFailure = {
  kind: FailureKind
  /** HTTP 状态码；`network` 时为 0。 */
  status: number
  /** 后端的稳定契约。`network` / `malformed` 时为空串。 */
  code: string
  /** 面向人的一句话。后端的 message 优先，没有就用本地兜底。 */
  message: string
  /**
   * 后端 `error.details` 原样带回。403 的 `{surface,layer,reason}` 靠它画出
   * "是哪一层拒的" —— 没有它，界面只能写"没有权限"。
   */
  details?: unknown
  /** 只在 `rate_limited` 时有值：服务端让等多少秒（`Retry-After`）。 */
  retryAfterSeconds?: number
  requestId?: string
}

/** 请求失败时抛的就是它。`instanceof` 不好用，请用 `isApiFailure` 判。 */
export class ApiError extends Error {
  readonly failure: ApiFailure

  constructor(failure: ApiFailure) {
    super(failure.message)
    this.name = 'ApiError'
    this.failure = failure
  }
}

export function isApiFailure(value: unknown): value is ApiError {
  return value instanceof ApiError
}

/** 后端的统一信封。成功与失败恰好出现一个。 */
export interface Envelope<T> {
  data?: T
  error?: { code: string; message: string; details?: unknown }
  request_id?: string
}

export function networkFailure(cause: unknown): ApiFailure {
  return {
    kind: 'network',
    status: 0,
    code: cause instanceof DOMException && cause.name === 'AbortError' ? 'aborted' : '',
    message: '网络不可达，请检查连接后重试',
  }
}

export function rateLimitedFailure(
  retryAfterHeader: string | null,
  envelope: Envelope<unknown> | null = null,
): ApiFailure {
  const retryAfter = Number.parseInt(retryAfterHeader ?? '', 10)
  return {
    kind: 'rate_limited',
    status: 429,
    code: envelope?.error?.code ?? 'rate_limited',
    message: envelope?.error?.message ?? '请求太频繁，请稍候再试',
    ...(envelope?.request_id ? { requestId: envelope.request_id } : {}),
    ...(envelope?.error?.details !== undefined ? { details: envelope.error.details } : {}),
    ...(Number.isFinite(retryAfter) && retryAfter > 0
      ? { retryAfterSeconds: retryAfter }
      : {}),
  }
}

/**
 * 429 有两种意思，靠信封里的码分开：`quota_exceeded` 是额度周期用完了（没有
 * `Retry-After`，重试无用），其余是普通限流（可能带 `Retry-After`）。HTTP 层
 * 只看状态码会把前者画成"等几秒就好"。
 */
export function isQuotaEnvelope(
  envelope: Envelope<unknown> | null,
): envelope is Envelope<unknown> {
  return envelope?.error?.code === 'quota_exceeded'
}

export function quotaFailure(envelope: Envelope<unknown>): ApiFailure {
  return {
    kind: 'quota',
    status: 429,
    code: 'quota_exceeded',
    message: envelope.error?.message ?? '本期额度已用完',
    ...(envelope.request_id ? { requestId: envelope.request_id } : {}),
    ...(envelope.error?.details !== undefined ? { details: envelope.error.details } : {}),
  }
}

export function unauthorizedFailure(envelope: Envelope<unknown> | null): ApiFailure {
  return {
    kind: 'unauthorized',
    status: 401,
    code: envelope?.error?.code ?? '',
    message: envelope?.error?.message ?? '登录状态已失效，请重新登录',
    ...(envelope?.request_id ? { requestId: envelope.request_id } : {}),
    ...(envelope?.error?.details !== undefined ? { details: envelope.error.details } : {}),
  }
}

export function apiFailure(response: Response, envelope: Envelope<unknown> | null): ApiFailure {
  return {
    kind: 'api',
    status: response.status,
    code: envelope?.error?.code ?? '',
    message: envelope?.error?.message ?? `请求失败（HTTP ${response.status}）`,
    ...(envelope?.request_id ? { requestId: envelope.request_id } : {}),
    ...(envelope?.error?.details !== undefined ? { details: envelope.error.details } : {}),
  }
}

export function malformedFailure(response: Response, message: string): ApiFailure {
  return {
    kind: 'malformed',
    status: response.status,
    code: '',
    message,
    ...(response.headers.get('X-Request-Id')
      ? { requestId: response.headers.get('X-Request-Id') ?? undefined }
      : {}),
  }
}

export function parseEnvelope<T>(text: string): Envelope<T> | null {
  try {
    return JSON.parse(text) as Envelope<T>
  } catch {
    return null
  }
}
