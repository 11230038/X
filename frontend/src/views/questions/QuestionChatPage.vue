<script setup lang="ts">
/**
 * 题目问答：挂在题库标题下的对话页。
 *
 * 默认就是一段空对话：列表栏、空的正文区与输入区，和普通对话页一样。不同
 * 的只有三处：列的是 `kind=question_bank` 的会话、每一轮固定用 `deep_solve`
 * 能力、输入区的工具行里多一个「选择题目」。
 *
 * 题目从弹窗里选（`QuestionPickerDialog.vue`），选中后把题干、题型、选项、
 * 参考答案与解析写成第一句话的正文放进输入框——`deep_solve` 读不到题库，
 * 模型能看到的只有这段文字。放进输入框而不是直接发送，是为了让用户能看见
 * 发出去的是什么，并在后面补上自己的问题。
 *
 * 「知识点 → 分步示范 → 错误对照 → 同类题询问」是人格设定，不是能力的固定
 * 行为：它放在「伙伴 → 题目问答伙伴」里，这一页**锁死**这一个人格——每一轮
 * 都带上它，输入区不给切换。人格仍是账号里可编辑的一行，改了提示词这一页就
 * 按新的说法回答；账号里还没有同名人格时，按 `QUESTION_CHAT_PERSONA` 的种子
 * 内容建一个。这段结构也不再硬编码进 `deepsolve/prompt.go`。
 *
 * 从练习页的「我不会，去提问」与题库详情的「提问」过来时题号在查询串里
 * （`?question=qn_…&send=1`）：读出这一道、直接建会话并发出默认问题，不停在
 * 输入框上；`send` 在发送前从地址摘掉，失败也不会因为刷新重复建会话。
 */

import { onMounted, ref, useTemplateRef } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { getQuestion, type QuestionEntry } from '../../api/questions'
import ChatWorkspace from '../chat/ChatWorkspace.vue'
import QuestionPickerDialog from './QuestionPickerDialog.vue'
import { QUESTION_CHAT, QUESTION_CHAT_PERSONA } from '../../copy/questions'
import { typeLabel } from './labels'

const route = useRoute()
const router = useRouter()

const workspace = useTemplateRef<InstanceType<typeof ChatWorkspace>>('workspace')

const pickerOpen = ref(false)
/** 带着题号进来但没读到那道题：打开弹窗并在顶上说明一句。 */
const preselectNotice = ref(false)
/**
 * 上一次由「选择题目」写进输入框的完整正文。再选一道题时，如果输入框还是
 * 这段原文（没被改过），就把用户的问题当作默认问题；被改过就按用户改过的
 * 内容当作问题——不把上一道题的正文再嵌进「我的问题」里。
 */
let lastComposed: string | null = null

/**
 * 第一句话。题目本身要说全：模型没有题库工具，看不到题干之外的东西。
 *
 * 参考答案带上选项原文，是因为选项键（`A`）离开选项表就没有意义；填空题的
 * 答案本身就是文字，不需要这一步。
 */
function compose(entry: QuestionEntry, text: string): string {
  const question = entry.question
  const lines = [QUESTION_CHAT.composeIntro, '']

  lines.push(`【${QUESTION_CHAT.composeQuestion}】${question.text}`)
  lines.push(`【${QUESTION_CHAT.composeType}】${typeLabel(question.question_type)}`)

  if (question.options.length > 0) {
    lines.push(`【${QUESTION_CHAT.composeOptions}】`)
    for (const option of question.options) {
      lines.push(`${option.key}. ${option.text}`)
    }
  }

  if (question.correct_answer) {
    const chosen = question.options.find((option) => option.key === question.correct_answer)
    lines.push(
      `【${QUESTION_CHAT.composeAnswer}】${
        chosen ? `${question.correct_answer}（${chosen.text}）` : question.correct_answer
      }`,
    )
  }

  if (question.explanation) {
    lines.push(`【${QUESTION_CHAT.composeExplanation}】${question.explanation}`)
  }

  lines.push('', `【${QUESTION_CHAT.composeAsk}】${text}`)
  return lines.join('\n')
}

/** 选中一道题：写进输入框，等用户按发送。 */
function pick(entry: QuestionEntry): void {
  pickerOpen.value = false
  preselectNotice.value = false
  const current = workspace.value?.getComposerText().trim() ?? ''
  const edited = current !== '' && current !== (lastComposed?.trim() ?? '')
  const text = compose(entry, edited ? current : QUESTION_CHAT.defaultAsk)
  lastComposed = text
  workspace.value?.setComposerText(text)
}

/**
 * 带题号进来的入口。
 *
 * 读不到就打开弹窗并说明一句——题可能被删了，或者不属于这个账号。`send=1`
 * 时先把 `send` 从地址里摘掉再发，同名路由不重挂载；建会话失败会留在这一页，
 * 刷新也不会又建一段会话。
 */
async function preselect(): Promise<void> {
  const raw = route.query.question
  if (typeof raw !== 'string' || raw.trim() === '') return
  const questionId = raw.trim()
  const sendNow = route.query.send === '1'

  let entry: QuestionEntry
  try {
    entry = await getQuestion(questionId)
  } catch {
    preselectNotice.value = true
    pickerOpen.value = true
    return
  }

  if (!sendNow) {
    pick(entry)
    return
  }

  await router.replace({ name: 'question-chat', query: { question: questionId } })
  await workspace.value?.startSession(compose(entry, QUESTION_CHAT.defaultAsk))
}

onMounted(() => {
  void preselect()
})
</script>

<template>
  <ChatWorkspace
    ref="workspace"
    kind="question_bank"
    capability="deep_solve"
    :persona-lock="QUESTION_CHAT_PERSONA"
    :home-route="{ name: 'question-chat' }"
    session-route="question-chat"
    :home-title="QUESTION_CHAT.title"
    :show-capability="false"
    :placeholder="QUESTION_CHAT.placeholder"
    :list-title="QUESTION_CHAT.listTitle"
    :list-empty-action="QUESTION_CHAT.listEmptyAction"
    :list-new-label="QUESTION_CHAT.listNewLabel"
    :empty-title="QUESTION_CHAT.emptySession"
    :empty-hint="QUESTION_CHAT.emptySessionHint"
    :empty-action-label="QUESTION_CHAT.emptySessionAction"
  >
    <template #composer-tools>
      <button type="button" class="qpick" @click="pickerOpen = true">
        {{ QUESTION_CHAT.pickQuestion }}
      </button>
      <QuestionPickerDialog
        :open="pickerOpen"
        :notice="preselectNotice"
        @close="pickerOpen = false"
        @pick="pick"
      />
    </template>
  </ChatWorkspace>
</template>

<style scoped>
/* 与输入区里的其他工具按钮同一套样式；slot 内容拿不到 Composer 的 scoped 样式。 */
.qpick {
  display: inline-flex;
  gap: 5px;
  align-items: center;
  padding: 2px 7px;
  border: 1px solid var(--border);
  border-radius: 999px;
  background: var(--surface-sunken);
  color: var(--text-muted);
  font-size: 12px;
  cursor: pointer;
}

.qpick:hover {
  border-color: var(--accent);
  color: var(--accent);
}
</style>
