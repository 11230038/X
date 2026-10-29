/**
 * 协同写作。
 *
 * 它读的仍是人格，只是换了一个使用场景：文案里必须把这件事说清楚，否则
 * "协同写作"听起来像有一份文稿存在某处。（「伙伴」那一页的文案在
 * `copy/personas.ts`：伙伴就是人格设定，两页已经合成一页。）
 */

export const CO_WRITER = {
  title: '协同写作',
  description:
    '选一个写作步骤，就在这一页开始写。写作的来回只留在写作列表里，不会混进对话历史；这一版没有独立的文稿存储，写下来的内容就是对话消息。',
  stepTitle: '写作步骤',
  contentLabel: '这次写什么',
  contentPlaceholder: '写一句主题，越具体越好',
  contentPlaceholderRevise: '把要改的文字贴在这里',
  start: '开始写作',
  starting: '正在开始…',
  startFailed: '这次写作没能开始，请重试',
  /** 写作会话里的退出动作：只回到准备页，不删除这段历史。 */
  endWriting: '结束写作',
  emptyContent: '先写一句主题，或者贴上要改的文字',

  /** 左侧列表是写作自己的历史，措辞不能沿用对话页那套。 */
  listTitle: '写作',
  listEmptyAction: '开始一段写作',
  listNewLabel: '新建写作',
  composerPlaceholder: '接着写，Enter 发送，Shift+Enter 换行',
  emptySession: '这段写作还没有消息',
  emptySessionHint: '输入一句话接着写，或者回到写作首页重新开始。',
  emptySessionAction: '回到写作首页',

  steps: {
    free: {
      label: '自由写作',
      description: '从一个主题开始，边聊边写。',
    },
    outline: {
      label: '搭提纲',
      description: '先问清读者与目的，再给结构。',
    },
    draft: {
      label: '写初稿',
      description: '确认长度与语气，一段一段写。',
    },
    revise: {
      label: '修改润色',
      description: '贴一段原文，先指出问题再给修改版。',
    },
  },
} as const

export type WritingStep = keyof typeof CO_WRITER.steps

/**
 * 写作页锁死的人格：写作教练。
 *
 * 与题目问答的分法一样：这一页不在输入区提供人格选择，每一轮都用它；人格仍
 * 是账号里的一行数据，可以在「伙伴」里改提示词，这里只是一份**种子**——账号
 * 里还没有同名人格时，写作页按这份内容建一个。名字也列进了
 * `copy/personas.ts` 的系统人格名单，别的对话的选择器不列它。
 *
 * 按名字找而不是按 id：人格是每个账号各建一份的数据，id 不跨账号。
 */
export const WRITING_COACH_PERSONA = {
  name: '写作教练',
  description: '写作：先问清读者、目的与长度，再按步骤陪你搭提纲、写初稿或改稿。',
  content: `你是一位写作教练。用户带着一个主题，或者一段要改的文字来；你的任务是陪他把这次写作做完，而不是替他一次写完。

规则：
1. 先问清读者是谁、写作目的是什么、大概多长，再动笔。用户已经说过的不要重复问。
2. 按写作步骤推进。搭提纲时，每一节写清它要回答的问题；写初稿时一段一段来，每段之后停下来等用户说继续或调整；修改时先指出主要问题（结构、语气、冗余），再给修改版，并说明改了哪些地方。
3. 不一次给出完整长文，不替用户决定他没有说过的立场、事实与数据。
4. 保持用户原有的意思；删改要说出理由。
5. 用与用户相同的语言写，不要开场白和收尾寒暄。
6. 不确定的事实、数字与引用不要编造，直说不知道。`,
} as const
