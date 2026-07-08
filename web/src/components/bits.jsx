import React from 'react'
import { createPortal } from 'react-dom'
import { IconsContext } from '../App'

const imgURL = (path) => '/img/' + path

// 极值高亮:声音接近 ±100、体重百分位接近上下限时按边界方向着色(列表/事件/详情统一)。
// 返回 val-hot-hi(接近上边界)/ val-hot-lo(接近下边界)/ undefined。
export const voiceHot = (v) => v >= 96 ? 'val-hot-hi' : v <= -96 ? 'val-hot-lo' : undefined
export const pctHot = (pct) => pct == null ? undefined : pct >= 98 ? 'val-hot-hi' : pct <= 2 ? 'val-hot-lo' : undefined

// InlineIcon 渲染文字前的小图标(系别/六维/血脉等);无路径或加载失败则不占位(留文字)。
export function InlineIcon({ src, className = 'inline-ic', alt = '' }) {
  const [bad, setBad] = React.useState(false)
  React.useEffect(() => setBad(false), [src])
  if (!src || bad) return null
  return <img className={className} src={imgURL(src)} alt={alt} loading="lazy" onError={() => setBad(true)} />
}

// StatIcon 按六维键(hp/attack/…)从 IconsContext 取对应属性小图。
export function StatIcon({ statKey, className = 'stat-ic' }) {
  const icons = React.useContext(IconsContext)
  return <InlineIcon src={icons.stat && icons.stat[statKey]} className={className} alt="" />
}

// boxLabel 把盒子位置渲染为 "13-性格1 5-2"(排-格,每盒 5 排 × 6 格,slot 从 0 起)。
export function boxLabel(box) {
  if (!box) return '-'
  const name = box.boxName || `盒${box.boxId}`
  const row = Math.floor(box.slot / 6) + 1
  const col = (box.slot % 6) + 1
  return `${box.boxId}-${name} ${row}-${col}`
}

// teamLabel 把队伍位置渲染为 "3-2"(队-位,teamIdx/pos 从 0 起)。
export function teamLabel(team) {
  if (!team) return '-'
  return `${team.teamIdx + 1}-${team.pos + 1}`
}

// locTag 返回宠物位置的【简化文案】,列表/事件/详情统一使用(单一权威格式):
// 盒子 📦盒号-盒名 排-格 / 大世界 🌍大世界 队-位 / 尚未落位 ⏳位置待同步。
export function locTag(pet) {
  if (pet?.box) return `📦${boxLabel(pet.box)}`
  if (pet?.team) return `🌍大世界 ${teamLabel(pet.team)}`
  return '⏳位置待同步'
}

// PetMark 渲染搭档标记徽章(橙色外框底 img_collect + 白色标记符号),叠在头像左上角;
// 无标记(值 0=无)或缺符号图时不渲染。
export function PetMark({ p }) {
  const icons = React.useContext(IconsContext)
  if (!p || !p.partnerMarkIcon || p.partnerMark === '无') return null
  return (
    <span className="pet-mark" title={p.partnerMark}>
      {icons.partnerFrame && <img className="pet-mark-frame" src={imgURL(icons.partnerFrame)} alt="" />}
      <img className="pet-mark-ic" src={imgURL(p.partnerMarkIcon)} alt={p.partnerMark} />
    </span>
  )
}

// Avatar 渲染宠物小头像(列表/事件用);无图(未上线/缺源)或无 pet 回退 emoji;
// 有搭档标记时在左上角叠加徽章。
export function Avatar({ p, className = 'pet-avatar' }) {
  const [bad, setBad] = React.useState(false)
  const src = p && p.image && p.image.head
  const inner = (src && !bad)
    ? <img className={className} src={imgURL(src)} alt={p.species} loading="lazy" onError={() => setBad(true)} />
    : <div className={className}>{p && p.shiny ? '✨' : '🐾'}</div>
  return <span className="avatar-wrap">{inner}<PetMark p={p} /></span>
}

// Portrait 渲染宠物全身图(详情用,优先 Pet256 全身缩略,退大头像);无图回退 emoji。
export function Portrait({ p }) {
  const src = (p.image && (p.image.portraitSmall || p.image.bigHead)) || ''
  const [bad, setBad] = React.useState(false)
  React.useEffect(() => setBad(false), [src])
  return (
    <div className="detail-hero">
      <PetMark p={p} />
      {src && !bad
        ? <img src={imgURL(src)} alt={p.species} onError={() => setBad(true)} />
        : <span>{p.shiny ? '✨' : '🐾'}</span>}
    </div>
  )
}

// Types 渲染系别(icons 与 types 一一对应,前置属性小图);plain=去掉色块背景,仅图标+文字。
export function Types({ types, icons, plain }) {
  const list = types || []
  const cls = plain ? 'type type-plain' : 'type'
  return (
    <>
      {list.map((t, i) => (
        <span key={i} className={cls} data-t={t}>
          <InlineIcon src={icons && icons[i]} className="type-ic" alt="" />{t}
        </span>
      ))}
      {list.length === 0 && <span className="muted">-</span>}
    </>
  )
}

