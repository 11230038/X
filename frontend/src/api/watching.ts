/**
 * 视频学习：按关键词搜索 B 站视频。
 *
 * 结果里已经带好官方播放器的地址（`embed_url`）。前端**不做任何拼接** ——
 * 播放器认的只有后端给的那个 id，地址从哪里来、怎么拼，只在后端有一份。
 */

import { request } from './client'

export interface VideoSearchItem {
  source: string
  video_id: string
  title: string
  author: string
  cover_url: string
  duration_seconds: number
  play_count: number
  published_at: number
  watch_url: string
  embed_url: string
}

export interface VideoSearchResult {
  query: string
  page: number
  has_more: boolean
  items: VideoSearchItem[]
}

/** `page` 从 1 开始；`query` 已由调用方 trim 过。 */
export function searchVideos(query: string, page = 1): Promise<VideoSearchResult> {
  return request<VideoSearchResult>('/video/search', { query: { q: query, page } })
}
