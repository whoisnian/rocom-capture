import React from 'react'
import { createPortal } from 'react-dom'

const imgURL = (path) => '/img/' + path

// boxLabel 把盒子位置渲染为 "13-性格1 第5排第2格"(每盒 5 排 × 6 格,slot 从 0 起)。
export function boxLabel(box) {
  if (!box) return '-'
  const name = box.boxName || `盒${box.boxId}`
  const row = Math.floor(box.slot / 6) + 1
  const col = (box.slot % 6) + 1
  return `${box.boxId}-${name} 第${row}排第${col}格`
}

// teamLabel 把队伍位置渲染为 "第3队第2位"(teamIdx/pos 从 0 起)。
export function teamLabel(team) {
  if (!team) return '-'
  return `第${team.teamIdx + 1}队第${team.pos + 1}位`
}

// locText 返回宠物的位置文本:在盒显示盒位,在大世界队伍显示队位,否则 '-'。
export function locText(pet) {
  if (pet.box) return boxLabel(pet.box)
  if (pet.team) return '大世界 ' + teamLabel(pet.team)
  return '-'
}

// Avatar 渲染宠物小头像(列表/事件用);无图(未上线/缺源)或无 pet 回退 emoji。
export function Avatar({ p, className = 'pet-avatar' }) {
  const [bad, setBad] = React.useState(false)
  const src = p && p.image && p.image.head
  if (src && !bad) {
    return <img className={className} src={imgURL(src)} alt={p.species} loading="lazy" onError={() => setBad(true)} />
  }
  return <div className={className}>{p && p.shiny ? '✨' : '🐾'}</div>
}

// Portrait 渲染宠物全身图(详情用,优先 Pet256 全身缩略,退大头像);无图回退 emoji。
export function Portrait({ p }) {
  const src = (p.image && (p.image.portraitSmall || p.image.bigHead)) || ''
  const [bad, setBad] = React.useState(false)
  React.useEffect(() => setBad(false), [src])
  return (
    <div className="detail-hero">
      {src && !bad
        ? <img src={imgURL(src)} alt={p.species} onError={() => setBad(true)} />
        : <span>{p.shiny ? '✨' : '🐾'}</span>}
    </div>
  )
}

// Types 渲染系别色块。
export function Types({ types }) {
  return (
    <>
      {(types || []).map((t, i) => (
        <span key={i} className="type" data-t={t}>{t}</span>
      ))}
      {(!types || types.length === 0) && <span className="muted">-</span>}
    </>
  )
}

const SIX = [
  ['生命', 'hp'],
  ['物攻', 'attack'],
  ['物防', 'defense'],
  ['速度', 'speed'],
  ['魔攻', 'spAttack'],
  ['魔防', 'spDefense'],
]

// Six 渲染六维(性格 ±10% 升降箭头 + 天分等级 +N)。
export function Six({ p }) {
  return (
    <div className="six">
      {SIX.map(([label, key]) => {
        const s = p[key] || {}
        return (
          <div key={key}>
            {label} <b>{s.value ?? 0}</b>
            {s.nature === 1 && <span className="up" title="性格 +10%"> ↑</span>}
            {s.nature === -1 && <span className="down" title="性格 -10%"> ↓</span>}
            {s.talentLv > 0 && <span className="talent" title="天分等级">+{s.talentLv}</span>}
          </div>
        )
      })}
    </div>
  )
}

// StatRange 渲染身高/体重值;悬停时 tooltip 以 `99.67% (下限-上限)` 显示当前值百分位与该形态取值范围。
// 范围/百分位来自后端 FillSizePercentile(按当前形态注入);未知形态无范围时退化为纯文本(无 tooltip)。
// tooltip 经 portal 渲染到 body、fixed 定位:不受列表 .table-wrap 的 overflow 裁剪,
// 默认浮在值上方,顶部空间不足时翻转到下方,并按视口左右夹取(可溢出列表但不出屏)。
export function StatRange({ value, min, max, pct, unit }) {
  const text = `${value}${unit}`
  const ref = React.useRef(null)
  const [anchor, setAnchor] = React.useState(null) // 悬停时锚点元素的视口矩形
  if (!(max > min)) return <>{text}</>
  const pctText = pct != null ? `${pct.toFixed(2)}%` : null
  const content = pctText ? `${pctText} (${min}-${max})` : `${min}-${max}`
  const show = () => { if (ref.current) setAnchor(ref.current.getBoundingClientRect()) }
  const hide = () => setAnchor(null)
  return (
    <span ref={ref} onMouseEnter={show} onMouseLeave={hide}>
      {text}
      {anchor && <Tooltip content={content} anchor={anchor} />}
    </span>
  )
}

// Tooltip 把内容 portal 到 body 并 fixed 定位:挂载后按自身尺寸相对锚点居中,
// 默认放上方,空间不足翻下方,水平方向夹在视口内(留 4px 边距)。定位算完前隐藏避免闪跳。
function Tooltip({ content, anchor }) {
  const ref = React.useRef(null)
  const [pos, setPos] = React.useState(null)
  React.useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const gap = 6
    const w = el.offsetWidth, h = el.offsetHeight
    let left = anchor.left + anchor.width / 2 - w / 2
    left = Math.max(4, Math.min(left, window.innerWidth - w - 4))
    let top = anchor.top - gap - h            // 默认上方
    if (top < 4) top = anchor.bottom + gap    // 上方放不下 → 翻到下方
    setPos({ left, top })
  }, [content, anchor])
  return createPortal(
    <div ref={ref} className="tip-pop" style={pos ? { left: pos.left, top: pos.top } : { left: 0, top: 0, visibility: 'hidden' }}>
      {content}
    </div>,
    document.body,
  )
}

// Gender 渲染性别符号(♂ 蓝、♀ 粉,加大加粗,字体差异下也易辨)。
export function Gender({ g }) {
  if (g !== '♂' && g !== '♀') return null
  return <span className={'gender ' + (g === '♂' ? 'male' : 'female')}>{g}</span>
}

// Form 渲染地区/季节形态徽标(普通宠物为空)。
export function Form({ form }) {
  if (!form) return null
  return <span className="mark mark-form" title="形态">{form}</span>
}

// ImgAvatar 按图片相对路径渲染一个头像(进化链等无 pet 对象处用);缺图回退 emoji。
export function ImgAvatar({ src, alt = '', className = 'pet-avatar' }) {
  const [bad, setBad] = React.useState(false)
  React.useEffect(() => setBad(false), [src])
  if (src && !bad) {
    return <img className={className} src={imgURL(src)} alt={alt} loading="lazy" onError={() => setBad(true)} />
  }
  return <div className={className}>🐾</div>
}

// Marks 渲染异色/炫彩标记。
export function Marks({ p }) {
  if (!p) return null
  return (
    <>
      {p.shiny && <span className="mark mark-shiny" title="异色">异</span>}
      {p.colorful && <span className="mark mark-colorful" title="炫彩">彩</span>}
    </>
  )
}

// fmtTime 把 unix 秒格式化为本地时间。
export function fmtTime(ts) {
  if (!ts) return '-'
  const d = new Date(ts * 1000)
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}
