import { useState, useEffect } from 'react'
import { getWildPets, subscribe } from '../../api'

// —— 野生宠物图层(异色/炫彩 · 污染 · 最大声音 · 最小声音)——
// 与 POI 图层不同,这几类**不是固定点位**:野生宠会刷新、被别人抓走,只有走进 AOI 才知道它在。
// 后端从周边实体快照与 AOI 通知里挑出这几类推过来(见 internal/pipeline/wildpets.go),
// 前端只管开关与摆放。判定依据(捕捉前后一致的属性)见 docs/data.md 3.5。
//
// 存储键带版本号:图层键改过就进一版,沿用旧键会让浏览器里存着旧选择的人一个图层都不开,
// 与「异色/炫彩默认勾选」相悖。v2:glass/colorful/shiny/nightmare → mutation/pollution/voice;
// v3:满声音一分为二(voice → voiceMax + voiceMin)。
const LS_KEY = 'map.wildLayers.v3'

// 图层 = 一个开关,可覆盖后端 kinds 里的**多个**类别(异色与炫彩合成一个开关);
// 按稀有度从高到低排,color 同时用作侧栏色点与地图标记描边(见 wildRing)。
export const WILD_LAYERS = [
  { k: 'mutation', n: '异色/炫彩', kinds: ['shiny', 'colorful'], color: '#7ad3ff', on: true },
  { k: 'pollution', n: '污染', kinds: ['pollution'], color: '#c792ea' },
  { k: 'voiceMax', n: '最大声音', kinds: ['voiceMax'], color: '#ffd54a' },
  { k: 'voiceMin', n: '最小声音', kinds: ['voiceMin'], color: '#7ee0a0' },
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
  if (has('voiceMax')) out.push('最大声音')
  if (has('voiceMin')) out.push('最小声音')
  return out
}

// wildRing 把一只宠物命中的类别翻成描边样式:最稀有的那层上主描边,次一层再加一圈外环。
// (一只可以同时是炫彩 + 最大声音,靠 CSS 类组合会指数爆炸,故按数据算。)
export function wildRing(kinds = []) {
  const hit = WILD_LAYERS.filter((l) => l.kinds.some((k) => kinds.includes(k)))
  if (hit.length === 0) return {}
  const style = { borderColor: hit[0].color }
  if (hit.length > 1) style.boxShadow = `0 0 0 2px ${hit[1].color}`
  return style
}

// null = 用户从没手动选过,按各图层的 on 默认;数组 = 用户的选择(可以是空数组 = 全关)。
const loadKeys = () => {
  try {
    const v = JSON.parse(localStorage.getItem(LS_KEY))
    return Array.isArray(v) ? v : null
  } catch { return null }
}
const defaultKeys = () => WILD_LAYERS.filter((l) => l.on).map((l) => l.k)

// useWildPets 管理野生宠物图层:订阅后端推送、按开关筛出可绘制的标记。
export function useWildPets(account) {
  const [pets, setPets] = useState([])
  const [on, setOn] = useState(() => new Set(loadKeys() || defaultKeys()))

  useEffect(() => {
    let alive = true
    setPets([])
    getWildPets().then((d) => { if (alive && d) setPets(d.pets || []) }).catch(() => {})
    return () => { alive = false }
  }, [account])

  // 后端每次成员/状态变化都推全量列表(实体进出 AOI 是低频事件),直接替换即可。
  useEffect(() => subscribe((m) => {
    if (m.type === 'wildpets') setPets(m.data.pets || [])
  }), [account])

  const toggle = (k) => {
    setOn((prev) => {
      const next = new Set(prev)
      next.has(k) ? next.delete(k) : next.add(k)
      localStorage.setItem(LS_KEY, JSON.stringify([...next]))
      return next
    })
  }

  // 开着的图层覆盖哪些后端类别;一只宠物命中任一即画(可同时命中多层)。
  const shown = new Set(WILD_LAYERS.filter((l) => on.has(l.k)).flatMap((l) => l.kinds))
  const marks = pets.filter((p) => (p.kinds || []).some((k) => shown.has(k)))
  // 图层行上的计数与地图上画出的标记一一对应:灰点(已离开视野的最后所见)也画在图上,
  // 故也计入——否则侧栏显示 0 而图上还挂着几个,只会让人以为标记出错了。
  // 另单算其中的灰点数,供侧栏悬浮说明拆开「视野内 / 已离开」(见 LayerPanel)。
  const count = (l, pick) => pets.filter(
    (p) => pick(p) && (p.kinds || []).some((k) => l.kinds.includes(k))).length
  const num = Object.fromEntries(WILD_LAYERS.map((l) => [l.k, count(l, () => true)]))
  const numStale = Object.fromEntries(WILD_LAYERS.map((l) => [l.k, count(l, (p) => p.stale)]))

  return { marks, num, numStale, on, toggle }
}
