import { useState, useEffect } from 'react'
import { getWildPets, subscribe } from '../../api'

// —— 野生宠物图层(变异 · 污染 · 体重 · 嗓音)——
// 与 POI 图层不同,这几类**不是固定点位**:野生宠会刷新、被别人抓走,只有走进 AOI 才知道它在。
// 后端从周边实体快照与 AOI 通知里挑出这几类推过来(见 internal/pipeline/wildpets.go),
// 前端只管开关与摆放。判定依据(捕捉前后一致的属性)见 docs/data.md 3.5。
//
// v3 把旧 voice(精确 +100)拆成奖牌窗口与 MAX;读 v2 时迁移到
// voice-high-max,保持原有选择的实际语义不变。
const LS_KEY = 'map.wildLayers.v3'
const OLD_LS_KEY = 'map.wildLayers.v2'
const MODE_LS_KEY = 'map.wildMatchMode.v1'

// 图层 = 一个开关,可覆盖后端 kinds 里的多个类别(异色与炫彩合成一个开关)。
// priority 只决定多类别标记的描边优先级,UI 顺序按用户筛选习惯排列。
export const WILD_LAYERS = [
  { k: 'mutation', group: 'mutation', n: '异色/炫彩', kinds: ['shiny', 'colorful'], color: '#7ad3ff', priority: 100, on: true },
  { k: 'pollution', group: 'pollution', n: '污染', kinds: ['pollution'], color: '#c792ea', priority: 90 },
  { k: 'weight-big', group: 'weight', n: '大块头', kinds: ['weight-big'], color: '#f5b942', priority: 40 },
  { k: 'weight-small', group: 'weight', n: '小不点', kinds: ['weight-small'], color: '#4c8dff', priority: 40 },
  { k: 'weight-big-max', group: 'weight', n: '大块头MAX', kinds: ['weight-big-max'], color: '#ff6b57', priority: 80 },
  { k: 'weight-small-max', group: 'weight', n: '小不点MAX', kinds: ['weight-small-max'], color: '#2dd4bf', priority: 80 },
  { k: 'voice-high', group: 'voice', n: '婉转声', kinds: ['voice-high'], color: '#e6d05f', priority: 30 },
  { k: 'voice-low', group: 'voice', n: '粗嗓门', kinds: ['voice-low'], color: '#8b9cff', priority: 30 },
  { k: 'voice-high-max', group: 'voice', n: '婉转声MAX', kinds: ['voice-high-max'], color: '#ff7eb6', priority: 70 },
  { k: 'voice-low-max', group: 'voice', n: '粗嗓门MAX', kinds: ['voice-low-max'], color: '#55d6e8', priority: 70 },
]

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

// null = 用户从没手动选过,按各图层的 on 默认;数组 = 用户的选择(可以是空数组 = 全关)。
const loadKeys = () => {
  try {
    const v = JSON.parse(localStorage.getItem(LS_KEY))
    if (Array.isArray(v)) return v
    const old = JSON.parse(localStorage.getItem(OLD_LS_KEY))
    if (!Array.isArray(old)) return null
    return old.map((k) => k === 'voice' ? 'voice-high-max' : k)
  } catch { return null }
}
const defaultKeys = () => WILD_LAYERS.filter((l) => l.on).map((l) => l.k)
const loadMode = () => localStorage.getItem(MODE_LS_KEY) === 'and' ? 'and' : 'or'

// useWildPets 管理野生宠物图层:订阅后端推送、按当前场景与开关筛出可绘制的标记。
export function useWildPets(account, sceneResId) {
  const [snapshot, setSnapshot] = useState({ sceneResId: null, pets: [] })
  const [on, setOn] = useState(() => new Set(loadKeys() || defaultKeys()))
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
    setOn((prev) => {
      const next = new Set(prev)
      next.has(k) ? next.delete(k) : next.add(k)
      localStorage.setItem(LS_KEY, JSON.stringify([...next]))
      return next
    })
  }

  const setMode = (next) => {
    const mode = next === 'and' ? 'and' : 'or'
    localStorage.setItem(MODE_LS_KEY, mode)
    setModeState(mode)
  }

  // OR: 命中任一已开图层。AND:同属性组内 OR、不同属性组间 AND。例如同时选择
  // 大/小块头 MAX 与高/低嗓音 MAX,即要求「任一体重 MAX 且任一嗓音 MAX」。
  // 复合的「异色/炫彩」本身也只需命中 shiny/colorful 任一。全关时不显示标记。
  const active = WILD_LAYERS.filter((l) => on.has(l.k))
  const hits = (p, l) => l.kinds.some((k) => (p.kinds || []).includes(k))
  const grouped = new Map()
  active.forEach((l) => grouped.set(l.group, [...(grouped.get(l.group) || []), l]))
  const groups = [...grouped.values()]
  const marks = active.length === 0 ? [] : pets.filter((p) =>
    mode === 'and' ? groups.every((group) => group.some((l) => hits(p, l))) : active.some((l) => hits(p, l)))
  // 图层行计数是该单项条件的命中数;AND 模式下不等于最终交集数。灰点
  // (已离开视野的最后所见)也计入,另单算灰点数供侧栏悬浮拆开说明。
  const count = (l, pick) => pets.filter(
    (p) => pick(p) && (p.kinds || []).some((k) => l.kinds.includes(k))).length
  const num = Object.fromEntries(WILD_LAYERS.map((l) => [l.k, count(l, () => true)]))
  const numStale = Object.fromEntries(WILD_LAYERS.map((l) => [l.k, count(l, (p) => p.stale)]))

  const ring = (kinds) => wildRing(kinds, on)
  return { marks, num, numStale, on, toggle, mode, setMode, ring }
}
