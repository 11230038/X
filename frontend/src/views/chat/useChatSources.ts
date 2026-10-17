/**
 * 来源面板与预览。
 *
 * 引用点击后先落到预览：能对上阅读材料就给"打开阅读页"，对不上就退回文件
 * 文本。材料列表拿不到不是面板失败 —— 文件那条出口仍然可用。
 */

import { ref, type ComputedRef } from 'vue'
import type { Router } from 'vue-router'

import { listReadingMaterials, type ReadingMaterial } from '../../api/reading'
import { canSurface } from '../../auth/permissions'
import type { PanelTab } from '../../components/shell/SidePanel.vue'
import type { SourceItem } from '../../ws/blocks'

export function useChatSources(deps: {
  router: Router
  activeId: ComputedRef<string | null>
  sources: ComputedRef<readonly SourceItem[]>
}) {
  const panelTab = ref<PanelTab>('activity')
  const activeSource = ref<SourceItem | null>(null)
  const activeMaterialId = ref('')
  const materialResolving = ref(false)
  const materials = ref<ReadingMaterial[]>([])
  let materialsLoaded = false
  let materialsInFlight: Promise<void> | null = null

  async function ensureMaterials(): Promise<void> {
    if (materialsLoaded || canSurface('reading') === false) return
    if (materialsInFlight) return materialsInFlight
    materialResolving.value = true
    materialsInFlight = (async () => {
      try {
        materials.value = (await listReadingMaterials()).materials
        materialsLoaded = true
      } catch {
        // 拿不到材料列表不是引用面板的失败：文件文本那条出口仍然可用。
      } finally {
        materialResolving.value = false
        materialsInFlight = null
      }
    })()
    return materialsInFlight
  }

  function openSource(source: SourceItem): void {
    activeSource.value = source
    activeMaterialId.value = ''
    panelTab.value = 'preview'
    void ensureMaterials().then(() => {
      if (activeSource.value !== source) return
      activeMaterialId.value =
        materials.value.find((material) => material.content_id === source.document_id)?.material_id ??
        ''
    })
  }

  function onCitation(index: number): void {
    const source = deps.sources.value.find((item) => item.index === index)
    if (source) openSource(source)
  }

  function openReading(): void {
    const source = activeSource.value
    if (!source || activeMaterialId.value === '') return
    void deps.router.push({
      name: 'reading',
      query: {
        material: activeMaterialId.value,
        ordinal: String(source.locator.ordinal ?? 0),
        start: String(source.locator.char_start),
        end: String(source.locator.char_end),
        // 引用是从这个会话里点开的，阅读页据此把批注与位置挂回同一个会话。
        ...(deps.activeId.value ? { session: deps.activeId.value } : {}),
      },
    })
  }

  function openFile(): void {
    const source = activeSource.value
    if (!source) return
    void deps.router.push({ name: 'files', query: { file: source.file_id, text: '1' } })
  }

  return {
    panelTab,
    activeSource,
    activeMaterialId,
    materialResolving,
    ensureMaterials,
    openSource,
    onCitation,
    openReading,
    openFile,
  }
}
