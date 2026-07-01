// REST 封装与 SSE 订阅。

// 当前选中账号(玩家 user_id 派生的 key,如 "role:839694713")。持久化到 localStorage,
// 所有 REST 请求自动带上 ?account=;为空则由后端回退到最近活跃账号。
let currentAccount = localStorage.getItem('account') || ''
export function getCurrentAccount() { return currentAccount }
export function setCurrentAccount(a) {
  currentAccount = a || ''
  if (currentAccount) localStorage.setItem('account', currentAccount)
  else localStorage.removeItem('account')
}

// buildQuery 把参数对象拼成查询串并附加当前 account。
function buildQuery(params) {
  const q = new URLSearchParams()
  Object.entries(params || {}).forEach(([k, v]) => {
    if (v !== undefined && v !== null && v !== '' && !(Array.isArray(v) && v.length === 0)) {
      q.set(k, Array.isArray(v) ? v.join(',') : v)
    }
  })
  if (currentAccount) q.set('account', currentAccount)
  return q.toString()
}

export async function getPets(params) {
  const r = await fetch('/api/pets?' + buildQuery(params))
  return r.json()
}

export async function getPet(gid) {
  const r = await fetch('/api/pets/' + gid + '?' + buildQuery())
  if (!r.ok) throw new Error('not found')
  return r.json()
}

export async function getEvents(params) {
  const r = await fetch('/api/events?' + buildQuery(params))
  return r.json()
}

export async function clearEvents() {
  await fetch('/api/events?' + buildQuery(), { method: 'DELETE' })
}

export async function getFilterOptions() {
  const r = await fetch('/api/filter-options?' + buildQuery())
  return r.json()
}

export async function getStats() {
  const r = await fetch('/api/stats?' + buildQuery())
  return r.json()
}

export async function getMedals() {
  const r = await fetch('/api/medals')
  return r.json()
}

export async function getBoxes() {
  const r = await fetch('/api/boxes?' + buildQuery())
  return r.json()
}

export async function getTeams() {
  const r = await fetch('/api/teams?' + buildQuery())
  return r.json()
}

// getAccounts 返回已知账号列表 [{account,name,petCount}](账号切换下拉用,不带 account 参数)。
export async function getAccounts() {
  const r = await fetch('/api/accounts')
  return r.json()
}

// getEvolution 返回某 petbase(base_conf_id)所属进化链(按阶段升序)。
export async function getEvolution(base) {
  const r = await fetch('/api/evolution?base=' + base)
  return r.json()
}

// getPetPage 查询某宠物在指定筛选/排序下所处页码。
export async function getPetPage(gid, params) {
  const r = await fetch('/api/pet-page?' + buildQuery({ ...params, gid }))
  return r.json()
}

// subscribe 订阅 SSE，onMsg 收到 {type, account, data}。返回取消函数。
// 按账号过滤由各页面自行决定(account 为空的调试/全局消息应放行),故此处不过滤。
export function subscribe(onMsg) {
  const es = new EventSource('/api/stream')
  es.onmessage = (e) => {
    try {
      onMsg(JSON.parse(e.data))
    } catch {
      /* ignore */
    }
  }
  return () => es.close()
}

// 固定系别列表(用于筛选)。
export const ALL_TYPES = ['普', '草', '火', '水', '光', '地', '冰', '龙', '电', '毒', '虫', '武', '翼', '萌', '幽', '恶', '机械', '幻']
