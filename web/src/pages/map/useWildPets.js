import { useState, useEffect } from 'react'
import { getWildPets, subscribe } from '../../api'
import { WILD_LAYERS, matchesWildPet } from './wildFilter'

export { WILD_LAYERS } from './wildFilter'

// —— 野生宠物图层(变异 · 污染 · 体重 · 嗓音)——
// 与 POI 图层不同,这几类**不是固定点位**:野生宠会刷新、被别人抓走,只有走进 AOI 才知道它在。
// 后端从周边实体快照与 AOI 通知里挑出这几类推过来(见 internal/pipeline/wildpets.go),
// 前端只管开关与摆放。判定依据(捕捉前后一致的属性)见 docs/data.md 3.5。
//
// v4 分开记忆共享独立项、OR 条件与 AND 条件。迁移 v3 时按当时保存的模式放入对应条件集;
// 更早的 v2 voice(精确 +100)先迁移为 voice-high-max。
const LS_KEY = 'map.wildLayers.v4'
const LEGACY_LS_KEY = 'map.wildLayers.v3'
const OLD_LS_KEY = 'map.wildLayers.v2'
const MODE_LS_KEY = 'map.wildMatchMode.v1'

// wildTags 把一只宠物命中的类别翻成悬浮提示上的标签(比图层名更细:图层把异色/炫彩合成
// 一个开关,提示里仍分开说)。异色 + 炫彩兼具时游戏自己有个合称「异色炫彩」
// (见 gen_gamedata.py 的 STATIC_ICONS),用它比并列两个词自然。
export function wildTags(kinds = []) {
  const has = (k) => kinds.includes(k)
  const out = []
  if (has('shiny') && has('colorful')) out.push('异色炫彩')
  else if (has('shiny')) out.push('异色')
  else if (has('colorful')) out.push('炫彩')
  if (has('pollution')) out.push('污染')
  if (has('weight-big-max')) out.push('大块头MAX')
  else if (has('weight-big')) out.push('大块头')
  if (has('weight-small-max')) out.push('小不点MAX')
  else if (has('weight-small')) out.push('小不点')
  if (has('voice-high-max')) out.push('婉转声MAX')
  else if (has('voice-high')) out.push('婉转声')
  if (has('voice-low-max')) out.push('粗嗓门MAX')
  else if (has('voice-low')) out.push('粗嗓门')
  return out
}

function mixColors(colors) {
  if (colors.length === 1) return colors[0]
  const rgb = colors.map((color) => [1, 3, 5].map((i) => parseInt(color.slice(i, i + 2), 16)))
  const mixed = [0, 1, 2].map((i) => Math.round(rgb.reduce((sum, c) => sum + c[i], 0) / rgb.length))
  return '#' + mixed.map((v) => v.toString(16).padStart(2, '0')).join('')
}

// wildRing 把一只宠物命中的独立类别色等权混合成单一描边色。
export function wildRing(kinds = [], enabled = null) {
  // MAX 是对应普通奖牌条件的子集;标签和描边只显示更精确的 MAX,
  // 但筛选仍保留两个 kind,供普通/MAX 按钮分别匹配和计数。
  const hidden = new Set()
  if (kinds.includes('weight-big-max') && (!enabled || enabled.has('weight-big-max'))) hidden.add('weight-big')
  if (kinds.includes('weight-small-max') && (!enabled || enabled.has('weight-small-max'))) hidden.add('weight-small')
  if (kinds.includes('voice-high-max') && (!enabled || enabled.has('voice-high-max'))) hidden.add('voice-high')
  if (kinds.includes('voice-low-max') && (!enabled || enabled.has('voice-low-max'))) hidden.add('voice-low')
  const hit = WILD_LAYERS
    .filter((l) => (!enabled || enabled.has(l.k)) && !hidden.has(l.k) && l.kinds.some((k) => kinds.includes(k)))
    .sort((a, b) => b.priority - a.priority)
  if (hit.length === 0) return {}
  const color = mixColors(hit.map((l) => l.color))
  return {
    borderColor: color,
    boxShadow: hit.length > 1 ? `0 0 0 2px ${color}80` : undefined,
  }
}

const loadMode = () => localStorage.getItem(MODE_LS_KEY) === 'and' ? 'and' : 'or'
const saveFilters = (filters) => localStorage.setItem(LS_KEY, JSON.stringify({
  standalone: [...filters.standalone], or: [...filters.or], and: [...filters.and],
}))

