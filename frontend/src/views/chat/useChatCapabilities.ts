/**
 * 能力选择。
 *
 * 选项整个来自 `/capabilities`，这里不筛也不排；默认选 `chat`，没有就选第一项。
 * 阶段名与阶段总数由所选能力决定，所以 `stagePlan` 交给连接层去读。
 */

import { computed, ref } from 'vue'

import { canSurface } from '../../auth/permissions'
import { capabilityList, refreshCapabilities } from '../../shared/capabilities'

export function useChatCapabilities() {
  const capabilityName = ref('')

  const activeCapability = computed(
    () => capabilityList.value.find((manifest) => manifest.name === capabilityName.value) ?? null,
  )

  /** 阶段的顺序表。放进连接是为了把 `stage_start` 渲染成"第几步 / 共几步"。 */
  const stagePlan = computed<readonly string[]>(() => activeCapability.value?.stages ?? [])

  function chooseDefaultCapability(): void {
    if (capabilityName.value !== '') return
    capabilityName.value =
      capabilityList.value.find((manifest) => manifest.name === 'chat')?.name ??
      capabilityList.value[0]?.name ??
      ''
  }

  async function loadCapabilities(): Promise<void> {
    if (canSurface('chat') === false) return
    await refreshCapabilities()
    chooseDefaultCapability()
  }

  return {
    capabilityName,
    activeCapability,
    stagePlan,
    chooseDefaultCapability,
    loadCapabilities,
  }
}
