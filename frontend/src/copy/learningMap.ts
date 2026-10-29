/**
 * 学习地图的文案。
 *
 * 四态与下一步的说法直接取掌握路径那份，避免同一个状态在两张页面上叫两个
 * 名字。这里只加地图自己的几句。
 */

import { MASTERY } from './mastery'

export const LEVEL_ORDER = ['unknown', 'weak', 'learning', 'mastered'] as const

export const LEVEL_LABELS = {
  unknown: MASTERY.levelUnknown,
  weak: MASTERY.levelWeak,
  learning: MASTERY.levelLearning,
  mastered: MASTERY.levelMastered,
} as const

export const STEP_LABELS = {
  map: MASTERY.nextMap,
  learn: MASTERY.nextLearn,
  review: MASTERY.nextReview,
  done: MASTERY.nextDone,
} as const

export const LEARNING_MAP = {
  title: '学习地图',
  description: '按模块看一条掌握路径上的知识点四态、观察统计与下一步。',
  /** 一级页（选路径）的说明。二级页沿用路径自己的说明。 */
  indexDescription: '选一条路径，看它的知识点按模块铺开。',
  chooseAnother: '换一条路径',

  legend: '图例',
  module: '模块',
  pointEmpty: '选一个知识点，这里会显示它的观察统计与下一步。',
  backToPath: '回到路径',

  summary: (
    total: number,
    mastered: number,
    learning: number,
    weak: number,
    unknown: number,
  ) => `共 ${total} 个知识点：已掌握 ${mastered}，学习中 ${learning}，薄弱 ${weak}，未接触 ${unknown}。`,
} as const
