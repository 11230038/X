<script setup lang="ts">
/**
 * 「关于 Rinnex」—— 彩蛋页。
 *
 * 入口只有一个：主图标栏顶上那枚品牌标志（`PrimaryNav`）。所以它没有进导航、
 * 没有侧栏、也没有面包屑 —— 进到这一页时整个应用外壳都让开，像合上笔记本
 * 之后翻到扉页。
 *
 * 这一页不读后端：文案在 `copy/about.ts`，图标是 `AppIcon` 里同一套手绘的
 * 那一排。版本号不在这里写（那是「设置 → 关于」的事），这里只写这个产品为
 * 什么长成现在这样，以及怎么回去。
 */

import { RouterLink } from 'vue-router'

import AppIcon from '../../components/common/AppIcon.vue'
import logo from '../../assets/logo-rinnex-color.png'
import { RINNEX_ABOUT } from '../../copy/about'
</script>

<template>
  <div class="about">
    <main class="about__card">
      <!-- 回到主页是这一页唯一真实的动作；它不是彩蛋的一部分，放在角落里。 -->
      <RouterLink class="about__back" :to="{ name: 'learning' }">
        <span aria-hidden="true">←</span>
        {{ RINNEX_ABOUT.back }}
      </RouterLink>

      <img class="about__logo" :src="logo" alt="Rinnex" />
      <h1 class="brand-script about__wordmark">Rinnex</h1>
      <p class="brand-script about__tagline">{{ RINNEX_ABOUT.tagline }}</p>

      <div class="about__rule" aria-hidden="true"></div>

      <div class="about__story">
        <p v-for="(line, index) in RINNEX_ABOUT.story" :key="index">{{ line }}</p>
      </div>

      <!-- 图标墙：把这一版的手绘图标排一排，纯装饰，读屏不念。 -->
      <ul class="about__icons" aria-hidden="true">
        <li v-for="name in RINNEX_ABOUT.icons" :key="name">
          <AppIcon :name="name" />
        </li>
      </ul>

      <p class="about__foot">{{ RINNEX_ABOUT.foot }}</p>
    </main>
  </div>
</template>

<style scoped>
/* 与登录页同一个底：极光一样的粉紫两团，卡片浮在上面。 */
.about {
  display: grid;
  place-items: center;
  height: 100vh;
  background:
    radial-gradient(circle at 12% 8%, rgb(247 169 239 / 32%) 0%, transparent 52%),
    radial-gradient(circle at 88% 92%, rgb(177 140 255 / 22%) 0%, transparent 52%);
}

.about__card {
  position: relative;
  display: flex;
  flex-direction: column;
  align-items: center;
  width: 760px;
  padding: 56px 72px 44px;
  border: 1px solid var(--glass-border);
  border-radius: 36px;
  background: var(--glass-bg);
  box-shadow: 0 32px 80px -20px rgb(51 68 85 / 18%), var(--inset-light);
  text-align: center;
  backdrop-filter: blur(30px);
  -webkit-backdrop-filter: blur(30px);
}

.about__back {
  position: absolute;
  top: 24px;
  left: 28px;
  display: inline-flex;
  gap: 6px;
  align-items: center;
  padding: 8px 14px;
  border-radius: 999px;
  color: var(--text-muted);
  font-size: 14px;
  font-weight: 600;
  text-decoration: none;
  transition: all 0.3s cubic-bezier(0.25, 0.8, 0.25, 1);
}

.about__back:hover {
  background: rgb(255 255 255 / 70%);
  color: var(--text);
}

/* 品牌标志：与登录页同一张手绘稿，这里再大一点，当扉页的题图。 */
.about__logo {
  width: 220px;
  height: auto;
  margin-top: 16px;
  filter: drop-shadow(0 12px 24px rgb(51 68 85 / 14%));
}

/* 花体字标与副题（字体、渐变在全局 `.brand-script`）。行楷没有粗体字面，按
   常规字重写，笔锋才不被合成粗体糊掉。 */
.about__wordmark {
  margin: 6px 0 0;
  font-size: 56px;
  font-weight: 400;
  line-height: 1.1;
  letter-spacing: 0.01em;
}

.about__tagline {
  width: fit-content;
  margin: 10px auto 0;
  font-size: 22px;
  font-weight: 400;
}

.about__rule {
  width: 120px;
  height: 2px;
  margin: 30px auto 26px;
  border-radius: 2px;
  background: linear-gradient(90deg, #e05abe, #7a54e2, #2496d6);
  opacity: 0.55;
}

.about__story {
  display: flex;
  flex-direction: column;
  gap: 14px;
  width: 100%;
  max-width: 560px;
  margin: 0 auto;
}

.about__story p {
  margin: 0;
  color: var(--text-muted);
  font-size: 16px;
  line-height: 1.95;
  text-align: left;
}

.about__icons {
  display: flex;
  gap: 26px;
  align-items: center;
  justify-content: center;
  padding: 0;
  margin: 34px 0 0;
  list-style: none;
}

.about__icons li {
  display: flex;
  --icon-size: 26px;
  opacity: 0.85;
  transition: transform 0.4s cubic-bezier(0.34, 1.56, 0.64, 1), opacity 0.3s ease;
}

.about__icons li:nth-child(even) {
  transform: translateY(3px);
}

.about__icons li:hover {
  opacity: 1;
  transform: translateY(-4px);
}

.about__foot {
  margin: 34px 0 0;
  color: var(--text-faint);
  font-size: 14px;
  letter-spacing: 0.02em;
}
</style>
