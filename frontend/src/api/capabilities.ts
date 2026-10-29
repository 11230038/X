/**
 * 这个 build 会跑哪些能力，以及每个能力的**阶段表**。
 *
 * 阶段表是这条接口在前端唯一被用到的理由：`设计说明书.md` §4.2 与《前端设计
 * 注意事项》§4.2 都明写阶段名与阶段总数**不许写死**（"分析 → 生成 → 校对"是
 * 一个很顺手的错误假设，而这个 build 里 chat 只有两段）。所以"第几步 / 共几
 * 步"的数字只能从这儿来。
 */

import { request } from './client'

export interface CapabilityManifest {
  /** 发起一轮时放在命令的 `capability` 字段里。 */
  name: string
  description: string
  /**
   * 这个能力的阶段，**顺序就是它走的顺序**。
   *
   * 事件里的 `stage` 用的就是这里的字符串，但**不能反过来说"界面上出现过的
   * stage 都在这里"**：一个能力的阶段表是它打算走的，`stage_start` 是它真走
   * 到的。两张表不一致时以事件为准，界面照事件画。
   */
  /** 空切片在 Go 侧可能序列化成 null，消费方一律先归一成数组。 */
  stages: string[] | null
  tools: string[] | null
}

/**
 * 工具清单里的一行。
 *
 * 能力声明的是工具**名字**；这句描述来自工具自己的定义，与模型调用时看到的
 * 是同一句，所以目录不会用一种模型没听过的说法介绍工具。
 */
export interface CapabilityTool {
  name: string
  description: string
}

export function listCapabilities(): Promise<{
  capabilities: CapabilityManifest[]
  tools: CapabilityTool[] | null
}> {
  return request<{ capabilities: CapabilityManifest[]; tools: CapabilityTool[] | null }>(
    '/capabilities',
  )
}
