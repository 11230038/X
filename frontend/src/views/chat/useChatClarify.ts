/**
 * 澄清提问的弹窗状态。
 *
 * 一次回合里可能有不止一个 `wait_for_input`；弹窗要的是**当前**那一个，所以
 * 从事件列表末尾往前找。问题可能比 `waiting` 晚一帧到（重连重放时会这样），
 * 所以空串是一个合法状态，弹窗自己会等它。
 */

import { computed, type Ref, type ShallowRef } from 'vue'

import type ClarifyDialog from '../../components/chat/ClarifyDialog.vue'
import type { Block } from '../../ws/blocks'

export function useChatClarify(
  blocks: Ref<readonly Block[]>,
  dialog: Readonly<ShallowRef<InstanceType<typeof ClarifyDialog> | null>>,
) {

  const question = computed(() => {
    for (let index = blocks.value.length - 1; index >= 0; index -= 1) {
      const block = blocks.value[index]
      if (block.kind === 'question') return block.text
    }
    return ''
  })

  /** 用户关掉弹窗之后，从状态条再把它叫回来。 */
  function reopen(): void {
    dialog.value?.show()
  }

  return { question, reopen }
}
