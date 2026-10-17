/** 学习首页的文案。 */

export const LEARNING = {
  title: '学习首页',
  description: '继续上次的对话、查看掌握路径、处理待复习的内容，并按理由选择下一步。',

  sessions: '继续对话',
  sessionsEmpty: '还没有对话',
  sessionsAction: '开始一段对话',
  paths: '掌握路径',
  pathsEmpty: '还没有掌握路径',
  pathsHint: '掌握路径由一次对话生成，不是在这里新建的。',
  pathsAction: '去掌握路径看看',
  reviews: '待复习',
  reviewsEmpty: '现在没有到期的复习',
  reviewsAction: '去练习',
  next: '下一步',

  nextLearn: '去学习',
  nextReview: '去复习',
  nextDone: '这一部分已经完成',
  nextEvidence: (count: number) => `证据 ${count} 条`,
  nextEvidenceHint: '证据在掌握路径里按知识点列出。',
  nextEmpty: '暂时没有建议',
  nextEmptyHint: '先和自己聊一段，或者挑一条掌握路径看看。',

  updatedAt: '更新于',
  loadFailed: '学习首页没能读出来',
  sectionFailed: '这一段没有读出来',
  retry: '重试',
} as const
