<script setup lang="ts">
/**
 * 个人中心的子页：账号与安全。
 *
 * 账号信息、改密与退出登录都在这里。这一页原是设置里的"账号"（`/settings/
 * account`），那头像改成进个人中心之后，账号也一起从设置搬到个人中心下面；
 * 设置侧栏不再有"账号"这一项。
 *
 * 改密放在弹窗里（`PasswordChangeDialog`）：这一页只留一张说明卡和一个按钮，
 * 三个密码框不进正文档。改密成功后后端会吊销**包括当前 token 在内**的全部
 * 凭据（204），所以这里按"主动登出"处理：清本地登录态、清权限与能力缓存、
 * 回登录页。不是"改完继续用"。退出登录同样是**全设备**下线（后端写
 * `token_valid_after`），所以按钮旁要说明这一点；服务端没答也照样清本地，
 * 留着 token 只会让下一个请求再吃一次 401。
 *
 * 没有邮箱、没有找回密码 —— 这个版本没有对应接口，页面不画假入口。
 */

import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

import { logout, me, type Account } from '../../api/auth'
import type { ApiFailure } from '../../api/client'
import PartialBanner from '../../components/common/PartialBanner.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import AppShell from '../../components/shell/AppShell.vue'
import PageHeader from '../../components/shell/PageHeader.vue'
import { PROFILE } from '../../copy/profile'
import { SETTINGS } from '../../copy/settings'
import {
  permissionSnapshot,
  refreshPermissions,
  resetPermissions,
} from '../../auth/permissions'
import { clearSession } from '../../session'
import { resetCapabilities } from '../../shared/capabilities'
import { confirmAction } from '../../shared/confirm'
import { toFailure } from '../../shared/resource'
import { notify } from '../../shared/toast'
import PasswordChangeDialog from './PasswordChangeDialog.vue'
import AppIcon from '../../components/common/AppIcon.vue'

const router = useRouter()

const account = ref<Account | null>(null)
const loading = ref(true)
const loadFailure = ref<ApiFailure | null>(null)

const signingOut = ref(false)
const passwordDialog = ref<InstanceType<typeof PasswordChangeDialog> | null>(null)

const preset = computed(() => permissionSnapshot.value?.preset ?? '')

/** 头像上的那个字：用户名首字母，与个人中心、主图标栏同一套做法。 */
const initial = computed(() => (account.value?.username ?? '').slice(0, 1).toUpperCase())

/** 角色说人话：`admin` / `user` 两个内部值不直接摆到页面上。 */
const ROLE_LABELS: Record<string, string> = {
  admin: PROFILE.roleAdmin,
  user: PROFILE.roleUser,
}

const roleLabel = computed(() => {
  const role = account.value?.role ?? ''
  return role === '' ? '' : (ROLE_LABELS[role] ?? role)
})

async function load(): Promise<void> {
  loading.value = true
  loadFailure.value = null
  const [meResult] = await Promise.allSettled([me(), refreshPermissions()])
  if (meResult.status === 'fulfilled') {
    account.value = meResult.value
  } else {
    loadFailure.value = toFailure(meResult.reason)
  }
  loading.value = false
}

/**
 * 改密成功。服务端已经吊销了全部 token（含这一张），所以本地不再发任何请求，
 * 直接清登录态回登录页。
 */
async function afterPasswordChanged(): Promise<void> {
  clearSession()
  resetPermissions()
  resetCapabilities()
  notify(SETTINGS.passwordChanged, 'success')
  await router.push({ name: 'login' })
}

/**
 * 退出登录。与改密同一个落点、同一条理由：后端写 `token_valid_after`，所有
 * 设备一起下线。服务端没答也照清本地 —— 留着 token 只会让下一个请求再吃
 * 一次 401，而本地清干净之后那张 token 本来也用不上了。
 */
async function signOut(): Promise<void> {
  if (signingOut.value) return
  // 全设备下线、且没有"撤销"可言，所以先确认；影响范围写清楚再让人点。
  const confirmed = await confirmAction({
    title: SETTINGS.signOut,
    body: SETTINGS.signOutNote,
    note: PROFILE.signOutConfirmNote,
    confirmLabel: PROFILE.signOutAction,
    danger: true,
  })
  if (!confirmed) return
  signingOut.value = true
  try {
    await logout()
  } catch {
    // 服务端是否确认过不影响本地登出。
  } finally {
    signingOut.value = false
    clearSession()
    resetPermissions()
    resetCapabilities()
    await router.push({ name: 'login' })
  }
}

onMounted(() => {
  void load()
})
</script>

