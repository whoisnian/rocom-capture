import React, { useSyncExternalStore } from 'react'
import { imgURL } from './icons'
import { openPetsPreview, petsAvailable, subscribePets } from '../pets'

// 炫彩色卡:复刻游戏内点开炫彩标记弹出的那张小卡(客户端 UMG_Pet_DazzlingTips_C),
// 素材与配色由后端 gamedata.GlassCard 下发(素材见 rocom-parse docs/data.md 的炫彩段)。两种画法:
//   隐藏炫彩(赛季款/常驻款)→ card 就是整张烤好的图,配色已画进去,原样贴上;
//   普通炫彩 → 三层叠:base(圆角矩形)着 color2 打底 → wave(上半波浪)着 color1
//              → card(粒子层)原色压最上。base/wave 是纯白+alpha 的遮罩,用 CSS mask 上色。
// 污染血脉的宠物也有一张(g.pollution,不是炫彩):游戏图鉴里与炫彩卡摆在一排,拼法同普通炫彩。

// rkpet 是姊妹项目 rocom-pets 的站点,能把这只的**模型**按同一套炫彩渲出来。
// `/api/link` 是它给外部工具开的接口:送游戏侧编号(形态 + 异色/炫彩),302 到对应的展示页。
// 「形态编号 → 包名/资产名」与它那边 look 的写法都由它自己管,这里不抄。
const RKPET_LINK = 'https://rkpet.whoisnian.com/api/link'

// maskStyle 把一张遮罩图 + 一个颜色变成一层:颜色铺满,再按遮罩的 alpha 裁形。
// 两个前缀都写死在内联 style 上:详情页导出 PNG 走 html-to-image,它只认内联的
// mask-image / -webkit-mask-image(会把 url 转成 data URI),写在 CSS 类里导出会丢形状。
function maskStyle(src, color) {
  const url = `url(${imgURL(src)})`
  return { background: color, maskImage: url, WebkitMaskImage: url }
}

// rkpetURL 拼这只在 rkpet 的展示链接。缺形态编号或(炫彩卡)缺 glass_info 原始编号时不给链接 ——
// 后者是**老库**才会缺(glassType/glassValue 是后加的字段,早先入库的行里没有),
// 下次登录全量快照重写那一行就补齐了;这期间卡照画,只是点不动。污染卡不看 glass_info,送 pollution=1。
function rkpetURL(p) {
  const pollution = p.glass && p.glass.pollution
  if (!p.baseConfId || (!pollution && !p.glassType)) return null
  const q = new URLSearchParams({ petbase: String(p.baseConfId) })
  if (p.shiny) q.set('shiny', '1')
  if (pollution) q.set('pollution', '1')
  else q.set('glass', `${p.glassType}:${p.glassValue}`)
  return `${RKPET_LINK}?${q}`
}

// GlassCard 色卡,摆在详情页身份区(昵称行 + 天分/系别行)的右侧,竖向跨这两行。
// 界面上只留卡,**提示也只说点了会怎样**:外观名与赛季归属由左边名称行那枚炫彩标记
// (badges.jsx 的 Marks)负责,这里再写一遍就是同一句话挂两处。
// 点击看同一套炫彩的 3D 效果,两条路(见 pets.js):
//   本机桌宠在监听 → 拦下链接,叫桌宠开预览窗口;叫不动就当场退回 rkpet 链接;
//   否则 → 就是个跳 rkpet 的普通链接,不点不会有任何外部请求(这是个局域网工具,没网也照常用)。
// 链接给不出时(老库缺 glass_info 编号)两条路都不走,连提示也不给。缺素材时不渲染。
// 污染卡同一套:图层同普通炫彩,点了看的是污染外观。
export function GlassCard({ p }) {
  const local = useSyncExternalStore(subscribePets, petsAvailable)
  const g = p && p.glass
  if (!g || !g.card) return null
  const href = rkpetURL(p)
  const layers = (
    <>
      {!g.hidden && g.base && g.color2 && <span className="glass-layer" style={maskStyle(g.base, g.color2)} />}
      {!g.hidden && g.wave && g.color1 && <span className="glass-layer glass-wave" style={maskStyle(g.wave, g.color1)} />}
      <img src={imgURL(g.card)} alt={g.name} />
    </>
  )
  if (!href) return <div className="glass-card">{layers}</div>
  // 回退时的 window.open 在 await 之后:本机回环一般几毫秒就回,仍在点击的临时激活窗口内,不会被当弹窗拦。
  const onClick = local ? async (e) => {
    e.preventDefault()
    if (!(await openPetsPreview(p))) window.open(href, '_blank', 'noopener,noreferrer')
  } : undefined
  return (
    <a className="glass-card" href={href} target="_blank" rel="noopener noreferrer" onClick={onClick}
      title={local ? '在桌宠中预览' : '跳转到 rkpet 查看效果'}>{layers}</a>
  )
}
