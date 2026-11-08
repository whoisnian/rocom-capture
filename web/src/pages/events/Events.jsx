import React, { useState, useEffect, useContext } from 'react'
import { getEvents, getEventCount, clearEvents, subscribe } from '../../api'
import { useBattle } from '../../battle'
import { AccountContext } from '../../context'
import { useStoredState, useStoredFlag, useStoredJSON } from '../../hooks/useStoredState'
import { useWakeLock, wakeLockSupported } from '../../hooks/useWakeLock'
import { Avatar } from '../../components/avatar'
import { Marks, Blood, Gender } from '../../components/badges'
import { PetDetailModal } from '../../components/PetDetailModal'
import { locTag, fmtTime, voiceHot, pctHot } from '../../utils/format'
import { sanitizeRules, isHighlight, NOTABLE_BLOODS } from './highlight'
import RulePanel from './RulePanel'

export default function Events() {
  const account = useContext(AccountContext)
  const [events, setEvents] = useState([])
  // total=自上次清空以来累计获得的宠物数(即列表最新一条的序号);列表可能因上限被截断,
  // 故序号以后端总数为准:列表第 i 条(0=最新)序号 = total - i。
  const [total, setTotal] = useState(0)
  const [rules, setRules] = useStoredJSON(localStorage, 'hlRules', [], sanitizeRules)
  // 多规则联合逻辑:'and'=需全部命中(默认)、'or'=任一命中
  const [mode, setMode] = useStoredState(localStorage, 'hlMode', (s) => (s === 'or' ? 'or' : 'and'), (v) => v)
  // 规则抽屉开合(仅移动端生效,桌面侧栏常驻)
  const [collapsed, setCollapsed] = useStoredFlag(sessionStorage, 'hlCollapsed', true)
  // 仅展示命中高亮规则的事件
  const [onlyHl, setOnlyHl] = useStoredFlag(localStorage, 'onlyHl', false)
  // 屏幕常亮开关(Screen Wake Lock)
  const [keepAwake, setKeepAwake] = useStoredFlag(localStorage, 'keepAwake', false)
  // 战斗信息开关与最近一场战斗(战斗数据由 App 层常驻订阅,见 battle.jsx)
  const [showBattle, setShowBattle] = useStoredFlag(localStorage, 'showBattle', false)
  const battle = useBattle()
  const [detailGid, setDetailGid] = useState(null) // 详情弹窗的 gid(null=关闭)
  useWakeLock(keepAwake)

  useEffect(() => {
    // 后端只记录获得宠物事件(放生/赠送出等减少事件不入库),故无需再按类型过滤。
    getEvents({ limit: 100 }).then((e) => setEvents(e || [])).catch(() => {})
    getEventCount().then((r) => setTotal(r?.count || 0)).catch(() => {})
    return subscribe((m) => {
      if (m.type !== 'event') return
      if (m.account && m.account !== account) return // 只认当前账号的事件
      setEvents((prev) => [m.data, ...prev].slice(0, 300))
      setTotal((n) => n + 1)
    })
  }, [account])

  // 点选条目:已选则移除、未选则添加(即时生效,无需「添加」按钮);addRule 只添加(去重)。
  const hasRule = (field, value) => rules.some((r) => r.field === field && r.value === value)
  const addRule = (field, value) => { if (!hasRule(field, value)) setRules((r) => [...r, { field, value }]) }
  const toggleRule = (field, value) => setRules((r) => hasRule(field, value)
    ? r.filter((x) => !(x.field === field && x.value === value))
    : [...r, { field, value }])

  // 清空事件历史(后端删除 + 前端清列表并将计数归零,下次获得从 1 重新计)
  const clearAll = () => {
    if (!window.confirm('确定清空所有事件历史?计数将从头开始。')) return
    clearEvents().then(() => { setEvents([]); setTotal(0) }).catch(() => {})
  }

  return (
    <div className="list-layout">
      <RulePanel
        rules={rules} mode={mode} setMode={setMode}
        addRule={addRule} toggleRule={toggleRule}
        collapsed={collapsed} onClose={() => setCollapsed(true)}
      />

      <section>
        <div className="toolbar list-toolbar event-head">
          <button className="btn filter-toggle" onClick={() => setCollapsed(false)}>规则{rules.length ? ` (${rules.length})` : ''}</button>
          <strong className="event-title">捕获事件</strong>
          <span className="muted">共 {total} 只</span>
          <div className="spacer" />
          {/* 操作统一为单图标,含义见各自 title */}
          <button className={'btn btn-icon' + (showBattle ? ' primary' : '')} onClick={() => setShowBattle((v) => !v)}
            title="在事件列表顶部展示对手信息">⚔</button>
          <button className={'btn btn-icon' + (onlyHl ? ' primary' : '')} onClick={() => setOnlyHl((v) => !v)}
            title="仅展示命中高亮规则的事件">{onlyHl ? '★' : '☆'}</button>
          {wakeLockSupported
            ? <button className={'btn btn-icon' + (keepAwake ? ' primary' : '')} onClick={() => setKeepAwake((v) => !v)}
                title="阻止屏幕熄灭,方便盯着高亮提醒">☀</button>
            : <button className="btn btn-icon" disabled title="当前非 HTTPS/localhost 环境,浏览器不提供屏幕常亮">☀</button>}
          <button className="btn btn-icon" disabled={events.length === 0} onClick={clearAll} title="清空事件历史">🗑</button>
        </div>
        <div className="event-list">
          {/* 战斗信息行:开关开启时固定在列表顶部;未对战为空槽,结束后置灰保留 */}
          {showBattle && <BattleRow battle={battle} account={account} rules={rules} mode={mode} />}
          {/* 先按原始下标算序号(#total-i)与高亮,再按"仅看高亮"过滤,保证序号不因过滤错位 */}
          {events
            .map((ev, i) => ({ ev, i, hl: isHighlight(ev.pet, rules, mode) }))
            .filter(({ hl }) => !onlyHl || hl)
            .map(({ ev, i, hl }) => (
              <EventItem key={ev.id || ev.gid + '-' + ev.time} ev={ev} seq={total - i} hl={hl}
                onOpen={() => ev.gid && setDetailGid(ev.gid)} />
            ))}
          {events.length === 0 && <div className="empty">暂无事件。游戏中捕捉/孵蛋新宠物后将实时出现在这里。</div>}
          {events.length > 0 && onlyHl && !events.some((ev) => isHighlight(ev.pet, rules, mode)) &&
            <div className="empty">当前没有命中高亮规则的事件。{rules.length === 0 ? '请先添加高亮规则。' : ''}</div>}
        </div>
      </section>

      {detailGid != null && <PetDetailModal gid={detailGid} onClose={() => setDetailGid(null)} />}
    </div>
  )
}