<template>
  <AppShell>
    <div class="page">
      <div class="page__inner">
        <PageHeader
          :title="PROFILE.accountSecurityTitle"
          :description="PROFILE.accountSecurityDescription"
        />

        <div class="account">
          <SkeletonBlock v-if="loading" :lines="4" :line-height="26" />

          <template v-else>
            <PartialBanner
              v-if="loadFailure"
              :failure="loadFailure"
              text="账号信息没能从服务端确认，下面显示的是本机保存的那一份"
              @retry="load"
            />

            <!-- 身份：头像、名字、角色，右边是两枚小事实。 -->
            <section class="hero">
              <div class="hero__avatar" aria-hidden="true">{{ initial || '—' }}</div>
              <div class="hero__main">
                <div class="hero__name-row">
                  <h2 class="hero__name">{{ account?.username ?? '—' }}</h2>
                  <span v-if="roleLabel" class="hero__badge">{{ roleLabel }}</span>
                </div>
                <p class="hero__id">
                  {{ PROFILE.accountIdLine(account?.user_id ?? '—') }}
                </p>
                <p class="hero__note">{{ PROFILE.accountNoEmail }}</p>
              </div>
              <dl class="hero__facts">
                <div class="fact">
                  <dt>{{ SETTINGS.username }}</dt>
                  <dd>{{ account?.username ?? '—' }}</dd>
                </div>
                <div class="fact">
                  <dt>{{ PROFILE.accountPreset }}</dt>
                  <dd>{{ preset || '—' }}</dd>
                </div>
              </dl>
            </section>

            <!-- 两块动作卡上下排：改密在上（不是破坏性动作），退出在下。 -->
            <div class="panels">
              <section class="card">
                <div class="card__head">
                  <span class="card__icon" aria-hidden="true"><AppIcon name="shield" /></span>
                  <h2 class="card__title">{{ SETTINGS.changePassword }}</h2>
                </div>
                <p class="hint">{{ PROFILE.changePasswordNote }}</p>
                <div class="actions">
                  <button type="button" class="primary" @click="passwordDialog?.show()">
                    {{ SETTINGS.changePassword }}
                  </button>
                </div>
              </section>

              <section class="card card--danger">
                <div class="card__head">
                  <span class="card__icon card__icon--danger" aria-hidden="true"><AppIcon name="exit" /></span>
                  <h2 class="card__title">{{ SETTINGS.signOut }}</h2>
                </div>
                <p class="hint">{{ SETTINGS.signOutNote }}</p>
                <div class="actions">
                  <button type="button" class="signout" :disabled="signingOut" @click="signOut">
                    {{ signingOut ? PROFILE.signOutBusy : PROFILE.signOutAction }}
                  </button>
                </div>
              </section>
            </div>

            <PasswordChangeDialog ref="passwordDialog" @done="afterPasswordChanged" />
          </template>
        </div>
      </div>
    </div>
  </AppShell>
</template>

<style scoped>
/* 版式照个人中心那一页：上面一张渐变身份卡，下面两张玻璃卡上下排。 */
.account {
  display: flex;
  flex-direction: column;
  gap: 24px;
}

/* ── 身份卡 ───────────────────────────────────────────────────────────── */

