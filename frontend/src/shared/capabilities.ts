/**
 * 能力清单的模块级缓存。
 *
 * 清单里的名字是**数据，不是枚举**：对话页的能力选择器只画服务端返回的名
 * 字；"新建掌握路径"的入口也只在清单里真有 `mastery_path` 时出现。写死一份
 * 名单会在后端增删能力时悄悄变成谎话。
 */

import { computed, ref } from 'vue'

import { isApiFailure, type ApiFailure } from '../api/client'
import { listCapabilities, type CapabilityManifest } from '../api/capabilities'

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
const status = ref<CapabilityStatus>('idle')
const failure = ref<ApiFailure | null>(null)

let inFlight: Promise<void> | null = null

export const capabilityList = computed(() => capabilities.value)
export const capabilityStatus = computed(() => status.value)
export const capabilityFailure = computed(() => failure.value)

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
  status.value = 'idle'
  failure.value = null
  inFlight = null
}

export function hasCapability(name: string): boolean {
  return capabilities.value.some((capability) => capability.name === name)
}

/** 去重后的工具名，供管理台的授权选择器使用。 */
export function knownTools(): string[] {
  const tools = new Set<string>()
  for (const capability of capabilities.value) {
    for (const tool of capability.tools) tools.add(tool)
  }
  return [...tools].sort()
}
