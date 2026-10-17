/**
 * 人格列表的读取，给三个入口共用：人格管理、伙伴、协同写作。
 *
 * 三个页面看的是同一份数据，状态机也必须是同一套：首次加载画骨架、空结果
 * 画带动作的空状态、刷新失败但已有内容时保留内容只挂一条横幅。复制三份的
 * 话，迟早有一页会在刷新失败时把人已经读到的人格换成错误页。
 *
 * 页面拿到的 `items` 是可写的：管理页保存或删除之后要就地更新这一行，而不
 * 是再往返一次。
 */

import { ref } from 'vue'

import type { ApiFailure } from '../../api/client'
import { listPersonas, type Persona } from '../../api/personas'
import { toFailure } from '../../shared/resource'

export function usePersonaList() {
  const items = ref<Persona[]>([])
  const status = ref<'loading' | 'ready' | 'failed'>('loading')
  const failure = ref<ApiFailure | null>(null)

  async function load(): Promise<void> {
    if (items.value.length === 0) status.value = 'loading'
    failure.value = null
    try {
      items.value = (await listPersonas()).personas
      status.value = 'ready'
    } catch (cause) {
      failure.value = toFailure(cause)
      if (items.value.length === 0) status.value = 'failed'
    }
  }

  return { items, status, failure, load }
}
