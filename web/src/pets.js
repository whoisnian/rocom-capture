// 本机桌宠:姊妹项目 rocom-pets 的桌面客户端开着「本地监听」时,色卡直接唤起它的预览窗口,
// 不再跳 rkpet 网页。接口是它那边 src/control/http.rs 定的:
//   GET  /          探活,回 {ok, app: "rocom-pets", version}
//   POST /preview   JSON {pet: base_conf_id, shiny, glass_type, glass_value},开一个不带列表的预览窗口;
//                   污染不在协议字段里,改送它认的外观写法 mutation(「异色+炫彩:污染」)
// 地址来自启动参数 -pets-url(经 GET /api/config 下发);没设就整个不探,色卡只跳 rkpet。
//
// 探测**整页只做一次**(页面打开时),结论存在模块里;之后点色卡按它选路,不再重探。
// 探不通的原因都归成「没有」:桌宠没开/没开监听、本页来源不在它的「允许跨域」单子上(403 不带 CORS 头,
// fetch 直接失败)、浏览器的本地网络访问权限被拒。手机上不探:桌宠只有桌面版,探了只会白弹一次权限询问。
import { getConfig } from './api'

const PROBE_TIMEOUT = 1500

let petsURL = ''
let available = false
let probed = false
const listeners = new Set()

function set(v) {
  if (available === v) return
  available = v
  listeners.forEach((f) => f())
}

// probePets 发那一次探测;重复调用不重发。
export function probePets() {
  if (probed) return
  probed = true
  if (/Android|iPhone|iPad|Mobile/i.test(navigator.userAgent)) return
  getConfig()
    .then((c) => {
      petsURL = (c && c.petsURL) || ''
      if (!petsURL) return null
      return fetch(petsURL + '/', { signal: AbortSignal.timeout(PROBE_TIMEOUT) })
        .then((r) => (r.ok ? r.json() : null))
    })
    .then((j) => set(!!(j && j.ok && j.app === 'rocom-pets')))
    .catch(() => {})
}

// subscribePets / petsAvailable 给 useSyncExternalStore 用:探测结果晚于首屏到达时色卡跟着换。
export function subscribePets(f) {
  listeners.add(f)
  return () => listeners.delete(f)
}
export const petsAvailable = () => available

// openPetsPreview 让桌宠开这只的预览,成功返回 true。连不上(探完之后桌宠退了)就记成「没有」,
// 后面的点击直接走 rkpet;桌宠回了错误(比如它的宠物链清单里没这只)只算这一次失败。
export async function openPetsPreview(p) {
  try {
    const r = await fetch(petsURL + '/preview', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(p.glass && p.glass.pollution ? {
        pet: p.baseConfId,
        mutation: (p.shiny ? '异色+' : '') + '炫彩:污染',
      } : {
        pet: p.baseConfId,
        shiny: !!p.shiny,
        glass_type: p.glassType,
        glass_value: p.glassValue,
      }),
    })
    return r.ok
  } catch {
    set(false)
    return false
  }
}
