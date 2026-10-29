/**
 * 「关于 Rinnex」—— 点主侧边栏那枚品牌标志会翻到的彩蛋页。
 *
 * 这一页没有任何后端：它读的是这一份文案与图标，不查版本、不查账号（版本的
 * 真相在「设置 → 关于」）。所以文案不写版本号、不写功能承诺，只写这个产品
 * 为什么长成现在这样。
 */

import type { AppIconName } from '../components/common/AppIcon.vue'

export const RINNEX_ABOUT = {
  tagline: '你的专属一体化学习空间',
  /** 两段就够：第一段说产品，第二段说这套图标。 */
  story: [
    'Rinnex 把学着学着散落一地的对话、资料、练习与路径，重新连成一张网 —— 不替你学，只让你少在几个窗口之间来回找。',
    '这里的每一枚图标都是手画的：一笔、一线、一个颜色。像刚开始学写字时那样，慢一点，但认真。',
  ],
  /** 图标墙：一排把品牌色铺开的图标，选的都是这一版真有的模块。 */
  icons: [
    'chat',
    'space',
    'reading',
    'practice',
    'mastery',
    'memory',
    'target',
    'sparkle',
  ] as AppIconName[],
  foot: '把这份认真，留给每一个还在好奇的人。',
  back: '回到主页',
} as const
