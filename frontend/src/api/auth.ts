/**
 * 认证相关的四条。注册与登录是仅有的两条**不带** `Authorization` 的请求
 * ——它们的意义正是在还没有 token 的时候拿到一个。
 */

import { request } from './client'

export interface Account {
  user_id: number
  username: string
  /** `user` / `admin` 之类，由后端决定。界面只读不判。 */
  role: string
}

/** 注册与登录的返回，也是"已登录"这个状态的全部内容。 */
export interface Session {
  user: Account
  token: string
  token_type: string
  /** RFC3339 UTC。 */
  expires_at: string
}

export interface Credentials {
  username: string
  password: string
}

/**
 * 建一个账号，**并直接返回登录态**。
 *
 * 所以注册页成功之后不需要再登一次 —— 后端回的是 201 加一个完整的 session，
 * 与登录同形。界面上"注册完还要登录一次"是没有的事。
 */
export function register(credentials: Credentials): Promise<Session> {
  return request<Session>('/auth/register', {
    method: 'POST',
    body: credentials,
    authenticated: false,
  })
}

export function login(credentials: Credentials): Promise<Session> {
  return request<Session>('/auth/login', {
    method: 'POST',
    body: credentials,
    authenticated: false,
  })
}

/**
 * WebSocket 票据。**浏览器连 socket 的第一步。**
 *
 * 浏览器在握手时设不了 `Authorization` 头（`new WebSocket(url, protocols)`
 * 的第二个参数是子协议，不是 header），所以它先发一个**能**带 header 的普
 * 通请求换一张票，再把票放进 URL。
 *
 * 两张凭据解出来的是同一个账号。票**不是一次性的**，只是活得短 —— 所以
 * 每次连接尝试都重新签一张，而不是把上一张留着复用。理由见《前端设计注意
 * 事项》§1.1。
 */
export interface SocketTicket {
  ticket: string
  /** RFC3339 UTC。到了这一刻它就不作数了。 */
  expires_at: string
}

export function wsTicket(): Promise<SocketTicket> {
  return request<SocketTicket>('/ws-ticket', { method: 'POST' })
}

/**
 * 登出：撤回这个账号的**全部** token，包括发起这次请求的那个，204 无 body。
 *
 * 所以它之后不需要再做什么本地清理之外的事 —— 这个请求自己带的那张票也已
 * 经废了。重新登录是唯一的出路。
 */
export function logout(): Promise<void> {
  return request<void>('/auth/logout', { method: 'POST' })
}
