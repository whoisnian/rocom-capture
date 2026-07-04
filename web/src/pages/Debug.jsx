import React, { useState, useEffect, useRef, useContext } from 'react'
import { subscribe } from '../api'
import { fmtTime } from '../components/bits'
import { AccountContext } from '../App'

// 默认忽略高频且无分析价值的场景 NPC 位置同步(每秒多条,会淹没事件流)。
const DEFAULT_IGNORED = ['ZONE_SCENE_SET_NPC_POS_REQ', 'ZONE_SCENE_SET_NPC_POS_RSP', 'ZONE_SCENE_PLAY_ACTS_NOTIFY']

// 忽略列表持久化:localStorage 无该键时用默认值;用户清空后存 [] 且不再回落默认。
function loadIgnored() {
  const v = localStorage.getItem('debugIgnore')
  if (v === null) return DEFAULT_IGNORED
  try { return JSON.parse(v) } catch { return DEFAULT_IGNORED }
}

export default function Debug() {
  const account = useContext(AccountContext)
  const [rows, setRows] = useState([])
  const [paused, setPaused] = useState(false)
  const [filter, setFilter] = useState('')
  const [ignored, setIgnored] = useState(loadIgnored)
  // 忽略集合放进 ref,避免每次增删都重新订阅;订阅回调里以名称精确匹配丢弃。
  const ignoredRef = useRef(ignored)
  ignoredRef.current = ignored

  useEffect(() => { localStorage.setItem('debugIgnore', JSON.stringify(ignored)) }, [ignored])

  // 仅本页(且未暂停)才订阅高频 debug 流;暂停 = 关闭连接,服务端随之停止推送,而非前端丢弃。
  // account 变化时重订阅(切换账号后只看新账号的流量)。
  useEffect(() => {
    if (paused) return
    return subscribe((m) => {
      if (m.type !== 'debug') return
      if (ignoredRef.current.includes(m.data.name)) return // 忽略的 opcode 直接不入缓冲,避免挤掉有用事件
      setRows((r) => [m.data, ...r].slice(0, 800))
    }, { debug: true })
  }, [paused, account])

  const addIgnore = (name) => { if (name) setIgnored((s) => (s.includes(name) ? s : [...s, name])) }
  const removeIgnore = (name) => setIgnored((s) => s.filter((n) => n !== name))

  const shown = filter
    ? rows.filter((r) => (r.name || '').toLowerCase().includes(filter.toLowerCase()) || (r.opcode || '').includes(filter))
    : rows

  return (
    <div>
      <div className="toolbar">
        <h3 style={{ margin: 0 }}>游戏事件流</h3>
        <span className="muted">实时展示当前账号的应用层消息(opcode);离开本页或暂停即停止拉取</span>
        <div className="spacer" />
        <input className="input" style={{ maxWidth: 220 }} placeholder="过滤名称 / opcode" value={filter} onChange={(e) => setFilter(e.target.value)} />
        <button className="btn" onClick={() => setPaused((p) => !p)}>{paused ? '继续' : '暂停'}</button>
        <button className="btn" onClick={() => setRows([])}>清空</button>
      </div>

      {ignored.length > 0 && (
        <div className="ignore-bar">
          <span className="muted">已忽略:</span>
          {ignored.map((n) => (
            <span key={n} className="chip on" title="点击取消忽略" onClick={() => removeIgnore(n)}>{n} ✕</span>
          ))}
        </div>
      )}

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
                <td><button className="btn-ignore" title="忽略该事件" onClick={() => addIgnore(r.name)}>🚫</button></td>
              </tr>
            ))}
          </tbody>
        </table>
        {shown.length === 0 && <div className="empty">等待事件…(需要后端正在抓包/回放)</div>}
      </div>
    </div>
  )
}
