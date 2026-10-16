/**
 * 全应用唯一的 `fetch` 出口。
 *
 * 所有 HTTP 请求都从这里走，原因在 PRD §15.4 与《前端设计注意事项》§1.3、
 * §4.2：**401 与 429 必须走不同的出口**，而"哪些地方会 401"这个问题的答
 * 案只有在请求集中在一处时才是一个可维护的答案。401 意味着这个账号的凭据
 * 已经被撤回了，界面要做的是拆掉 socket 并回到登录页；429 意味着服务端让
 * 你等几秒，界面要做的是倒计时。把这两个混成一个"请求失败"，界面就会在明
 * 明只需等两秒的时候把人踢回登录页。
 *
 * 这里不引 axios：裸 `fetch` 够用，而多一个依赖就多一处要在鉴权改动时跟着
 * 改的地方。
 */

/** 后端所有业务路由的前缀。开发期这条路径被 Vite 代理原样转给后端。 */
const API_BASE = '/api/v1'

/**
 * 当前 access token。由 `src/session.ts` 独占写入 —— 它是唯一知道 token
 * 存在哪、什么时候该清掉的地方。这里只持有，不持久化。
 */
let authToken: string | null = null

/** 由 session 模块在登录 / 恢复 / 登出时调用。 */
export function setAuthToken(token: string | null): void {
  authToken = token
}

/**
 * 一个请求失败的分类。界面按 `kind` 分支，而不是按 HTTP 状态码 —— 状态码
 * 是后端的实现细节，`kind` 才是"我该做什么"。
 */
export type FailureKind =
  /** 凭据没了或被撤回。→ 拆 socket、回登录页（§1.3）。 */
  | 'unauthorized'
  /** 服务端让等几秒。→ 倒计时，不是"网络不通"（§4.2）。 */
  | 'rate_limited'
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
interface Envelope<T> {
  data?: T
  error?: { code: string; message: string }
  request_id?: string
}

export interface RequestOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'
  /** 会被序列化成 JSON 并带上 `Content-Type`。 */
  body?: unknown
  /** 查询串。值会被 `URLSearchParams` 编码；`undefined` 的键被丢掉。 */
  query?: Record<string, string | number | boolean | undefined>
  /**
   * 带上 `Authorization`。默认 true —— 只有注册与登录是 false，因为那两个
   * 请求的意义正是在还没有 token 的时候拿到一个。
   */
  authenticated?: boolean
  signal?: AbortSignal
}

/**
 * 发一个请求，成功返回信封里的 `data`，失败抛 `ApiError`。
 *
 * 204 没有 body，返回 `undefined`。那是 `as T` 的一次断言，调用方在 204 的
 * 路由上只能用 `void` 接——这条约束没有类型能表达，所以写在注释里。
 */
export async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { method = 'GET', body, query, authenticated = true, signal } = options

  const url = new URL(API_BASE + path, window.location.origin)
  if (query) {
    for (const [key, value] of Object.entries(query)) {
      if (value !== undefined) {
        url.searchParams.set(key, String(value))
      }
    }
  }

  const headers = new Headers()
  if (body !== undefined) {
    headers.set('Content-Type', 'application/json')
  }
  if (authenticated && authToken) {
    headers.set('Authorization', `Bearer ${authToken}`)
  }

  let response: Response
  try {
    response = await fetch(url, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      signal,
    })
  } catch (cause) {
    // 只有这里抛出来的才算"没到服务端"。AbortError 也走这条路：调用方自己
    // 取消的请求和断网在界面上是同一件事 —— 那一屏已经不在看了。
    throw new ApiError({
      kind: 'network',
      status: 0,
      code: '',
      message: '网络不可达，请检查连接后重试',
      ...(cause instanceof DOMException && cause.name === 'AbortError' ? { code: 'aborted' } : {}),
    })
  }

  // 429 有自己的出口，因为它是唯一一个**响应头里带着该怎么等**的失败。
  // `Retry-After` 是秒数整数；解析不出来时不猜一个数，让界面自己去退避。
  if (response.status === 429) {
    const retryAfter = Number.parseInt(response.headers.get('Retry-After') ?? '', 10)
    throw new ApiError({
      kind: 'rate_limited',
      status: 429,
      code: 'rate_limited',
      message: '请求太频繁，请稍候再试',
      ...(Number.isFinite(retryAfter) && retryAfter > 0
        ? { retryAfterSeconds: retryAfter }
        : {}),
    })
  }

  if (response.status === 204) {
    return undefined as T
  }

  let envelope: Envelope<T>
  try {
    envelope = (await response.json()) as Envelope<T>
  } catch {
    throw new ApiError({
      kind: 'malformed',
      status: response.status,
      code: '',
      message: '服务端返回了无法解析的响应',
    })
  }

  if (!response.ok) {
    const code = envelope.error?.code ?? ''
    // 401 单拎出来：后端已经说了是凭据的问题，界面要做的动作是固定的（拆
    // socket、回登录页），不需要再看 code 是 invalid_token 还是
    // authorization_required —— 那是给日志看的，不是给分支看的。
    //
    // **但只在这次请求真的带了凭据时才算**。登录也是 401（`invalid_credentials`），
    // 而它的意思是"你填的这组不对"，不是"你的登录态失效了"——前者要让人改输入
    // 框，后者要把人踢回登录页。两者的区别不在状态码上，在**这次请求有没有带
    // 凭据**上，所以判据取这一条。
    if (response.status === 401 && authenticated) {
      throw new ApiError({
        kind: 'unauthorized',
        status: 401,
        code,
        message: envelope.error?.message ?? '登录状态已失效，请重新登录',
        ...(envelope.request_id ? { requestId: envelope.request_id } : {}),
      })
    }
    throw new ApiError({
      kind: 'api',
      status: response.status,
      code,
      message: envelope.error?.message ?? '请求失败',
      ...(envelope.request_id ? { requestId: envelope.request_id } : {}),
    })
  }

  // 200 但没有 data：后端所有成功响应都带 data（204 已在上面返回），所以这
  // 是一个真的"读不懂"，不是合法的空。
  if (envelope.data === undefined) {
    throw new ApiError({
      kind: 'malformed',
      status: response.status,
      code: '',
      message: '服务端返回的响应缺少 data',
    })
  }
  return envelope.data
}
