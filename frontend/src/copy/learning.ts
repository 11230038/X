/** 学习首页的文案。 */

export const LEARNING = {
  /** 顶栏。用户给的两句原话，不另加问候或按钮。 */
  homeTitle: '今天，你想探索点什么？',
  homeSubtitle: '在这里，所有的知识都为你无缝连接。',

  /**
   * 五块砖都有出处：路径完成率是第一条掌握路径自己的已掌握 / 总数；快照三行
   * 分别是本周练习统计、证据回放出来的掌握知识点数、在线心跳的连续天数；
   * 本周任务进度两行见下面 `weekTasks`；今日时长来自心跳。读不到的格子画横线。
   */
  pathCompletion: '路径完成率',
  pathSubtitle: (goal: string) => `${goal} · 掌握路径`,
  noPath: '还没有掌握路径',

  weekSnapshot: '本周学习快照',
  accuracy: '练习正确率',
  weekMastered: '本周掌握知识点',
  points: '个',
  streak: '连续打卡',
  days: '天',
  snapshotNote: '正确率按本周判过的题计算，还没有练习时画横线。',

  moduleProgress: '当前模块进度',
  learningNow: (title: string) => `正在学习：${title}`,
  moduleNote: '这条路径还没有可以显示进度的模块。',

  /*
   * 本周任务进度两根条，两个都是真的：
   * - 掌握路径模块：`GET /mastery/paths/:id/progress` 给本周完成的模块数 /
   *   模块总数（模块里每个知识点都 mastered 才算完成）；
   * - 复习错题集：`GET /practice/mistakes` 给本周复习过的错题数 / 错题集
   *   现在的题数。
   * 演示稿里这条叫「完成课程任务」，后端没有"课程"这个概念，换成了路径模块。
   */
  weekTasks: '本周任务进度',
  taskModules: '掌握路径模块',
  taskReview: '复习错题集',

  todayTime: '今日学习时长',
  minutes: 'min',
  /**
   * 演示稿里这句是「比昨天多学了 15 分钟」。现在这比较有出处了：昨天也是心跳
   * 记出来的分钟数，所以照实写多、少、持平三种。
   */
  timeHint: (today: number, yesterday: number): string => {
    if (today === 0) return '今天还没有学习记录。'
    const diff = today - yesterday
    if (diff > 0) return `比昨天多学了 ${diff} 分钟`
    if (diff < 0) return `比昨天少学了 ${-diff} 分钟`
    return '和昨天学了一样久'
  },

  loadFailed: '学习首页没能读出来',
} as const
