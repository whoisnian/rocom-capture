import React, { useState, useEffect, useRef } from 'react'
import { subscribe } from '../api'
import { fmtTime } from '../components/bits'

export default function Debug() {
  const [rows, setRows] = useState([])
  const [paused, setPaused] = useState(false)
  const [filter, setFilter] = useState('')
  const pausedRef = useRef(false)
  pausedRef.current = paused

  useEffect(() => {
    return subscribe((m) => {
      if (m.type !== 'debug' || pausedRef.current) return
      setRows((r) => [m.data, ...r].slice(0, 800))
    })
  }, [])

  const shown = filter
    ? rows.filter((r) => (r.name || '').toLowerCase().includes(filter.toLowerCase()) || (r.opcode || '').includes(filter))
    : rows

  return (
    <div>
      <div className="toolbar">
        <h3 style={{ margin: 0 }}>游戏事件流</h3>
        <span className="muted">实时展示所有应用层消息(opcode)</span>
        <div className="spacer" />
        <input className="input" style={{ maxWidth: 220 }} placeholder="过滤名称 / opcode" value={filter} onChange={(e) => setFilter(e.target.value)} />
        <button className="btn" onClick={() => setPaused((p) => !p)}>{paused ? '继续' : '暂停'}</button>
        <button className="btn" onClick={() => setRows([])}>清空</button>
      </div>

      <div className="table-wrap" style={{ display: 'block' }}>
        <table className="debug-table">
          <tbody>
            {shown.map((r, i) => (
              <tr key={i}>
                <td className="muted">{fmtTime(r.time)}</td>
                <td className={r.dir === 'c2s' ? 'dir-c2s' : 'dir-s2c'}>{r.dir}</td>
                <td className="muted">{(r.account || '').replace(/^ip:/, '')}</td>
                <td>{r.opcode}</td>
                <td>{r.name}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {shown.length === 0 && <div className="empty">等待事件…(需要后端正在抓包/回放)</div>}
      </div>
    </div>
  )
}
