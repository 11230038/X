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
  /**
   * 是不是部署白名单里的超级管理员。
   *
   * 它只用来决定**画不画**账号管理的那几个按钮；真正的门在服务端，四条生
   * 命周期路由各自有一层白名单检查。别拿它当权限判断用 —— 改一下 localStorage
   * 就能让这个字段为 true，而后端不会因此多放行一条请求。
   */
  super_admin: boolean
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

/** 当前账号。用于设置页确认 token 对应的仍是同一个人。 */
export function me(): Promise<Account> {
  return request<{ user: Account }>('/auth/me').then((result) => result.user)
}

/**
 * 改密：204，并且后端会吊销**包括当前 token 在内**的全部凭据
 * （`backend/internal/handler/auth.go:204-222`）。
 *
 * 所以成功后不是"继续用"，而是按主动登出处理：清会话、回登录页、提示
 * "密码已修改，请重新登录"。
 */
export function changePassword(oldPassword: string, newPassword: string): Promise<void> {
  return request<void>('/me/password', {
    method: 'PUT',
    body: { old_password: oldPassword, new_password: newPassword },
  })
}
