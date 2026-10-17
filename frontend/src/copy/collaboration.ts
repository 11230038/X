/**
 * 伙伴与协同写作。
 *
 * 两块都不是新的数据：它们读的是同一份人格，只是换了一个使用场景。文案里
 * 必须把这件事说清楚，否则"伙伴"听起来像另一个账号，"协同写作"听起来像
 * 有一份文稿存在某处。
 */

export const PARTNERS = {
  title: '伙伴',
  description:
    '在这里，一个伙伴就是一份保存下来的人格设定：选一个开始对话。人设只改变角色、语气和回答方式，不改变事实标准、安全边界、可用工具或权限范围。编辑与新建都在「人格」里。',
  manage: '管理人格',
  start: '开始对话',
  showPrompt: '查看设定',
  count: (count: number) => `${count} 个伙伴`,
  loading: '正在读取伙伴…',
  loadFailed: '伙伴列表没能读出来',
  refreshFailedKept: '伙伴列表没有刷新成功，显示的是上次读到的内容',
  emptyTitle: '还没有伙伴',
  emptyHint: '伙伴就是人格设定。新建一个，它就会出现在这里。',
  emptyAction: '去新建人格',
  removeNote: '删除只能去「人格」里做，因为那会改变数据，不只是换一个看法。',
} as const

export const CO_WRITER = {
  title: '协同写作',
  description:
    '选一个伙伴和写作步骤，这里会把第一句话带进对话。真正的写作在对话里进行；这一版没有独立的文稿存储，写下来的内容就是对话消息。',
  stepTitle: '写作步骤',
  personaTitle: '选一个伙伴',
  noPersona: '不指定伙伴',
  personaLoading: '正在读取…',
  personaLoadFailed: '伙伴列表没读出来，点这里重试',
  personaEmpty: '还没有伙伴，可以先用不指定伙伴开始；想固定一种写法，就去新建一个人格。',
  manage: '管理人格',
  contentLabel: '这次写什么',
  contentPlaceholder: '写一句主题，越具体越好',
  contentPlaceholderRevise: '把要改的文字贴在这里',
  start: '去对话开始',
  emptyContent: '先写一句主题，或者贴上要改的文字',
  backToPartners: '看看伙伴',

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
