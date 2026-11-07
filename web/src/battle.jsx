import React, { createContext, useContext, useEffect } from 'react'
import { subscribe } from './api'
import { useStoredJSON } from './hooks/useStoredState'

// 战斗信息(捕获事件页顶部的对手行):enter/reveal 带完整对手数据,end 只带阶段。
// 订阅挂在 App 层而不是捕获事件页里——浏览器停在别的页面(如实时地图)时也要照常收下,
// 否则切到捕获事件页时那场战斗的 enter 早已过去,页面上只能显示空行。
// 数据仍只存前端 localStorage(键名不变),后端不持久化。
const BattleContext = createContext(null)

// 战斗信息的阶段:enter=进战斗,reveal=龙棋士护盾破后宠物现身,end=战斗结束。
const BATTLE_STAGES = ['enter', 'reveal', 'end']

// sanitizeBattle 读取 localStorage 时丢弃旧的/损坏的数据。
function sanitizeBattle(v) {
  if (!v || typeof v !== 'object' || !BATTLE_STAGES.includes(v.stage)) return null
  return v
}

export function BattleProvider({ account, children }) {
  const [battle, setBattle] = useStoredJSON(localStorage, 'battleInfo', null, sanitizeBattle)
  useEffect(() => subscribe((m) => {
    if (m.type !== 'battle') return
    if (m.account && m.account !== account) return // 只认当前账号的战斗
    const acc = m.account || ''
    if (m.data.stage === 'end') {
      // 结束只带 stage/time:保留上一份宠物数据置灰,不清空。
      setBattle((prev) => (prev && (!prev.account || prev.account === acc)
        ? { ...prev, stage: 'end', time: m.data.time, account: acc || prev.account }
        : null))
    } else {
      setBattle({ ...m.data, account: acc }) // 记下账号,切账号时不串用
    }
  }), [account])
  return <BattleContext.Provider value={battle}>{children}</BattleContext.Provider>
}

export const useBattle = () => useContext(BattleContext)
