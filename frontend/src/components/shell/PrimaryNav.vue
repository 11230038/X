<script setup lang="ts">
/**
 * 一级导航。照演示稿的 `.nav` 逐项实现：**固定 72px 的玻璃图标栏**，顶部
 * 品牌标志，中间一组 48×48 的圆角图标块，底部一组。没有文字标签、没有悬停
 * 浮出、没有二级入口 —— 二级入口全部在 240px 的侧栏里（`SectionSidebar`）。
 *
 * 顶部那枚标志是一个彩蛋入口：点它翻到「关于 Rinnex」（`about-rinnex`）——
 * 一整屏的扉页，不是回到主页。回主页走下面的房子图标。
 *
 * 演示稿的四个主入口是主页、伴学、学习空间、掌握路径，底部是设置与个人中心。
 * 这一版保留同样的位置，图标换成与品牌标志同一路笔触的渐变单线图标
 * （`AppIcon`）；名称放在 `title` 与
 * `aria-label` 上，悬停时仍然读得到。
 *
 * 底部一组是「设置」与头像：齿轮进设置（`/settings`），头像进**个人中心**
 * （`/profile`）—— 资料概览、账号与安全（改密、退出登录）都在那里。记忆、
 * 伙伴以及技能与工具都从设置侧栏进（`sections.ts` 的 `settingsSection()`）；
 * 管理员可见的用户管理与日志统计挂在个人中心那一列。
 */

import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'

import { currentAccount } from '../../session'
import logo from '../../assets/logo-rinnex-color.png'
import AppIcon, { type AppIconName } from '../common/AppIcon.vue'

interface NavEntry {
  key: string
  label: string
  icon: AppIconName
  to: string
}

/** 演示稿 `.nav-menu` 里的四项。顺序不变。 */
const topEntries: readonly NavEntry[] = [
  { key: 'learning', label: '主页', icon: 'home', to: '/learning' },
  { key: 'chat', label: '伴学', icon: 'chat', to: '/chat' },
  { key: 'space', label: '学习空间', icon: 'space', to: '/books' },
  { key: 'mastery', label: '掌握路径', icon: 'mastery', to: '/mastery' },
]

const route = useRoute()

const username = computed(() => currentAccount.value?.username ?? '')

/**
 * 入口是否高亮。按路由名或路径前缀判断，不依赖 `RouterLink` 自带的
 * `active-class`：一个入口覆盖哪些页面由这张表决定。
 */
function isActive(entry: NavEntry): boolean {
  const name = String(route.name ?? '')
  switch (entry.key) {
    case 'learning':
      return name === 'learning'
    case 'chat':
      return (
        name === 'chat' ||
        name === 'conversation' ||
        name === 'chat-recycle-bin' ||
        name === 'co-writer'
      )
    case 'space':
      return (
        name === 'books' ||
        name === 'book' ||
        name === 'knowledge-bases' ||
        name === 'knowledge-base' ||
        name === 'reading' ||
        name === 'files' ||
        name === 'questions' ||
        name === 'question-chat' ||
        name === 'practice' ||
        name === 'watching'
      )
    case 'mastery':
      return (
        name === 'mastery' ||
        name === 'mastery-conversations' ||
        name === 'mastery-chat' ||
        name === 'learning-map'
      )
    default:
      return false
  }
}

/** 「设置」覆盖整个 `/settings` 子树。 */
const settingsActive = computed(() => route.path.startsWith('/settings'))

/** 头像覆盖个人中心的两页（资料与账号安全）。 */
const profileActive = computed(() => route.path.startsWith('/profile'))
</script>

