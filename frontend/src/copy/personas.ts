/**
 * 人格：「伙伴」页与对话输入区共用的文案。
 *
 * 界面上这一份数据叫"伙伴"——一个伙伴就是一份保存下来的人格设定。两页已经
 * 合成一页（`views/Partners.vue`），所以这里同时有列表、编辑与删除的词。
 *
 * 这一组里最重要的一句是 `description`：人格**不改变权限**。页面把它写在
 * 标题下面，而不是藏在帮助里，因为一个叫"人格"的东西很容易被理解成"用谁
 * 的身份说话"。
 */

import { WRITING_COACH_PERSONA } from './collaboration'
import { QUESTION_CHAT_PERSONA } from './questions'

/**
 * 系统人格：锁在自己那一页上的人格，别的对话不能选。
 *
 * 它们仍然是账号里的普通人格（能改提示词），但选择器里不列——用户在一个
 * 普通对话里选到「写作教练」，得到的是一个不比默认对话更会写的角色，只会
 * 让人以为写作页坏了；「题目问答伙伴」的教学四段也一样，离开题目那一刻
 * 就没有题目可讲了。名单按名字比对，与锁定用的同一套约定（人格 id 不跨
 * 账号）。
 */
const SYSTEM_PERSONA_NAMES: ReadonlySet<string> = new Set([
  WRITING_COACH_PERSONA.name,
  QUESTION_CHAT_PERSONA.name,
])

/** 这个名字的人格只属于它自己那页，别处不提供选择。 */
export function isSystemPersona(name: string): boolean {
  return SYSTEM_PERSONA_NAMES.has(name)
}

export const PERSONAS = {
  /** 伙伴与人格是同一页：一个伙伴就是一份保存下来的人格设定。 */
  title: '伙伴',
  description:
    '一个伙伴就是一份人格设定：选一个开始对话，也能在这里新建、编辑和删除。人设只改变角色、语气和回答方式，不改变事实标准、安全边界、可用工具或权限范围；对话里也只保存说过的话，不保存人格。',
  newPersona: '新建人格',
  toChat: '去对话',

  loadFailed: '伙伴列表没能读出来',
  refreshFailedKept: '伙伴列表没有刷新成功，显示的是上次读到的内容',
  emptyTitle: '还没有伙伴',
  emptyHint: '伙伴就是人格设定。新建一个，它就会出现在这里，对话里也能选到。',
  emptyAction: '新建人格',
  count: (count: number) => `${count} 个伙伴`,
  /** 列表上方的筛选：默认全部，只筛显示，不改数据。 */
  filterLabel: '筛选',
  filterAll: '全部',
  filterSystem: '系统',
  filterUser: '非系统',
  filterEmpty: '这一栏还没有伙伴',
  updatedAt: (when: string) => `更新于 ${when}`,
  showContent: '查看提示词',
  edit: '编辑',
  remove: '删除',
  useThisPersona: '开始对话',
  /** 锁在自己那一页上的人格（写作教练）：这里不给「开始对话」出口，只标出来。 */
  systemBadge: '系统人格',
  deleting: '正在删除…',
  actionFailed: '这一步没能完成',
  dismiss: '关闭',
  retry: '重试',

  removeTitle: (name: string) => `删除「${name}」？`,
  removeBody: '这个人格会从列表里消失，之后不能再选。已经发生的对话不受影响。',
  removeNote: '这一步不能撤销。',
  removeConfirm: '删除人格',
  removed: '人格已删除',

  createTitle: '新建人格',
  editTitle: '编辑人格',
  close: '关闭',
  saved: '人格已保存',
  nameLabel: '名字',
  namePlaceholder: '例如：耐心的物理老师',
  nameHint: '最长 64 个字符；同一个账号下不能重名。',
  descriptionLabel: '说明',
  descriptionPlaceholder: '一句话说明这个人格适合什么场景（可不填）',
  descriptionHint: '最长 500 个字符。',
  contentLabel: '提示词',
  contentPlaceholder:
    '写下角色、语气和回答方式。不要写它有权做什么——权限不由人格决定。',
  contentHint: '最长 4000 个字符。',
  cancel: '取消',
  save: '保存',
  saving: '正在保存…',

  errorName: '名字不能为空，且不超过 64 个字符',
  errorDescription: '说明不能超过 500 个字符',
  errorContent: '提示词不能为空，且不超过 4000 个字符',
  errorNameTaken: '已经有一个同名的人格，换一个名字',
  errorGone: '这个人格已经不在了，关闭后刷新列表',
  errorSave: '没有保存成功，请重试',

  /* 对话输入区 */
  composerLabel: '人格',
  noPersona: '不指定人格',
  personaLoading: '正在读取…',
  personaLoadFailed: '人格列表没读出来，点这里重试',
  manage: '管理人格',
} as const
