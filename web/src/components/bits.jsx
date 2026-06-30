import React from 'react'

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

// Gender 渲染性别符号(♂ 蓝、♀ 粉,加大加粗,字体差异下也易辨)。
export function Gender({ g }) {
  if (g !== '♂' && g !== '♀') return null
  return <span className={'gender ' + (g === '♂' ? 'male' : 'female')}>{g}</span>
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