<template>
  <nav class="nav" aria-label="一级导航">
    <RouterLink class="nav__logo" :to="{ name: 'about-rinnex' }" aria-label="关于 Rinnex">
      <img :src="logo" alt="" aria-hidden="true" />
      <span class="brand-script nav__wordmark" aria-hidden="true">Rinnex</span>
    </RouterLink>

    <div class="nav__menu">
      <RouterLink
        v-for="entry in topEntries"
        :key="entry.key"
        class="nav__item"
        :class="{ 'nav__item--active': isActive(entry) }"
        :to="entry.to"
        :title="entry.label"
        :aria-label="entry.label"
      >
        <AppIcon :name="entry.icon" />
      </RouterLink>
    </div>

    <div class="nav__bottom">
      <RouterLink
        class="nav__item"
        :class="{ 'nav__item--active': settingsActive }"
        to="/settings"
        title="设置"
        aria-label="设置"
      >
        <AppIcon name="settings" />
      </RouterLink>

      <RouterLink
        class="nav__item"
        :class="{ 'nav__item--active': profileActive }"
        to="/profile"
        title="个人中心"
        aria-label="个人中心"
      >
        <span v-if="username !== ''" class="nav__avatar" aria-hidden="true">
          {{ username.slice(0, 1).toUpperCase() }}
        </span>
        <AppIcon v-else name="user" />
      </RouterLink>
    </div>
  </nav>
</template>

<style scoped>
/* 演示稿 `.nav`：72px 宽，玻璃卡，纵向居中，上下两组图标。 */
.nav {
  position: relative;
  z-index: 10;
  display: flex;
  flex-direction: column;
  align-items: center;
  width: var(--nav-width);
  height: 100%;
  padding: 24px 0;
  border: 1px solid var(--glass-border);
  border-radius: var(--radius-card);
  background: var(--glass-bg);
  box-shadow: var(--glass-shadow), var(--inset-light);
  backdrop-filter: blur(24px);
  -webkit-backdrop-filter: blur(24px);
}

.nav__logo {
  display: flex;
  flex: none;
  flex-direction: column;
  gap: 4px;
  align-items: center;
  width: 60px;
  margin-bottom: 18px;
  /* 链接默认的下划线会落在字标下面：字标是 background-clip: text 上色的，
     那条线会跟着渐变一起画出来，像一条多余的横杠。整块品牌都不画线。 */
  text-decoration: none;
}

/* 品牌标志用的是 `.product/Rinnex logo 初版方案.pdf` 里那张手绘稿，只裁掉
   四周的纸白：无穷符号、右侧的 R、星点与圆环都在。它本身是原图 2.34:1 的
   横构图，宽多高少，所以高度跟着宽度走，别设成方框里 40×20 那种比例。
   宽度取 60px，与下面 60×60 的图标块同宽；横构图下高约 26px，和 30px 的
   单线图标差不多高，一排看下来是一套尺寸。 */
.nav__logo img {
  width: 60px;
  height: auto;
}

/* 字标本身（字体、渐变）在全局 `.brand-script` 里，这里只管导航栏这一档
   字号：60px 宽标志下面 18px，和下方 30px 的图标同看是一套尺度。 */
.nav__wordmark {
  font-size: 18px;
  line-height: 1.05;
  white-space: nowrap;
}

.nav__menu {
  display: flex;
  flex-direction: column;
  gap: 16px;
  align-items: center;
  width: 100%;
}

/* 60×60 / 圆角 14 / 30px 图标，与下面二级侧栏收起时的图标块同尺寸。 */
.nav__item {
  display: flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 60px;
  height: 60px;
  border: 0;
  border-radius: 14px;
  background: transparent;
  color: var(--text-faint);
  --icon-size: 30px;
  text-decoration: none;
  cursor: pointer;
  transition: all 0.4s cubic-bezier(0.34, 1.56, 0.64, 1);
}

.nav__item:hover {
  background: rgb(255 255 255 / 60%);
  transform: translateY(-2px) scale(1.05);
}

.nav__item--active {
  background: #fff;
  box-shadow: 0 4px 16px rgb(51 68 85 / 8%);
}

.nav__bottom {
  display: flex;
  flex-direction: column;
  gap: 16px;
  align-items: center;
  margin-top: auto;
}

.nav__avatar {
  display: grid;
  place-items: center;
  width: 28px;
  height: 28px;
  border-radius: 50%;
  background: linear-gradient(135deg, #e05abe, #7a54e2);
  color: var(--text);
  font-size: 13px;
  font-weight: 700;
}

</style>
