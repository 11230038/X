/**
 * 设置下技能与工具两块的文案。
 *
 * 数据全部来自 `/capabilities`：技能是服务端注册的能力，工具是能力声明的
 * 工具名。没有安装、启用、停用 —— 所以文案里不出现这些词，也不出现灰色或
 * 禁用的开关。
 */

export const CATALOG = {
  refresh: '重新读取',
  refreshing: '正在读取…',
  refreshFailedKept: '这次没有刷新成功，下面仍是上一次的清单',
  loadFailed: '能力清单没能读出来',
  emptyTitle: '服务端没有登记任何能力',
  emptyHint: '没有能力就无法发起对话。重新读取一次；仍然为空时，问题在服务端。',
  skillsTitle: '技能',
  skillsDescription: '服务端注册的能力清单；每一项写着它打算走的阶段与会用到的工具。',
  skillsCount: (count: number) => `${count} 项能力`,
  toolsTitle: '工具',
  toolsDescription:
    '工具由能力声明，这里按工具名反向列出调用它的能力。描述来自工具自己的定义，与模型看到的是同一句。',
  toolsCount: (count: number) => `${count} 个工具`,
  toolsEmpty: '没有任何能力声明工具。',
  usedBy: '调用它的能力',
  stages: '阶段',
  noStages: '这个能力没有声明阶段。',
  noTools: '这个能力不额外挂载工具。',
  stageNote: '阶段表是能力打算走的顺序；实际走到了哪一步，以对话里收到的事件为准。',
}
