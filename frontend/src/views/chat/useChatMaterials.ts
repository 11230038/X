/**
 * 这一轮带上的材料：附件、知识库与阅读选区。
 *
 * 附件与知识库都从服务端列表里选，前端不预判能不能读——能不能用由后端的地
 * 面化结果表达。选区从阅读页的 query 带进来，只用来填一个 chip。
 *
 * 人格不在这里持有（它有自己的 composable），但**这一轮的命令**只有这一处
 * 拼得出来，所以人格 id 从这里进 `SendOptions`：否则 `send` 与排队发送两条
 * 路径会各拼一份，迟早有一边漏掉。
 */

import { computed, ref, type Ref } from 'vue'
import type { RouteLocationNormalizedLoaded } from 'vue-router'

import type { ApiFailure } from '../../api/client'
import { listFiles, type FileSummary } from '../../api/files'
import { listKnowledgeBases, type KnowledgeBase } from '../../api/knowledgeBases'
import type { PickerItem } from '../../components/chat/MaterialPicker.vue'
import { formatBytes, formatDate, truncate } from '../../shared/format'
import { toFailure } from '../../shared/resource'
import type { SendOptions, TurnSelection } from '../../ws/socket'

type PickerKind = 'attachments' | 'knowledgeBases'

export function useChatMaterials(
  route: RouteLocationNormalizedLoaded,
  personaId: Ref<string>,
) {
  const picker = ref<PickerKind | null>(null)
  const files = ref<FileSummary[]>([])
  const filesLoaded = ref(false)
  const filesLoading = ref(false)
  const filesFailure = ref<ApiFailure | null>(null)
  const selectedFileIds = ref<string[]>([])

  const knowledgeBases = ref<KnowledgeBase[]>([])
  const kbLoaded = ref(false)
  const kbLoading = ref(false)
  const kbFailure = ref<ApiFailure | null>(null)
  const selectedKbIds = ref<string[]>([])

  const selection = ref<TurnSelection | null>(null)

  const fileItems = computed<PickerItem[]>(() =>
    files.value.map((file) => ({
      id: file.id,
      label: file.filename,
      detail: `${formatBytes(file.size_bytes)} · ${formatDate(file.created_at)}`,
    })),
  )

  const kbItems = computed<PickerItem[]>(() =>
    knowledgeBases.value.map((base) => ({
      id: base.kb_id,
      label: base.name,
      detail: base.description ?? '',
    })),
  )

  const attachmentChips = computed(() =>
    selectedFileIds.value.map((id) => ({
      id,
      label: fileItems.value.find((item) => item.id === id)?.label ?? id,
    })),
  )

  const kbChips = computed(() =>
    selectedKbIds.value.map((id) => ({
      id,
      label: kbItems.value.find((item) => item.id === id)?.label ?? id,
    })),
  )

  const selectionChip = computed(() =>
    selection.value ? { label: truncate(selection.value.text, 40) } : null,
  )

  async function loadFiles(force = false): Promise<void> {
    if (filesLoaded.value && !force) return
    filesLoading.value = true
    filesFailure.value = null
    try {
      files.value = (await listFiles({ limit: 100 })).files
      filesLoaded.value = true
    } catch (cause) {
      filesFailure.value = toFailure(cause)
    } finally {
      filesLoading.value = false
    }
  }

  async function loadKnowledgeBases(force = false): Promise<void> {
    if (kbLoaded.value && !force) return
    kbLoading.value = true
    kbFailure.value = null
    try {
      knowledgeBases.value = (await listKnowledgeBases()).knowledge_bases
      kbLoaded.value = true
    } catch (cause) {
      kbFailure.value = toFailure(cause)
    } finally {
      kbLoading.value = false
    }
  }

  function openPicker(kind: PickerKind): void {
    picker.value = kind
    if (kind === 'attachments') void loadFiles()
    else void loadKnowledgeBases()
  }

  function toggleFile(id: string): void {
    selectedFileIds.value = selectedFileIds.value.includes(id)
      ? selectedFileIds.value.filter((item) => item !== id)
      : [...selectedFileIds.value, id]
  }

  function toggleKb(id: string): void {
    selectedKbIds.value = selectedKbIds.value.includes(id)
      ? selectedKbIds.value.filter((item) => item !== id)
      : [...selectedKbIds.value, id]
  }

  function clearMaterials(): void {
    selectedFileIds.value = []
    selectedKbIds.value = []
    selection.value = null
  }

  function buildOptions(): SendOptions {
    const options: SendOptions = {}
    if (selectedFileIds.value.length > 0) options.attachments = [...selectedFileIds.value]
    if (selectedKbIds.value.length > 0) options.knowledgeBases = [...selectedKbIds.value]
    if (selection.value) options.selection = selection.value
    if (personaId.value !== '') options.persona = personaId.value
    return options
  }

  /**
   * 阅读页把选区带过来：`?material=&ordinal=&start=&end=&text=`。
   * 这些参数只用来填一个 chip；真正的坐标合法性由服务端按材料文本核对。
   */
  function readSelectionFromQuery(): void {
    const material = route.query.material
    const start = Number(route.query.start)
    const end = Number(route.query.end)
    if (typeof material !== 'string' || material === '') return
    if (!Number.isFinite(start) || !Number.isFinite(end)) return
    const ordinal = Number(route.query.ordinal)
    const text = typeof route.query.text === 'string' ? route.query.text : ''
    selection.value = {
      material_id: material,
      locator: {
        kind: 'window',
        ...(Number.isFinite(ordinal) && ordinal > 0 ? { ordinal } : {}),
        char_start: start,
        char_end: end,
      },
      text,
    }
  }

  return {
    picker,
    files,
    filesLoading,
    filesFailure,
    selectedFileIds,
    knowledgeBases,
    kbLoading,
    kbFailure,
    selectedKbIds,
    selection,
    fileItems,
    kbItems,
    attachmentChips,
    kbChips,
    selectionChip,
    loadFiles,
    loadKnowledgeBases,
    openPicker,
    toggleFile,
    toggleKb,
    clearMaterials,
    buildOptions,
    readSelectionFromQuery,
  }
}
