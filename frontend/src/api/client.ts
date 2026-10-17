/**
 * 全应用唯一的 HTTP 出口。
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
 *
 * **两处例外，都是有理由的：**
 * - `upload()` 用 `XMLHttpRequest`，因为 `fetch` 没有上传进度；
 * - `download()` 不走信封，因为工作区导出是一条带 `Content-Disposition`
 *   的裸 ZIP。
 * 两者对 401 / 429 的判定与 `request()` 完全一致。
 */

/** 后端所有业务路由的前缀。开发期这条路径被 Vite 代理原样转给后端。 */
import {
  ApiError,
  apiFailure,
  isQuotaEnvelope,
  malformedFailure,
  networkFailure,
  parseEnvelope,
  quotaFailure,
  rateLimitedFailure,
  unauthorizedFailure,
  type ApiFailure,
  type Envelope,
} from './failure'

const API_BASE = '/api/v1'

/**
 * 当前 access token。由 `src/session.ts` 独占写入 —— 它是唯一知道 token
 * 存在哪、什么时候该清掉的地方。这里只持有，不持久化。
 */
export { ApiError, isApiFailure } from './failure'
export type { ApiFailure, FailureKind } from './failure'

let authToken: string | null = null

/** 由 session 模块在登录 / 恢复 / 登出时调用。 */
export function setAuthToken(token: string | null): void {
  authToken = token
}

/**
 * 凭据失效时的全局出口，由 `src/auth/auth-failure.ts` 注册。
 *
 * 用注册回调而不是直接 import，是为了不让 `client → auth-failure → session
 * → client` 形成一个初始化顺序敏感的环。
 */
let unauthorizedHandler: (() => void) | null = null

export function setUnauthorizedHandler(handler: (() => void) | null): void {
  unauthorizedHandler = handler
}

function reportUnauthorized(): void {
  try {
    unauthorizedHandler?.()
  } catch {
    // 登出流程自己出错不能盖住"这次请求 401 了"这个原始事实。
  }
}

type QueryValue = string | number | boolean | ReadonlyArray<string | number>

interface RequestOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'
  /** 会被序列化成 JSON 并带上 `Content-Type`。 */
  body?: unknown
  /**
   * 纯文本请求体。题库导入的请求体是一段 `.txt` 原文，不是 JSON
   * （`backend/internal/handler/decode.go:80-103`）。
   */
  rawBody?: { text: string; contentType?: string }
  /** 查询串。值会被 `URLSearchParams` 编码；`undefined` 的键被丢掉。 */
  query?: Record<string, QueryValue | undefined>
  /**
   * 带上 `Authorization`。默认 true —— 只有注册与登录是 false，因为那两个
   * 请求的意义正是在还没有 token 的时候拿到一个。
   */
  authenticated?: boolean
  signal?: AbortSignal
}

function buildUrl(path: string, query?: RequestOptions['query']): URL {
  const url = new URL(API_BASE + path, window.location.origin)
  if (query) {
    for (const [key, value] of Object.entries(query)) {
      if (value === undefined) continue
      if (Array.isArray(value)) {
        for (const item of value) url.searchParams.append(key, String(item))
      } else {
        url.searchParams.set(key, String(value))
      }
    }
  }
  return url
}

/* ------------------------------------------------------------------ */
/* JSON 请求                                                           */
/* ------------------------------------------------------------------ */

/**
 * 发一个请求，成功返回信封里的 `data`，失败抛 `ApiError`。
 *
 * 204 没有 body，返回 `undefined`。那是 `as T` 的一次断言，调用方在 204 的
 * 路由上只能用 `void` 接——这条约束没有类型能表达，所以写在注释里。
 */
