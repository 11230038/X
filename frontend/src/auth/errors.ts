import { ApiError } from './types'

// The backend answers with stable error codes and English messages. The codes
// are the contract; the wording below is what the user reads.
const messages: Record<string, string> = {
  invalid_request: '请求格式不正确',
  request_too_large: '请求内容过大',
  invalid_username: '用户名需为 3-32 位字母、数字、下划线或连字符',
  invalid_password: '密码长度需为 8-72 字节',
  username_taken: '该用户名已被占用',
  invalid_credentials: '用户名或密码错误',
  account_disabled: '账号已被禁用',
  network_error: '无法连接服务器，请稍后重试',
}

export function errorMessage(cause: unknown): string {
  if (cause instanceof ApiError) {
    return messages[cause.code] ?? '请求失败，请稍后重试'
  }
  return '请求失败，请稍后重试'
}
