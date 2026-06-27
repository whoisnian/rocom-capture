import React from 'react'

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

// Six 渲染六维(含性格升降箭头)。
export function Six({ p }) {
  return (
    <div className="six">
      {SIX.map(([label, key]) => {
        const s = p[key] || {}
        return (
          <div key={key}>
            {label} <b>{s.value ?? 0}</b>
            {s.natureAdd > 0 && <span className="up"> ↑</span>}
            {s.natureAdd < 0 && <span className="down"> ↓</span>}
          </div>
        )
      })}
    </div>
  )
}

// fmtTime 把 unix 秒格式化为本地时间。
export function fmtTime(ts) {
  if (!ts) return '-'
  const d = new Date(ts * 1000)
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}