export async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { method = 'GET', body, rawBody, query, authenticated = true, signal } = options

  const url = buildUrl(path, query)

  const headers = new Headers()
  if (rawBody) {
    headers.set('Content-Type', rawBody.contentType ?? 'text/plain; charset=utf-8')
  } else if (body !== undefined) {
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
      body: rawBody ? rawBody.text : body === undefined ? undefined : JSON.stringify(body),
      signal,
    })
  } catch (cause) {
    // 只有这里抛出来的才算"没到服务端"。AbortError 也走这条路：调用方自己
    // 取消的请求和断网在界面上是同一件事 —— 那一屏已经不在看了。
    throw new ApiError(networkFailure(cause))
  }

  // 429 有自己的出口，因为它是唯一一个**响应头里带着该怎么等**的失败。
  // 但同一个状态码也承担额度用尽，所以要先读信封里的码再决定是"等几秒"还是
  // "等这个周期结束"。
  if (response.status === 429) {
    const text = await response.text()
    const envelope = parseEnvelope<unknown>(text)
    if (isQuotaEnvelope(envelope)) {
      throw new ApiError(quotaFailure(envelope))
    }
    throw new ApiError(
      rateLimitedFailure(response.headers.get('Retry-After'), envelope),
    )
  }

  if (response.status === 204) {
    return undefined as T
  }

  let envelope: Envelope<T>
  try {
    envelope = (await response.json()) as Envelope<T>
  } catch {
    throw new ApiError(malformedFailure(response, '服务端返回了无法解析的响应'))
  }

  if (!response.ok) {
    // 401 单拎出来：后端已经说了是凭据的问题，界面要做的动作是固定的（拆
    // socket、回登录页），不需要再看 code 是 invalid_token 还是
    // authorization_required —— 那是给日志看的，不是给分支看的。
    //
    // **但只在这次请求真的带了凭据时才算**。登录也是 401（`invalid_credentials`），
    // 而它的意思是"你填的这组不对"，不是"你的登录态失效了"——前者要让人改输入
    // 框，后者要把人踢回登录页。两者的区别不在状态码上，在**这次请求有没有带
    // 凭据**上，所以判据取这一条。
    if (response.status === 401 && authenticated) {
      reportUnauthorized()
      throw new ApiError(unauthorizedFailure(envelope))
    }
    throw new ApiError(apiFailure(response, envelope))
  }

  // 200 但没有 data：后端所有成功响应都带 data（204 已在上面返回），所以这
  // 是一个真的"读不懂"，不是合法的空。
  if (envelope.data === undefined) {
    throw new ApiError(malformedFailure(response, '服务端返回的响应缺少 data'))
  }
  return envelope.data
}

/* ------------------------------------------------------------------ */
/* 裸文件下载（工作区导出 ZIP）                                          */
/* ------------------------------------------------------------------ */

export interface DownloadResult {
  blob: Blob
  filename: string
}

function filenameFromDisposition(header: string | null): string | null {
  if (!header) return null
  const encoded = /filename\*\s*=\s*(?:UTF-8'')?([^;]+)/i.exec(header)
  if (encoded) {
    const raw = encoded[1].trim().replace(/^"|"$/g, '')
    try {
      return decodeURIComponent(raw)
    } catch {
      return raw
    }
  }
  const plain = /filename\s*=\s*(?:"([^"]+)"|([^;]+))/i.exec(header)
  if (plain) return (plain[1] ?? plain[2] ?? '').trim() || null
  return null
}

/**
 * 下载一个**没有信封**的响应。
 *
 * 不能把 URL 直接交给 `window.open`：token 在 `Authorization` 头里，导航过去
 * 不会带上它。所以先带凭据取回 blob，再交给调用方触发下载。
 */
