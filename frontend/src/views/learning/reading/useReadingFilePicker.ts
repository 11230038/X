/**
 * 从文件库打开一份材料。
 *
 * "已上传但未解析"是正常状态：文件在这里明确报出原因，用户去文件库挂进
 * 知识库之后再来；不把"打不开"伪装成材料不存在。
 */

import { reactive, ref, type Ref } from 'vue'

import type { ApiFailure } from '../../../api/client'
import { describeFailure } from '../../../api/errors'
import { getFile, listFiles, type FileSummary } from '../../../api/files'
import { createReadingMaterial, type ReadingMaterial } from '../../../api/reading'
import { READING } from '../../../copy/reading'
import { toFailure } from '../../../shared/resource'
import type { ReadingStatus } from './types'

export function useReadingFilePicker(options: {
  currentId: Ref<string>
  materials: Ref<ReadingMaterial[]>
  replaceRoute: (materialId: string) => void
  reloadOutline: () => Promise<void>
  closeDialog: () => void
}) {
  const files = ref<FileSummary[]>([])
  const filesStatus = ref<ReadingStatus>('idle')
  const filesFailure = ref<ApiFailure | null>(null)
  const openingFileId = ref('')
  const fileErrors = reactive<Record<string, string>>({})

  async function loadFiles(): Promise<void> {
    if (filesStatus.value === 'ready') return
    filesStatus.value = 'loading'
    filesFailure.value = null
    try {
      files.value = (await listFiles({ limit: 100 })).files
      filesStatus.value = 'ready'
    } catch (cause) {
      filesFailure.value = toFailure(cause)
      filesStatus.value = 'failed'
    }
  }

  async function chooseFile(file: FileSummary): Promise<void> {
    openingFileId.value = file.id
    delete fileErrors[file.id]
    try {
      const view = await getFile(file.id)
      if (!view.document) {
        fileErrors[file.id] = READING.unparsed
        return
      }
      const created = await createReadingMaterial(file.id, 'library')
      if (!options.materials.value.some((item) => item.material_id === created.material.material_id)) {
        options.materials.value = [created.material, ...options.materials.value]
      }
      options.closeDialog()
      options.currentId.value = created.material.material_id
      options.replaceRoute(created.material.material_id)
      await options.reloadOutline()
    } catch (cause) {
      fileErrors[file.id] = describeFailure(toFailure(cause))
    } finally {
      openingFileId.value = ''
    }
  }

  return {
    files,
    filesStatus,
    filesFailure,
    openingFileId,
    fileErrors,
    loadFiles,
    chooseFile,
  }
}
