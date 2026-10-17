/**
 * 技能与工具目录的文案。
 *
 * 这一页的数据全部来自 `/capabilities`：技能是服务端注册的能力，工具是能力
 * 声明的工具名。没有安装、启用、停用 —— 所以文案里不出现这些词，也不出现
 * 灰色或禁用的开关。
 */

export const CATALOG = {
  title: '技能与工具',
  description: '名单来自服务端的能力注册表。这里只读：不能安装、启用或停用任何一项。',
  refresh: '重新读取',
  refreshing: '正在读取…',
  refreshFailedKept: '这次没有刷新成功，下面仍是上一次的清单',
  loadFailed: '能力清单没能读出来',
  emptyTitle: '服务端没有登记任何能力',
  emptyHint: '没有能力就无法发起对话。重新读取一次；仍然为空时，问题在服务端。',
  skillsTitle: '技能',
  skillsCount: (count: number) => `${count} 项能力`,
  toolsTitle: '工具',
  toolsCount: (count: number) => `${count} 个工具`,
  toolsEmpty: '没有任何能力声明工具。',
  toolsScope: '工具由能力声明；这里按工具名反向列出调用它的能力。',
  usedBy: '调用它的能力',
  stages: '阶段',
  noStages: '这个能力没有声明阶段。',
  noTools: '这个能力不额外挂载工具。',
  stageNote: '阶段表是能力打算走的顺序；实际走到了哪一步，以对话里收到的事件为准。',
}
