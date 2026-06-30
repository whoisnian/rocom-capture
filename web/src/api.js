// REST 封装与 SSE 订阅。

export async function getPets(params) {
  const q = new URLSearchParams()
  Object.entries(params || {}).forEach(([k, v]) => {
    if (v !== undefined && v !== null && v !== '' && !(Array.isArray(v) && v.length === 0)) {
      q.set(k, Array.isArray(v) ? v.join(',') : v)
    }
  })
  const r = await fetch('/api/pets?' + q.toString())
  return r.json()
}

export async function getPet(gid) {
  const r = await fetch('/api/pets/' + gid)
  if (!r.ok) throw new Error('not found')
  return r.json()
}

export async function getEvents(params) {
  const q = new URLSearchParams(params || {})
  const r = await fetch('/api/events?' + q.toString())
  return r.json()
}

export async function clearEvents() {
  await fetch('/api/events', { method: 'DELETE' })
}

export async function getFilterOptions() {
  const r = await fetch('/api/filter-options')
  return r.json()
}

export async function getStats() {
  const r = await fetch('/api/stats')
  return r.json()
}

export async function getMedals() {
  const r = await fetch('/api/medals')
  return r.json()
}

export async function getBoxes() {
  const r = await fetch('/api/boxes')
  return r.json()
}

export async function getTeams() {
  const r = await fetch('/api/teams')
  return r.json()
}

// getEvolution 返回某 petbase(base_conf_id)所属进化链(按阶段升序)。
export async function getEvolution(base) {
  const r = await fetch('/api/evolution?base=' + base)
  return r.json()
}

// getPetPage 查询某宠物在指定筛选/排序下所处页码。
export async function getPetPage(gid, params) {
  const q = new URLSearchParams()
  Object.entries(params || {}).forEach(([k, v]) => {
    if (v !== undefined && v !== null && v !== '' && !(Array.isArray(v) && v.length === 0)) {
      q.set(k, Array.isArray(v) ? v.join(',') : v)
    }
  })
  q.set('gid', gid)
  const r = await fetch('/api/pet-page?' + q.toString())
  return r.json()
}

// subscribe 订阅 SSE，onMsg 收到 {type, data}。返回取消函数。
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