.hero {
  position: relative;
  display: flex;
  gap: 28px;
  align-items: center;
  padding: 32px 36px;
  overflow: hidden;
  border: 1px solid var(--glass-border);
  border-radius: 28px;
  background: linear-gradient(135deg, #f3f0ff, #e9f9ff);
  box-shadow: var(--glass-shadow);
}

/* 右上角那块高光，与个人中心的资料卡同一处装饰。 */
.hero::before {
  content: '';
  position: absolute;
  top: -60px;
  right: -60px;
  width: 220px;
  height: 220px;
  border-radius: 50%;
  background: radial-gradient(circle, rgb(255 255 255 / 85%) 0%, transparent 70%);
}

.hero__avatar {
  position: relative;
  z-index: 1;
  display: grid;
  flex: none;
  place-items: center;
  width: 76px;
  height: 76px;
  border: 4px solid #fff;
  border-radius: 50%;
  background: linear-gradient(135deg, var(--color-yellow), var(--color-pink));
  box-shadow: 0 8px 20px rgb(0 0 0 / 8%);
  color: var(--text);
  font-size: 30px;
  font-weight: 800;
  line-height: 1;
}

.hero__main {
  position: relative;
  z-index: 1;
  flex: 1;
  min-width: 0;
}

.hero__name-row {
  display: flex;
  gap: 10px;
  align-items: center;
  margin-bottom: 6px;
}

.hero__name {
  margin: 0;
  color: var(--text);
  font-size: 26px;
  font-weight: 800;
  letter-spacing: -0.02em;
}

.hero__badge {
  padding: 4px 10px;
  border-radius: 8px;
  background: rgb(255 255 255 / 85%);
  color: var(--accent-ink);
  font-size: 12px;
  font-weight: 800;
  box-shadow: 0 2px 8px rgb(177 140 255 / 18%);
}

.hero__id {
  margin: 0 0 10px;
  color: var(--text-muted);
  font-size: 14px;
  font-variant-numeric: tabular-nums;
}

.hero__note {
  margin: 0;
  color: var(--text-faint);
  font-size: 13px;
}

/* 右边两枚事实：小标题在上、值在下，与个人信息卡里的数字同一档。 */
.hero__facts {
  position: relative;
  z-index: 1;
  display: flex;
  flex: none;
  gap: 14px;
  margin: 0;
}

.fact {
  min-width: 120px;
  padding: 12px 16px;
  border: 1px solid rgb(255 255 255 / 70%);
  border-radius: 16px;
  background: rgb(255 255 255 / 55%);
}

.fact dt {
  margin-bottom: 4px;
  color: var(--text-faint);
  font-size: 12px;
  font-weight: 600;
}

.fact dd {
  margin: 0;
  overflow: hidden;
  color: var(--text);
  font-size: 15px;
  font-weight: 700;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* ── 下面两张卡 ───────────────────────────────────────────────────────── */

/* 两块动作卡上下排：改密在上、退出在下，都拉满内容区宽度。 */
.panels {
  display: flex;
  flex-direction: column;
  gap: 24px;
}

.card {
  padding: 26px 28px;
  border: 1px solid var(--glass-border);
  border-radius: var(--radius);
  background: var(--glass-bg);
  box-shadow: var(--glass-shadow), var(--inset-light);
  backdrop-filter: blur(20px);
  -webkit-backdrop-filter: blur(20px);
  transition: background 0.3s ease, box-shadow 0.3s ease;
}

.card:hover {
  background: var(--glass-bg-strong);
  box-shadow: var(--glass-shadow-hover), var(--inset-light);
}

/* 退出是破坏性动作：整张卡淡淡地染一层红，边界说清楚，但不抢改密那张的视线。 */
.card--danger {
  border-color: rgb(192 57 43 / 16%);
  background: rgb(255 246 245 / 60%);
}

.card--danger:hover {
  background: rgb(255 246 245 / 85%);
}

.card__head {
  display: flex;
  gap: 10px;
  align-items: center;
  margin-bottom: 8px;
}

/* 标题前的小图标块：与导航里的图标同一档尺寸，一眼分得清是哪张卡。 */
.card__icon {
  display: grid;
  flex: none;
  place-items: center;
  width: 32px;
  height: 32px;
  border-radius: 10px;
  background: linear-gradient(135deg, rgb(177 140 255 / 18%), rgb(102 210 245 / 18%));
  font-size: 16px;
  line-height: 1;
}

.card__icon--danger {
  background: linear-gradient(135deg, rgb(192 57 43 / 12%), rgb(255 176 124 / 18%));
}

.card__title {
  margin: 0;
  color: var(--text);
  font-size: 17px;
  font-weight: 800;
  letter-spacing: -0.01em;
}

.hint {
  margin: 0 0 18px;
  color: var(--text-muted);
  font-size: 14px;
  line-height: 1.6;
}

/* ── 按钮 ─────────────────────────────────────────────────────────────── */

/* 卡里的按钮靠左，与标题、说明同一条竖线。 */
.actions {
  display: flex;
  justify-content: flex-start;
}

/* 主按钮：渐变底 + 一点投影，与首页的强调色同一套。 */
.primary {
  padding: 10px 22px;
  border: 0;
  border-radius: 12px;
  background: var(--accent-gradient);
  box-shadow: 0 8px 18px rgb(177 140 255 / 28%);
  color: #fff;
  font-size: 14px;
  font-weight: 700;
  cursor: pointer;
  transition: transform 0.2s ease, box-shadow 0.2s ease, opacity 0.2s ease;
}

.primary:hover:enabled {
  box-shadow: 0 10px 22px rgb(177 140 255 / 38%);
  transform: translateY(-1px);
}

.primary:disabled {
  cursor: default;
  opacity: 0.55;
}

/* 退出是破坏性动作，但不该比"保存"更显眼：描边红字，悬停才染底色。 */
.signout {
  padding: 9px 20px;
  border: 1px solid rgb(192 57 43 / 55%);
  border-radius: 12px;
  background: transparent;
  color: var(--danger);
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.2s ease, border-color 0.2s ease;
}

.signout:hover:enabled {
  border-color: var(--danger);
  background: rgb(192 57 43 / 8%);
}

.signout:disabled {
  cursor: default;
  opacity: 0.55;
}

/* 内容区窄下来时身份卡折行，两枚事实落到下一排；断点跟骨架的图标栏同步。 */
@media (max-width: 1200px) {
  .hero {
    flex-wrap: wrap;
  }

  .hero__facts {
    width: 100%;
  }
}
</style>
