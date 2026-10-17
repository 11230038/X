/**
 * 人格：管理页与对话输入区共用的文案。
 *
 * 这一组里最重要的一句是 `description`：人格**不改变权限**。页面把它写在
 * 标题下面，而不是藏在帮助里，因为一个叫"人格"的东西很容易被理解成"用谁
 * 的身份说话"。
 */

export const PERSONAS = {
  title: '人格',
  description:
    '人格是你自己的提示词，决定 AI 用什么角色、语气和回答方式。它不改变事实标准、安全边界、可用工具或权限范围；对话里也只保存说过的话，不保存人格。',
  newPersona: '新建人格',
  toChat: '去对话',

  loadFailed: '人格列表没能读出来',
  refreshFailedKept: '人格列表没有刷新成功，显示的是上次读到的内容',
  emptyTitle: '还没有人格',
  emptyHint: '新建一个，之后每一轮对话都可以在输入框上方选择它。',
  emptyAction: '新建人格',
  count: (count: number) => `${count} 个人格`,
  updatedAt: (when: string) => `更新于 ${when}`,
  showContent: '查看提示词',
  edit: '编辑',
  remove: '删除',
  useThisPersona: '用这个人格对话',
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
