/**
 * 一次性问答。
 *
 * `POST /chat/complete` 是唯一一条不围着"轮次"转的模型调用：不开会话、
 * 不写消息、不带历史，答案只活在这次响应里。文档问答助手用它回答选区，
 * 因此问完不在任何对话里留下痕迹。
 */

import { request } from './client'

export interface DirectAnswer {
  answer: string
}

export function completeQuestion(question: string): Promise<DirectAnswer> {
  return request<DirectAnswer>('/chat/complete', {
    method: 'POST',
    body: { question },
  })
}