const loadFilters = () => {
  try {
    const v = JSON.parse(localStorage.getItem(LS_KEY))
    if (v && Array.isArray(v.standalone) && Array.isArray(v.or) && Array.isArray(v.and)) {
      return { standalone: new Set(v.standalone), or: new Set(v.or), and: new Set(v.and) }
    }
    let old = JSON.parse(localStorage.getItem(LEGACY_LS_KEY))
    if (!Array.isArray(old)) {
      old = JSON.parse(localStorage.getItem(OLD_LS_KEY))
      if (Array.isArray(old)) old = old.map((k) => k === 'voice' ? 'voice-high-max' : k)
    }
    if (Array.isArray(old)) {
      const standalone = new Set(WILD_LAYERS.filter((l) => l.standalone && old.includes(l.k)).map((l) => l.k))
      const conditional = new Set(WILD_LAYERS.filter((l) => !l.standalone && old.includes(l.k)).map((l) => l.k))
      return {
        standalone,
        or: loadMode() === 'or' ? conditional : new Set(),
        and: loadMode() === 'and' ? conditional : new Set(),
      }
    }
  } catch { /* 使用默认值 */ }
  return {
    standalone: new Set(WILD_LAYERS.filter((l) => l.standalone && l.on).map((l) => l.k)),
    or: new Set(),
    and: new Set(),
  }
}

// useWildPets 管理野生宠物图层:订阅后端推送、按当前场景与开关筛出可绘制的标记。
export function useWildPets(account, sceneResId) {
  const [snapshot, setSnapshot] = useState({ sceneResId: null, pets: [] })
  const [filters, setFilters] = useState(loadFilters)
  const [mode, setModeState] = useState(loadMode)

  useEffect(() => {
    let alive = true
    setSnapshot({ sceneResId: null, pets: [] })
    getWildPets().then((d) => {
      if (alive && d) setSnapshot({ sceneResId: d.sceneResId, pets: d.pets || [] })
    }).catch(() => {})
    return () => { alive = false }
  }, [account])

  // 后端每次成员/状态变化都推全量列表(实体进出 AOI 是低频事件),直接替换即可。
  useEffect(() => subscribe((m) => {
    if (m.type === 'wildpets') {
      setSnapshot({ sceneResId: m.data.sceneResId, pets: m.data.pets || [] })
    }
  }), [account])

  // 位置与野生宠物是两条独立 SSE。切场景时任一条都可能先到,只绘制场景号一致的快照,
  // 避免把上一张地图或下一张地图的投影坐标短暂画到当前底图上。
  const pets = sceneResId != null && snapshot.sceneResId === sceneResId ? snapshot.pets : []

  const toggle = (k) => {
    const layer = WILD_LAYERS.find((l) => l.k === k)
    if (!layer) return
    setFilters((prev) => {
      const next = {
        standalone: new Set(prev.standalone), or: new Set(prev.or), and: new Set(prev.and),
      }
      const bucket = layer.standalone ? next.standalone : next[mode]
      bucket.has(k) ? bucket.delete(k) : bucket.add(k)
      saveFilters(next)
      return next
    })
  }

  const setMode = (next) => {
    const mode = next === 'and' ? 'and' : 'or'
    localStorage.setItem(MODE_LS_KEY, mode)
    setModeState(mode)
  }

  // OR 与 AND 两套条件同时生效并取并集;mode 只表示侧栏当前正在编辑哪一套。
  // 异色/炫彩、污染是跨页签共享的独立附加项;MAX 与普通条件都可进入 OR 或 AND。
  const layersIn = (keys) => WILD_LAYERS.filter((l) => keys.has(l.k))
  const standalone = layersIn(filters.standalone)
  const orLayers = layersIn(filters.or)
  const andLayers = layersIn(filters.and)
  const marks = pets.filter((p) => matchesWildPet(p, standalone, orLayers, andLayers))
  // 图层行计数是该单项条件的命中数;AND 条件集下不等于最终交集数。灰点
  // (已离开视野的最后所见)也计入,另单算灰点数供侧栏悬浮拆开说明。
  const count = (l, pick) => pets.filter(
    (p) => pick(p) && (p.kinds || []).some((k) => l.kinds.includes(k))).length
  const num = Object.fromEntries(WILD_LAYERS.map((l) => [l.k, count(l, () => true)]))
  const numStale = Object.fromEntries(WILD_LAYERS.map((l) => [l.k, count(l, (p) => p.stale)]))

  const on = new Set([...filters.standalone, ...filters[mode]])
  const enabled = new Set([...filters.standalone, ...filters.or, ...filters.and])
  const ring = (kinds) => wildRing(kinds, enabled)
  return { marks, num, numStale, on, toggle, mode, setMode, ring }
}
