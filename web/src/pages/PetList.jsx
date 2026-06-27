import React, { useState, useEffect, useCallback, useRef } from 'react'
import { useNavigate } from 'react-router-dom'
import { getPets, getFilterOptions, subscribe, ALL_TYPES } from '../api'
import { Types, Six, fmtTime } from '../components/bits'

// 热门性格(筛选用)及其影响。其余归入"其他"。
const HOT_NATURES = [
  ['开朗', '速↑魔攻↓'],
  ['胆小', '速↑物攻↓'],
  ['固执', '物攻↑魔攻↓'],
  ['聪明', '魔攻↑物攻↓'],
  ['平和', '生↑魔攻↓'],
  ['踏实', '生↑速↓'],
  ['沉默', '生↑物攻↓'],
  ['急躁', '速↑物防↓'],
]
const HOT_NAMES = HOT_NATURES.map((n) => n[0])

const SORTS = [
  { key: 'gid', label: '编号' },
  { key: 'level', label: '等级' },
  { key: 'weight', label: '体重' },
  { key: 'height', label: '身高' },
  { key: 'voice', label: '声音' },
  { key: 'catchTime', label: '捕捉时间' },
]

export default function PetList() {
  const nav = useNavigate()
  const [filter, setFilter] = useState({ page: 1, pageSize: 12, sort: 'gid', order: 'asc' })
  const [data, setData] = useState({ total: 0, pets: [] })
  const [options, setOptions] = useState({})
  const [collapsed, setCollapsed] = useState(true)
  const reloadRef = useRef(null)

  const load = useCallback(() => { getPets(filter).then(setData).catch(() => {}) }, [filter])
  useEffect(() => { load() }, [load])
  useEffect(() => { getFilterOptions().then(setOptions).catch(() => {}) }, [])

  // 实时：收到宠物更新时防抖重载当前页
  useEffect(() => {
    return subscribe((m) => {
      if (m.type !== 'pet') return
      clearTimeout(reloadRef.current)
      reloadRef.current = setTimeout(() => reloadRef.current && load(), 600)
    })
  }, [load])

  const set = (patch) => setFilter((f) => ({ ...f, ...patch, page: patch.page || 1 }))
  const toggleType = (t) =>
    setFilter((f) => {
      const s = new Set(f.types || [])
      s.has(t) ? s.delete(t) : s.add(t)
      return { ...f, types: [...s], page: 1 }
    })
  const sortBy = (key) =>
    setFilter((f) => ({ ...f, sort: key, order: f.sort === key && f.order === 'asc' ? 'desc' : 'asc', page: 1 }))

  const pages = Math.max(1, Math.ceil(data.total / filter.pageSize))
  const arrow = (k) => (filter.sort === k ? (filter.order === 'asc' ? ' ▲' : ' ▼') : '')

  return (
    <div className="list-layout">
      <aside className={'filters' + (collapsed ? ' collapsed' : '')}>
        <div className="filter-group">
          <label>系别</label>
          <div className="chips">
            {ALL_TYPES.map((t) => (
              <span key={t} className={'chip' + ((filter.types || []).includes(t) ? ' on' : '')} onClick={() => toggleType(t)}>{t}</span>
            ))}
          </div>
        </div>
        <div className="filter-group">
          <label>等级</label>
          <div className="range">
            <input className="input" type="number" placeholder="最小" onChange={(e) => set({ levelMin: e.target.value })} />
            <span className="muted">~</span>
            <input className="input" type="number" placeholder="最大" onChange={(e) => set({ levelMax: e.target.value })} />
          </div>
        </div>
        <div className="filter-group">
          <label>性格</label>
          <select
            className="select"
            value={filter.natureExclude ? '__other__' : filter.nature || ''}
            onChange={(e) => {
              const v = e.target.value
              if (v === '__other__') set({ nature: '', natureExclude: HOT_NAMES.join(',') })
              else set({ nature: v, natureExclude: '' })
            }}
          >
            <option value="">全部</option>
            {HOT_NATURES.map(([n, eff]) => (
              <option key={n} value={n}>{n}（{eff}）</option>
            ))}
            <option value="__other__">其他</option>
          </select>
        </div>
        <Select label="天分" opts={options.talentRank} onChange={(v) => set({ talentRank: v })} />
        <Select label="特长" opts={options.speciality} onChange={(v) => set({ speciality: v })} />
        <Select label="奖牌" opts={options.medal} onChange={(v) => set({ medal: v })} />
        <div className="filter-group">
          <label>性别</label>
          <select className="select" onChange={(e) => set({ gender: e.target.value })}>
            <option value="">全部</option><option value="♂">♂</option><option value="♀">♀</option>
          </select>
        </div>
        <div className="filter-group">
          <label>异色/炫彩</label>
          <select className="select" onChange={(e) => set({ shiny: e.target.value })}>
            <option value="">全部</option><option value="1">仅异色</option><option value="0">非异色</option>
          </select>
        </div>
      </aside>

      <section>
        <div className="toolbar">
          <button className="btn filter-toggle" onClick={() => setCollapsed((c) => !c)}>筛选</button>
          <input className="input" placeholder="搜索昵称 / 种类" onChange={(e) => set({ search: e.target.value })} />
          <select className="select" style={{ maxWidth: 130 }} value={filter.sort} onChange={(e) => set({ sort: e.target.value })}>
            {SORTS.map((s) => <option key={s.key} value={s.key}>{s.label}</option>)}
          </select>
          <button className="btn" onClick={() => set({ order: filter.order === 'asc' ? 'desc' : 'asc' })}>{filter.order === 'asc' ? '升序' : '降序'}</button>
          <div className="spacer" />
          <span className="muted">共 {data.total} 只</span>
        </div>

        {/* 桌面表格 */}
        <div className="table-wrap">
          <table className="pets">
            <thead>
              <tr>
                <th onClick={() => sortBy('gid')}>宠物{arrow('gid')}</th>
                <th>系别</th><th>性格</th><th>特长</th><th>佩戴奖牌</th>
                <th onClick={() => sortBy('voice')}>声音{arrow('voice')}</th>
                <th onClick={() => sortBy('weight')}>体重{arrow('weight')}</th>
                <th onClick={() => sortBy('height')}>身高{arrow('height')}</th>
                <th>六维</th>
                <th onClick={() => sortBy('catchTime')}>捕捉时间{arrow('catchTime')}</th>
              </tr>
            </thead>
            <tbody>
              {data.pets.map((p) => (
                <tr key={p.gid} onClick={() => nav('/pets/' + p.gid)}>
                  <td>
                    <div className="pet-cell">
                      <div className="pet-avatar">{p.shiny ? '✨' : '🐾'}</div>
                      <div>
                        <div className="pet-name">{p.name || p.species} {p.gender}</div>
                        <div className="pet-sub">{p.species} · Lv.{p.level}</div>
                      </div>
                    </div>
                  </td>
                  <td><Types types={p.types} /></td>
                  <td>{p.nature || '-'}</td>
                  <td>{p.speciality || '无'}</td>
                  <td>{p.medal || '-'}</td>
                  <td>{p.voice}</td>
                  <td>{p.weightKg.toFixed(2)} kg</td>
                  <td>{p.heightM.toFixed(2)} m</td>
                  <td><Six p={p} /></td>
                  <td className="muted">{fmtTime(p.catchTime)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        {/* 移动卡片 */}
        <div className="cards">
          {data.pets.map((p) => (
            <div className="card" key={p.gid} onClick={() => nav('/pets/' + p.gid)}>
              <div className="card-head">
                <div className="pet-avatar">{p.shiny ? '✨' : '🐾'}</div>
                <div style={{ flex: 1 }}>
                  <div className="pet-name">{p.name || p.species} {p.gender}</div>
                  <div className="pet-sub">{p.species} · Lv.{p.level}</div>
                </div>
                <Types types={p.types} />
              </div>
              <div className="card-grid">
                <div>性格：{p.nature || '-'}</div>
                <div>特长：{p.speciality || '无'}</div>
                <div>奖牌：{p.medal || '-'}</div>
                <div>体重：{p.weightKg.toFixed(2)}kg</div>
                <div>身高：{p.heightM.toFixed(2)}m</div>
                <div>声音：{p.voice}</div>
              </div>
              <Six p={p} />
            </div>
          ))}
        </div>

        {data.pets.length === 0 && <div className="empty">没有匹配的宠物</div>}

        <div className="pager">
          <button className="btn" disabled={filter.page <= 1} onClick={() => set({ page: filter.page - 1 })}>上一页</button>
          <span className="muted">{filter.page} / {pages}</span>
          <button className="btn" disabled={filter.page >= pages} onClick={() => set({ page: filter.page + 1 })}>下一页</button>
          <select className="select" style={{ width: 110 }} value={filter.pageSize} onChange={(e) => set({ pageSize: +e.target.value })}>
            {[12, 24, 48, 96].map((n) => <option key={n} value={n}>{n} 条/页</option>)}
          </select>
        </div>
      </section>
    </div>
  )
}

function Select({ label, opts, onChange }) {
  return (
    <div className="filter-group">
      <label>{label}</label>
      <select className="select" onChange={(e) => onChange(e.target.value)}>
        <option value="">全部</option>
        {(opts || []).map((o) => <option key={o} value={o}>{o}</option>)}
      </select>
    </div>
  )
}
