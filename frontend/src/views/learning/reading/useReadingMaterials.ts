/**
 * 材料目录与当前材料。
 *
 * 换材料是**路由动作**：地址里始终是正在读的那一份，刷新或分享链接不会
 * 落回第一份。材料不存在时 `clearMaterial` 清掉地址，不留下死 id。
 */

import { ref, type Ref } from 'vue'
import type { RouteLocationNormalizedLoaded, Router } from 'vue-router'

import type { ApiFailure } from '../../../api/client'
import { listReadingMaterials, type ReadingMaterial } from '../../../api/reading'
import { toFailure } from '../../../shared/resource'
import type { ReadingStatus } from './types'

export function useReadingMaterials(
  route: RouteLocationNormalizedLoaded,
  router: Router,
  currentId: Ref<string>,
  sessionId: Ref<string>,
  reloadOutline: () => Promise<void>,
  onClear: () => void,
) {
  const materials = ref<ReadingMaterial[]>([])
  const materialsStatus = ref<ReadingStatus>('idle')
  const materialsFailure = ref<ApiFailure | null>(null)

  async function loadMaterials(): Promise<void> {
    materialsStatus.value = 'loading'
    materialsFailure.value = null
    try {
      const loaded = await listReadingMaterials()
      materials.value = loaded.materials
      materialsStatus.value = 'ready'
      const requested = typeof route.query.material === 'string' ? route.query.material : ''
      const chosen = requested || materials.value[0]?.material_id || ''
      if (chosen) {
        currentId.value = chosen
        if (route.query.material !== chosen) replaceRoute(chosen)
        await reloadOutline()
      }
    } catch (cause) {
      materialsFailure.value = toFailure(cause)
      materialsStatus.value = 'failed'
    }
  }

  function replaceRoute(materialId: string): void {
    void router.replace({
      name: 'reading',
      query: {
        material: materialId,
        ...(sessionId.value ? { session: sessionId.value } : {}),
      },
    })
  }

  function selectMaterial(materialId: string): void {
    if (materialId === currentId.value) return
    currentId.value = materialId
    replaceRoute(materialId)
    void reloadOutline()
  }

  function clearMaterial(): void {
    currentId.value = ''
    onClear()
    void router.replace({ name: 'reading', query: {} })
  }

  return {
    materials,
    materialsStatus,
    materialsFailure,
    loadMaterials,
    replaceRoute,
    selectMaterial,
    clearMaterial,
  }
}