// BattleRow 是捕获事件页顶部预留的战斗信息行:
// enter 展示等级/种类/性别/体重/声音/性格(普通对战推不出性格,显示未知)与已知的异色/炫彩;
// 龙棋士的异色由后端按护盾倍率预判,enter 即可见,血脉/炫彩仍要等 reveal(enter 的合成 Pet
// 已由后端清掉这些)。名称行按需带血脉徽标(副行不显示血脉文字)。与事件条目一样按高亮规则
// 点亮。end(或超过 30 分钟没有新消息)置灰保留,等待下一场。数据只在前端 localStorage,
// 由 App 层常驻订阅(见 battle.jsx),停在别的页面时也照常记录,战斗中切回本页不会漏。
function BattleRow({ battle, account, rules, mode }) {
  // 切账号后不串用上一账号的战斗信息(旧版本存的数据没有 account 字段,照常显示)。
  if (!battle || (battle.account && account && battle.account !== account)) {
    return <div className="event battle-event empty"><span className="battle-hint">尚未进行对战</span></div>
  }
  const p = battle.pet || {}
  const revealed = !!battle.revealed
  const stale = !battle.time || Date.now() / 1000 - battle.time > 1800
  const done = battle.stage === 'end' || stale
  // 阶段标签占用原来 event-time 的位置(战斗信息不显示时间)
  const stage = done ? '已结束' : revealed ? '已打破' : '对战中'
  const species = battle.species || p.species || ''
  const hl = isHighlight(p, rules, mode)
  return (
    <div className={'event battle-event' + (hl ? ' hl' : '') + (done ? ' done' : '')}>
      <Avatar p={p} />
      <div className="event-body">
        <div className="event-row">
          {/* 等级放在事件序号的位置 */}
          <span className="event-seq muted">{'Lv.'}{p.level ?? '-'}</span>
          <span className="pet-name">
            {species || '未知'}
            <Gender g={p.gender} />
            <Marks p={p} />
            {NOTABLE_BLOODS.includes(p.blood) && <Blood p={p} iconOnly />}
          </span>
          <span className={'battle-stage' + (revealed && !done ? ' reveal' : '')}>{stage}</span>
        </div>
        <div className="pet-sub">
          {p.nature || '未知'}
          {' · W '}<span className={pctHot(p.weightPct)}>{p.weightPct != null ? `${Math.round(p.weightPct)}%` : '-'}</span>
          {' · V '}<span className={voiceHot(p.voice)}>{p.voice ?? '-'}</span>
        </div>
      </div>
    </div>
  )
}

// EventItem 单条捕获事件:头像 + 名称徽标行 + 关键数值摘要;点击打开详情弹窗。
function EventItem({ ev, seq, hl, onOpen }) {
  const p = ev.pet
  return (
    <div className={'event' + (hl ? ' hl' : '')} onClick={onOpen}>
      <Avatar p={p} />
      <div className="event-body">
        <div className="event-row">
          <span className="event-seq muted">#{seq}</span>
          <span className="pet-name">
            {p?.name || p?.species}
            <Gender g={p?.gender} />
            <Marks p={p} />
            {NOTABLE_BLOODS.includes(p?.blood) && <Blood p={p} iconOnly />}
          </span>
          <span className="event-time muted">{fmtTime(ev.time)}</span>
        </div>
        <div className="pet-sub">
          {p?.nature}
          {p?.speciality && p.speciality !== '无' ? ` · ${p.speciality}` : ''}
          {' · W '}<span className={pctHot(p?.weightPct)}>{p?.weightPct != null ? `${Math.round(p.weightPct)}%` : '-'}</span>
          {' · V '}<span className={voiceHot(p?.voice)}>{p?.voice ?? '-'}</span>
          {' · '}{locTag(p)}
        </div>
      </div>
    </div>
  )
}