// Blood 渲染血脉(主图标 + 中文短名);iconOnly=仅图标(列表用,名称落到 title)。
export function Blood({ p, iconOnly }) {
  if (!p || !p.blood) return null
  return (
    <span className="blood" title={'血脉 ' + p.blood}>
      <InlineIcon src={p.bloodIcon} className="blood-ic" alt={p.blood} />{!iconOnly && p.blood}
    </span>
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

// Six 渲染六维(纯文字:标签 + 面板值 + 性格 ±10% 升降箭头 + 天分 +N)。列表用。
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
            {s.talentLv > 0 && <span className="talent" title="天分">+{s.talentLv}</span>}
          </div>
        )
      })}
    </div>
  )
}

// 雷达图六轴顺序(顺时针自顶点):生命→魔攻→魔防→速度→物防→物攻,与游戏内六维雷达一致。
const RADAR_AXES = ['hp', 'spAttack', 'spDefense', 'speed', 'defense', 'attack']
// 绝对刻度:外环对应的面板值上限(与游戏一致,按截图比例定标 219≈44%);超出者夹到外环。
const RADAR_MAX = 500

// NatBadge 右上角性格增减角标:SVG 实心粗箭头(箭头 + 矩形柄,同游戏内,无圆底),
// 绿=增益向上、红=减益向下。
function NatBadge({ dir }) {
  const up = dir === 1
  return (
    <span className={'radar-nat ' + (up ? 'up' : 'down')} title={up ? '性格 +10%' : '性格 -10%'}>
      <svg viewBox="0 0 12 14" aria-hidden="true">
        <path d={up ? 'M6 0L12 6H8.5V14H3.5V6H0Z' : 'M6 14L12 8H8.5V0H3.5V8H0Z'} />
      </svg>
    </span>
  )
}

// StatRadar 以六边形雷达图展示六维(仅图标,不显示中文标签):各顶点=属性图标 + 面板值
// (含性格 ±10% 箭头 / 天分 +N);橙色多边形按绝对刻度(RADAR_MAX)定标,越强多边形越大。详情页用。
export function StatRadar({ p }) {
  const icons = React.useContext(IconsContext)
  const stats = RADAR_AXES.map((k) => p[k] || {})
  const vals = stats.map((s) => s.value ?? 0)
  const size = 280, c = size / 2, R = 84, labelR = 108
  const pt = (i, r) => {
    const a = (-90 + i * 60) * Math.PI / 180
    return [c + r * Math.cos(a), c + r * Math.sin(a)]
  }
  const poly = (r) => RADAR_AXES.map((_, i) => pt(i, r).join(',')).join(' ')
  const dataPoly = vals.map((v, i) => pt(i, R * Math.min(1, v / RADAR_MAX)).join(',')).join(' ')
  return (
    <div className="radar">
      <svg className="radar-svg" viewBox={`0 0 ${size} ${size}`}>
        {[0.25, 0.5, 0.75, 1].map((rr, i) => <polygon key={i} className="radar-ring" points={poly(R * rr)} />)}
        {RADAR_AXES.map((_, i) => { const [x, y] = pt(i, R); return <line key={i} className="radar-spoke" x1={c} y1={c} x2={x} y2={y} /> })}
        <polygon className="radar-area" points={dataPoly} />
      </svg>
      {RADAR_AXES.map((key, i) => {
        const s = stats[i]
        const [x, y] = pt(i, labelR)
        const talented = s.talentLv > 0
        return (
          <div key={key} className="radar-label" style={{ left: (x / size * 100) + '%', top: (y / size * 100) + '%' }}>
            <InlineIcon src={icons.stat && icons.stat[key]} className="radar-ic" alt="" />
            <b className={talented ? 'has-talent' : undefined} title={talented ? `天分 +${s.talentLv}` : undefined}>{s.value ?? 0}</b>
            {s.nature === 1 && <NatBadge dir={1} />}
            {s.nature === -1 && <NatBadge dir={-1} />}
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

// EggGroups 展示宠物蛋组(繁殖组)标签,每个组名 hover 显示官方描述;无蛋组返回 null。
export function EggGroups({ groups }) {
  if (!groups || !groups.length) return null
  return (
    <span className="egg-groups">
      {groups.map((g) => (
        <span key={g.id} className="egg-group" title={g.desc ? `蛋组 · ${g.desc}` : '蛋组'}>{g.name}</span>
      ))}
    </span>
  )
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

// MarkIcon 渲染单个异色/炫彩标记图;无图或加载失败退化为原文字徽标(异/彩)。
function MarkIcon({ src, title, fallback, cls }) {
  const [bad, setBad] = React.useState(false)
  React.useEffect(() => setBad(false), [src])
  if (src && !bad) {
    return <img className="mark-img" src={imgURL(src)} alt={title} title={title} onError={() => setBad(true)} />
  }
  return <span className={'mark ' + cls} title={title}>{fallback}</span>
}

// Marks 渲染异色/炫彩标记(优先游戏图标;两者兼具用合成的异色炫彩图)。
export function Marks({ p }) {
  const icons = React.useContext(IconsContext)
  if (!p) return null
  if (p.shiny && p.colorful && icons.shinyColorful) {
    return <MarkIcon src={icons.shinyColorful} title="异色炫彩" fallback="异彩" cls="mark-colorful" />
  }
  return (
    <>
      {p.shiny && <MarkIcon src={icons.shiny} title="异色" fallback="异" cls="mark-shiny" />}
      {p.colorful && <MarkIcon src={icons.colorful} title="炫彩" fallback="彩" cls="mark-colorful" />}
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
