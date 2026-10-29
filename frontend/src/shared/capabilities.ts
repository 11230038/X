/**
 * 能力清单的模块级缓存。
 *
 * 清单里的名字是**数据，不是枚举**：对话页的能力选择器只画服务端返回的名
 * 字；"新建掌握路径"的入口也只在清单里真有 `mastery_path` 时出现。写死一份
 * 名单会在后端增删能力时悄悄变成谎话。
 *
 * 同一次请求还带回一份工具描述：能力只声明它挂载哪些工具（名字），"这个工具
 * 是干什么的"由工具自己给出，所以目录里那句话与模型看到的是同一句。
 */

import { computed, ref } from 'vue'

import { isApiFailure, type ApiFailure } from '../api/client'
import { listCapabilities, type CapabilityManifest, type CapabilityTool } from '../api/capabilities'

export type CapabilityStatus = 'idle' | 'loading' | 'ready' | 'failed'

/**
 * 归一化之后的能力：`stages` / `tools` 一定是数组。
 *
 * Go 的空切片会序列化成 `null`；在入口一次转成 `[]`，页面就不用每次判断。
 * 类型也跟着收紧，否则每个消费方都得写一遍 `?? []`。
 */
export interface Capability extends Omit<CapabilityManifest, 'stages' | 'tools'> {
  stages: string[]
  tools: string[]
}

const capabilities = ref<Capability[]>([])
const tools = ref<CapabilityTool[]>([])
const status = ref<CapabilityStatus>('idle')
const failure = ref<ApiFailure | null>(null)

let inFlight: Promise<void> | null = null

export const capabilityList = computed(() => capabilities.value)
export const capabilityStatus = computed(() => status.value)
export const capabilityFailure = computed(() => failure.value)

/**
 * 工具名 → 那句描述。工具页按能力反查工具名，再用这张表补上"它是干什么的"。
 *
 * 清单里没有名字时不给兜底句子：宁可这一行不写描述，也不编一句服务端没说过
 * 的话。
 */
export const toolDescriptions = computed(
  () => new Map(tools.value.map((tool) => [tool.name, tool.description])),
)

export function refreshCapabilities(): Promise<void> {
  if (inFlight) return inFlight
  if (capabilities.value.length === 0) status.value = 'loading'
  failure.value = null

  inFlight = (async () => {
    try {
      const result = await listCapabilities()
      capabilities.value = result.capabilities.map(
        (capability): Capability => ({
          ...capability,
          stages: capability.stages ?? [],
          tools: capability.tools ?? [],
        }),
      )
      tools.value = result.tools ?? []
      status.value = 'ready'
      failure.value = null
    } catch (cause) {
      failure.value = isApiFailure(cause)
        ? cause.failure
        : {
            kind: 'malformed',
            status: 0,
            code: '',
            message: cause instanceof Error ? cause.message : '能力清单加载失败',
          }
      if (capabilities.value.length === 0) status.value = 'failed'
    } finally {
      inFlight = null
    }
  })()

  return inFlight
}

export function resetCapabilities(): void {
  capabilities.value = []
  tools.value = []
  status.value = 'idle'
  failure.value = null
  inFlight = null
}

export function hasCapability(name: string): boolean {
  return capabilities.value.some((capability) => capability.name === name)
}

/** 去重后的工具名，供用户管理页的授权选择器使用。 */
export function knownTools(): string[] {
  const tools = new Set<string>()
  for (const capability of capabilities.value) {
    for (const tool of capability.tools) tools.add(tool)
  }
  return [...tools].sort()
}