export async function download(
  path: string,
  options: { query?: RequestOptions['query']; signal?: AbortSignal } = {},
): Promise<DownloadResult> {
  const url = buildUrl(path, options.query)
  const headers = new Headers()
  if (authToken) headers.set('Authorization', `Bearer ${authToken}`)

  let response: Response
  try {
    response = await fetch(url, { headers, signal: options.signal })
  } catch (cause) {
    throw new ApiError(networkFailure(cause))
  }

  if (response.status === 429) {
    const text = await response.text()
    const envelope = parseEnvelope<unknown>(text)
    if (isQuotaEnvelope(envelope)) throw new ApiError(quotaFailure(envelope))
    throw new ApiError(rateLimitedFailure(response.headers.get('Retry-After'), envelope))
  }

  if (!response.ok) {
    const text = await response.text()
    const envelope = parseEnvelope<unknown>(text)
    if (response.status === 401) {
      reportUnauthorized()
      throw new ApiError(unauthorizedFailure(envelope))
    }
    throw new ApiError(apiFailure(response, envelope))
  }

  const blob = await response.blob()
  return {
    blob,
    filename:
      filenameFromDisposition(response.headers.get('Content-Disposition')) ?? 'download',
  }
}

/* ------------------------------------------------------------------ */
/* 上传（唯一使用 XHR 的地方，因为 fetch 没有上传进度）                    */
/* ------------------------------------------------------------------ */

export interface UploadOptions {
  /** 后端 multipart 的字段名，默认 `file`。 */
  fieldName?: string
  onProgress?: (loaded: number, total: number, percent: number | null) => void
  signal?: AbortSignal
}

/**
 * 上传单个文件。401 / 429 的判定与 `request()` 一模一样：401 交给全局登出
 * 出口，429 带着 `Retry-After` 抛出去让界面画倒计时。
 */
export function upload<T>(
  path: string,
  file: File,
  options: UploadOptions = {},
): Promise<T> {
  const { fieldName = 'file', onProgress, signal } = options

  return new Promise<T>((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('POST', API_BASE + path)
    if (authToken) xhr.setRequestHeader('Authorization', `Bearer ${authToken}`)

    function fail(failure: ApiFailure): void {
      reject(new ApiError(failure))
    }

    xhr.upload.onprogress = (event) => {
      if (!onProgress) return
      const percent =
        event.lengthComputable && event.total > 0
          ? Math.round((event.loaded / event.total) * 100)
          : null
      onProgress(event.loaded, event.total, percent)
    }

    xhr.onerror = () => fail(networkFailure(null))
    xhr.onabort = () =>
      fail({
        kind: 'network',
        status: 0,
        code: 'aborted',
        message: '上传已取消',
      })

    xhr.onload = () => {
      if (xhr.status === 429) {
        const envelope = parseEnvelope<unknown>(xhr.responseText)
        if (isQuotaEnvelope(envelope)) {
          fail(quotaFailure(envelope))
          return
        }
        fail(rateLimitedFailure(xhr.getResponseHeader('Retry-After'), envelope))
        return
      }

      if (xhr.status === 204) {
        resolve(undefined as T)
        return
      }

      const envelope = parseEnvelope<T>(xhr.responseText)
      if (!envelope) {
        const fake = new Response(null, { status: xhr.status })
        fail(malformedFailure(fake, '服务端返回了无法解析的响应'))
        return
      }

      if (xhr.status < 200 || xhr.status >= 300) {
        if (xhr.status === 401) {
          reportUnauthorized()
          fail(unauthorizedFailure(envelope))
          return
        }
        const fake = new Response(null, { status: xhr.status })
        fail(apiFailure(fake, envelope))
        return
      }

      if (envelope.data === undefined) {
        const fake = new Response(null, { status: xhr.status })
        fail(malformedFailure(fake, '服务端返回的响应缺少 data'))
        return
      }
      resolve(envelope.data)
    }

    if (signal) {
      if (signal.aborted) {
        fail({ kind: 'network', status: 0, code: 'aborted', message: '上传已取消' })
        return
      }
      signal.addEventListener('abort', () => xhr.abort(), { once: true })
    }

    const form = new FormData()
    form.append(fieldName, file)
    xhr.send(form)
  })
}
