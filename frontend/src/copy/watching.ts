/** 视频学习的文案。搜索与播放都在这一页里。 */

export const WATCHING = {
  title: '视频学习',
  description: '输入关键词搜索 B 站视频，选中后在当前页观看。',

  searchPlaceholder: '搜索学习视频',
  searchAction: '搜索',
  searching: '正在搜索…',

  idleTitle: '输入关键词开始',
  idleHint: '结果来自 B 站，播放使用官方播放器。',

  emptyTitle: '没有搜到相关视频',
  emptyHint: '换一个更具体的关键词再试。',
  emptyAction: '换个关键词',

  loadFailedTitle: '视频搜索没有完成',
  partialFailed: '这次搜索没有完成，下面还是上一次的结果。',

  loadMore: '加载更多',
  playerEmpty: '从左侧选一个视频',
  watchOnSite: '在 B 站打开',
} as const
