/**
 * 单文件上传。
 *
 * 用带进度的上传（XHR），取消走 `AbortController`。上传成功后把它所在的
 * "在用"列表刷新一遍并选中它 —— 用户刚传的东西应该直接看得见。
 */

import { computed, ref, type Ref } from 'vue'

import { isApiFailure } from '../../api/client'
import { describeFailure } from '../../api/errors'
import { uploadFile } from '../../api/files'
import { UPLOAD_ERRORS } from '../../copy/files'
import { formatBytes } from '../../shared/format'
import { notify } from '../../shared/toast'

import type { useFileDetail } from './useFileDetail'
import type { useFileList } from './useFileList'

export function useFileUpload(deps: {
  list: ReturnType<typeof useFileList>
  detail: ReturnType<typeof useFileDetail>
  fileInput: Ref<HTMLInputElement | null>
}) {
  const uploading = ref(false)
  const uploadName = ref('')
  const uploadPercent = ref<number | null>(0)
  const uploadLoaded = ref(0)
  const uploadTotal = ref(0)
  const uploadError = ref<string | null>(null)
  let uploadController: AbortController | null = null

  function chooseFile(): void {
    if (uploading.value) return
    deps.fileInput.value?.click()
  }

  function onPicked(event: Event): void {
    const input = event.target as HTMLInputElement
    const picked = input.files?.[0]
    input.value = ''
    if (picked) void startUpload(picked)
  }

  async function startUpload(file: File): Promise<void> {
    uploading.value = true
    uploadName.value = file.name
    uploadPercent.value = 0
    uploadLoaded.value = 0
    uploadTotal.value = 0
    uploadError.value = null
    uploadController = new AbortController()

    try {
      const uploaded = await uploadFile(file, {
        signal: uploadController.signal,
        onProgress: (loaded, total, percent) => {
          uploadLoaded.value = loaded
          uploadTotal.value = total
          uploadPercent.value = percent
        },
      })
      notify(`已上传「${uploaded.filename}」`, 'success')
      await deps.list.resetForNewFile()
      await deps.detail.selectFile(uploaded.id, { force: true })
    } catch (cause) {
      if (isApiFailure(cause)) {
        const { code } = cause.failure
        if (code === 'aborted') {
          notify('已取消上传', 'info')
        } else {
          uploadError.value = UPLOAD_ERRORS[code] ?? describeFailure(cause.failure)
        }
      } else {
        uploadError.value = '上传没能完成，请重试'
      }
    } finally {
      uploading.value = false
      uploadController = null
    }
  }

  function cancelUpload(): void {
    uploadController?.abort()
  }

  const progressLabel = computed(() => {
    if (uploadPercent.value !== null) return `${uploadPercent.value}%`
    if (uploadTotal.value > 0) {
      return `${formatBytes(uploadLoaded.value)} / ${formatBytes(uploadTotal.value)}`
    }
    return '正在上传'
  })

  return {
    uploading,
    uploadName,
    uploadPercent,
    uploadLoaded,
    uploadTotal,
    uploadError,
    chooseFile,
    onPicked,
    startUpload,
    cancelUpload,
    progressLabel,
  }
}
