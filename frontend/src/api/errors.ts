/**
 * 后端的 `error.code` → 界面上那一句话。
 *
 * 集中在这里而不是散在表单里，是因为**同一个码在界面上的出口是固定的**：
 * `account_disabled` 永远不是"请重填密码"，`account_locked` 永远不能说"还剩几
 * 次"。这些判断写在两个地方就会有一天只改了一处。
 *
 * 文案的形状守着 `设计说明书.md` §7.1 与 §6.2 硬规则 2：
 * **每一句都要带一个用户能做的动作**，且不写解释性小字。
 */

import type { ApiFailure } from './client'

/** 认证类失败的文案。登录与注册各有一组，码不重叠。 */
const AUTH_MESSAGES: Record<string, string> = {
  // 登录。§4.1 要求这三种的出口各不相同，所以三句话必须真的不一样：
  'invalid_credentials': '用户名或密码不对，请重新输入',
  // 不是让他重填密码 —— 重填一百次都没用。
  'account_disabled': '这个账号已被停用，请联系管理员',
  // 说清是"暂时"。**不许出现"还剩几次"** —— 那等于把阈值告诉攻击者。
  'account_locked': '失败次数过多，账号已被暂时锁定，请稍后再试',

  // 注册。三句话都给出**具体边界**，否则"不合法"只是一个拒收。
  // 边界来自 `backend/internal/auth/validation.go`：用户名 `[A-Za-z0-9_-]{3,32}`，
  // 密码 8–72 字节。
  'invalid_username': '用户名只能用字母、数字、下划线或连字符，长度 3–32',
  'invalid_password': '密码长度需要在 8–72 之间',
  'username_taken': '这个用户名已经被占用，换一个',

  // 两个页面都可能拿到的。
  'invalid_request': '提交的内容没能被接受，请检查后重试',
  'request_too_large': '提交的内容太大，请缩短后重试',
}

/**
 * WebSocket **命令**错误的文案。
 *
 * 与上面那一组不是同一件事：那些是 HTTP 请求失败，这些是"这条连接还活着，但
 * 你刚发的那条命令它没照做"（`internal/ws/dispatch.go` 的 `describe`）。服务端
 * 给的是英文，而且是说给开发者听的（`"this session is already running a turn"`），
 * 所以这一组必须在这里换一遍 —— 帧上的 `message` 就是屏幕上会出现的全部。
 *
 * `retryable` 挨着文案一起定，**不读帧上的 `terminal`**：那个字段说的是"这条连
 * 接到此为止"（版本对不上之类），与"再发一次有没有用"是两件事。把它当成重试
 * 性，会让"这个会话正在跑一轮"也挂上"可以重试"，而立刻重发必然再失败一次。
 */
const COMMAND_MESSAGES: Record<string, { text: string; retryable: boolean }> = {
  // 同一个会话在两个标签页里打开时最常见：另一边有一轮在跑。
  'active_turn_conflict': { text: '这个会话正在跑一轮，等它结束再发', retryable: false },
  // 提问已经被答过，或者等超时了（`turn.ErrNotWaiting`）。
  'turn_not_waiting': { text: '这一轮已经不在等回答了，重新问一次', retryable: true },
  'empty_content': { text: '内容是空的，写点什么再发', retryable: true },
  'empty_title': { text: '标题不能是空的', retryable: false },
  'session_not_found': { text: '这个会话已经不在了，回列表里换一个', retryable: false },
  'turn_not_found': { text: '这一轮已经不在了，重新问一次', retryable: true },
  // `check_active_turn` 说"没有正在跑的一轮"时走的是 `active_turn` 帧，不是错误；
  // 这一个码只在订阅时点了一个没有活跃轮的会话才会出现。
  'no_active_turn': { text: '这个会话现在没有在跑的一轮', retryable: false },
  'not_found': { text: '服务端没有这个能力', retryable: false },
  'malformed_command': { text: '这条命令没能被服务端接受', retryable: false },
  'unsupported_command': { text: '这一版还不支持这个操作', retryable: false },
  'unsupported_protocol_version': { text: '客户端与服务端的协议版本对不上，请刷新页面', retryable: false },
  'slow_consumer': { text: '这一侧收得太慢，连接被服务端断开了', retryable: false },
  'internal_error': { text: '服务端没能完成这一步，请重试', retryable: true },
}

/**
 * 一条命令级错误在界面上长什么样。
 *
 * 认不出来的码也不把服务端那句英文摆出去：给一句中性的话，`code` 仍然会以标签
 * 的形式显示在旁边（那是排查时要看的），**但用户读的那一句必须是中文**。
 */
export function describeCommandError(code: string): { text: string; retryable: boolean } {
  return COMMAND_MESSAGES[code] ?? { text: '这一步没能完成，请重试', retryable: true }
}

/** 网络层与收尾层的兜底。到这一步说明请求没到服务端，或者服务端坏了。 */
export function describeFailure(failure: ApiFailure): string {
  if (failure.kind === 'network') {
    return '连不上服务，请检查网络后重试'
  }
  if (failure.kind === 'rate_limited') {
    // 只有服务端说了等多久才说具体秒数；没说的不给一个编出来的数。
    return failure.retryAfterSeconds
      ? `请求太频繁，请 ${failure.retryAfterSeconds} 秒后再试`
      : '请求太频繁，请稍后再试'
  }
  if (failure.kind === 'unauthorized') {
    return '登录状态已失效，请重新登录'
  }
  return AUTH_MESSAGES[failure.code] ?? '操作没能完成，请重试'
}
