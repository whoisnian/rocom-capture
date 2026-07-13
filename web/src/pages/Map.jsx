import React, { useState, useEffect, useRef, useContext, useCallback, useLayoutEffect } from 'react'
import { subscribe, getPosition } from '../api'
import { AccountContext } from '../App'

const clock = (ts) => {
  if (!ts) return '-'
  const d = new Date(ts * 1000)
  const p = (n) => String(n).padStart(2, '0')
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

const ZOOM_MIN = 1
const ZOOM_MAX = 10
// 各场景默认缩放(按细节人工调优):卡洛西亚大陆/魔法学院 5、家园室内 2、种植园 3;
// 未列出的场景回退 ZOOM_FALLBACK。键为 scene_res_cfg_id。
const ZOOM_DEFAULTS = { 10003: 5, 10018: 5, 30001: 2, 30002: 3 }
const ZOOM_FALLBACK = 5
const clamp = (v, lo, hi) => Math.max(lo, Math.min(hi, v))
// 默认缩放按场景(底图)决定;洞穴层只是叠加在底图上,进层不改缩放(与外层保持一致)。
const defaultZoom = (p) => ZOOM_DEFAULTS[p && p.sceneResId] || ZOOM_FALLBACK

// —— 平滑移动(航位推算 + 真实轨迹回放)——
// 移动包是按操作事件上报的(地面与飞行同理):持续改方向/变速时约 0.1s 一包;推住摇杆不动、
// 直线巡航或坐骑自行盘旋时输入不变,就退化成约 2.5-3s 一次心跳。若收到才画,箭头会定住再硬跳。
// 故沿用客户端给其他玩家做平滑的同一套办法:每包除位置外还带速度向量(vu/vv,归一化底图坐标每秒,
// 后端投影,见 cmd/rocom-capture),两包之间逐帧外推 pos + v*Δt。实测预测下一包实际位置的误差中位
// 仅 3cm(地面)、2.5m(飞行巡航),都远小于"收到才画"的硬跳。
//
// 心跳空窗里如果玩家其实在转弯(推住摇杆盘旋),外推必然偏出去——但那几秒实际走的路会随下一包的
// path(后端投影自 move_seg_list)补报上来:箭头届时沿这条**真实曲线**滑回正轨(GLIDE 秒内追平),
// 而不是直线跳过去。转向本身最多晚一个心跳(~3s)才可见,那是游戏的上报节奏决定的,任何画法都提前
// 不了(实测:此时直线外推仍是各策略中最准的,阻尼/定住/圆弧都更差)。见 docs/protocol.md 6。
const MAX_EXTRAP = 3.5 // 外推上限(秒):超过心跳间隔仍无新包(抓包中断/掉线)就停住,免得一路飘走
const GLIDE = 0.45 // 沿真实轨迹追平的时长(秒):这段轨迹本是过去几秒走的,快放一遍即可
const SMOOTH_TAU = 0.12 // 误差收敛时间常数(秒):新包与外推位置的落差按 e^(-Δt/τ) 抹平,而非硬跳
const SNAP_DIST = 0.005 // 落差超过底图边长的 0.5%(几十米)判为传送/换场景:直接跳过去,不做平滑
const angleDiff = (a, b) => (((a - b) % 360) + 540) % 360 - 180 // a-b 折算到 (-180,180]
const dpr = window.devicePixelRatio || 1 // 设备像素比:地图平移量按它对齐(见 applyFrame)
const easeOut = (x) => 1 - (1 - x) * (1 - x)

// pathAt 取折线上按弧长比例 r∈[0,1] 的点(cum 为累计弧长,末点即上报位置)。
const pathAt = (path, cum, r) => {
  const target = r * cum[cum.length - 1]
  let i = 1
  while (i < cum.length - 1 && cum[i] < target) i++
  const seg = cum[i] - cum[i - 1]
  const f = seg > 0 ? (target - cum[i - 1]) / seg : 1
  const a = path[i - 1], b = path[i]
  return { u: a.u + (b.u - a.u) * f, v: a.v + (b.v - a.v) * f }
}

// posAt 是锚点在其之后 dt 秒的应有位置(不含误差修正):先回放真实轨迹(有的话),再按速度外推。
const posAt = (a, dt) => {
  if (a.cum && dt < GLIDE) return pathAt(a.path, a.cum, easeOut(dt / GLIDE))
  const t = dt - (a.cum ? GLIDE : 0) // 回放结束时正好停在上报位置,由此继续外推
  const ex = Math.min(t, MAX_EXTRAP)
  return { u: a.u + a.vu * ex, v: a.v + a.vv * ex }
}

// 实时地图页:地图软件式交互——方向箭头指示朝向、可缩放平移、默认放大跟随玩家。
// 位置来自 SSE position(玩家移动时逐包推送)+ 加载时 GET /api/position。仅自己。
// 注:组件名不能叫 Map——会遮蔽内置 Map 构造器,下方 new Map() 将递归调用本组件。
export default function MapPage() {
  const account = useContext(AccountContext)
  const [pos, setPos] = useState(null) // 最近一个移动包(工具栏文字、底图选择);箭头位置另由 anchor 逐帧算出
  const [imgError, setImgError] = useState(false)
  const [layerError, setLayerError] = useState(false)
  const sceneRef = useRef(null) // 当前底图名(换底图=换场景/等级才重置缩放/跟随)
  const layerRef = useRef(null) // 当前叠加层图名(换层仅重试层图,不动缩放)

  // 视口尺寸(用于把归一化坐标换算成像素;地图边长 = min(w,h)*zoom)。
  const vpRef = useRef(null)
  const [vp, setVp] = useState({ w: 0, h: 0 })
  // 视图:zoom 缩放;follow=跟随玩家(玩家居中)。
  const [zoom, setZoom] = useState(ZOOM_FALLBACK)
  const [follow, setFollow] = useState(true)
  // 视口中心对应的地图归一化坐标。跟随时每帧跟着玩家走,故不进 state(否则每帧重渲染整页)。
  const focusRef = useRef({ u: 0.5, v: 0.5 })
  // zoom/follow/vp 放进 ref 供指针回调与逐帧循环即时读取,避免闭包过期。
  const st = useRef({ zoom, follow, vp })
  st.current = { zoom, follow, vp }

  // 逐帧外推的锚点:最近一个移动包的位置/速度/朝向 + 收到它时与画面位置的落差(cu/cv/dh)。
  const anchorRef = useRef(null)
  const dispRef = useRef(null) // 当前画面上的位置/朝向(每帧算出,供下一个包算落差)
  const worldRef = useRef(null)
  const arrowRef = useRef(null)

  // applyFrame 按当前时刻把锚点外推成画面位置,并直接写 transform(不经 React,免每帧重渲染)。
  const applyFrame = useCallback(() => {
    const a = anchorRef.current
    const { zoom: z, follow: fl, vp: v } = st.current
    if (!a || !worldRef.current) return
    const dt = (performance.now() - a.t0) / 1000
    const decay = Math.exp(-dt / SMOOTH_TAU) // 与上一帧位置的落差随时间抹平
    const p = posAt(a, dt)
    const u = p.u + a.cu * decay
    const w = p.v + a.cv * decay
    const heading = a.heading + a.dh * decay
    dispRef.current = { u, v: w, heading }
    if (fl) focusRef.current = { u, v: w }

    const f = focusRef.current
    const px = (Math.min(v.w, v.h) || 1) * z
    // 地图平移量**对齐整设备像素**。底图与洞穴层图是两个元素,浏览器绘制时各自把位置吸附到整像素;
    // 若容器按小数像素逐帧平移,两者的吸附时机会错开,看起来就是层图与底图错位抖动(Firefox 实测
    // 相对位移抖 1px;Chromium 把整个地图合成为一张纹理、平移不重绘,故几乎看不出——但不能指望)。
    // 平移量落在设备像素网格上后,两者每帧的吸附结果恒定,相对位置就锁死了。代价是地图以 1 设备像素
    // 为步进移动:跟随时地图本就只有几 px/s,肉眼无感。
    const snap = (n) => Math.round(n * dpr) / dpr
    const left = snap(v.w / 2 - f.u * px)
    const top = snap(v.h / 2 - f.v * px)
    worldRef.current.style.transform = `translate3d(${left}px, ${top}px, 0)`
    if (arrowRef.current) {
      // 箭头同样对齐设备像素:否则地图钉在像素网格上、箭头却按小数走,跟随时箭头会相对地图晃半个像素。
      // 世界 yaw(0=东/右,逆时针+)→ 默认朝上的箭头旋转 heading+90(CSS 顺时针,屏幕Y向下)。
      arrowRef.current.style.transform =
        `translate3d(${snap(left + u * px)}px, ${snap(top + w * px)}px, 0) translate(-50%,-50%) rotate(${heading + 90}deg)`
    }
  }, [])

  // 逐帧循环:即使没有新包也要跑——外推、落差收敛、跟随都是随时间连续变化的。
  useEffect(() => {
    let raf = 0
    const tick = () => { applyFrame(); raf = requestAnimationFrame(tick) }
    raf = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(raf)
  }, [applyFrame])
  // 渲染后(缩放/视口/底图变化)立刻按新参数重画一帧,免得等到下一帧才对齐。
  useLayoutEffect(applyFrame)

  const applyPos = useCallback((p) => {
    // 只更新分层的消息(后端在区域进/出事件后推,如传送落地进洞时玩家还站着不动):
    // 只叠加/撤下切片图,不碰位置锚点——否则会用旧位置重置外推,箭头往回跳。
    if (p.layerOnly) {
      const li = p.layer ? p.layer.img : ''
      if (li !== layerRef.current) {
        layerRef.current = li
        setLayerError(false)
      }
      setPos((prev) => (prev ? { ...prev, layer: p.layer || null, sceneName: p.sceneName || prev.sceneName } : prev))
      return
    }
    setPos(p)
    const sceneChanged = p.img !== sceneRef.current
    // 底图变化(换场景、家园换等级)才重置缩放/跟随并重试底图;同底图内移动不打断手动缩放/平移。
    if (sceneChanged) {
      sceneRef.current = p.img
      setImgError(false)
      setZoom(defaultZoom(p))
      setFollow(true)
    }
    // 叠加层变化(进/出/换洞穴层)只重试层图,不动缩放/跟随——与外层保持一致。
    const li = p.layer ? p.layer.img : ''
    if (li !== layerRef.current) {
      layerRef.current = li
      setLayerError(false)
    }
    if (p.u == null) { // 该场景无底图:无从投影,也就无从外推
      anchorRef.current = null
      dispRef.current = null
      return
    }
    // 新锚点:停下的包不带速度(vu/vv 缺省),外推量自然为零。
    const d = dispRef.current
    const a = {
      u: p.u, v: p.v, vu: p.vu || 0, vv: p.vv || 0, heading: p.heading || 0,
      t0: performance.now(), cu: 0, cv: 0, dh: 0,
    }
    // 心跳空窗后补报的真实轨迹(那几秒实际走过的点,末点即本包位置):预先算好累计弧长供回放取点。
    if (p.path && p.path.length >= 2) {
      const cum = [0]
      for (let i = 1; i < p.path.length; i++) {
        cum.push(cum[i - 1] + Math.hypot(p.path[i].u - p.path[i - 1].u, p.path[i].v - p.path[i - 1].v))
      }
      if (cum[cum.length - 1] > 0) { a.path = p.path; a.cum = cum }
    }
    // 与画面当前位置的落差:小落差(外推的正常误差)平滑抹平;换场景/传送这种大落差直接跳过去。
    // 有轨迹时起点是轨迹首点(箭头先并入真实路线),故落差按它算。
    const start = posAt(a, 0)
    if (d && !sceneChanged && Math.hypot(d.u - start.u, d.v - start.v) < SNAP_DIST) {
      a.cu = d.u - start.u
      a.cv = d.v - start.v
      a.dh = angleDiff(d.heading, a.heading) // 转向同样平滑,不硬掰
    }
    anchorRef.current = a
    if (sceneChanged || !d) focusRef.current = { u: p.u, v: p.v } // 新场景:视口先对准玩家
  }, [])

  useEffect(() => {
    let alive = true
    sceneRef.current = null
    layerRef.current = null
    anchorRef.current = null
    dispRef.current = null
    setPos(null); setImgError(false); setLayerError(false); setFollow(true); setZoom(ZOOM_FALLBACK)
    getPosition().then((p) => { if (alive && p) applyPos(p) }).catch(() => {})
    return () => { alive = false }
  }, [account, applyPos])

  useEffect(() => subscribe((m) => { if (m.type === 'position') applyPos(m.data) }), [account, applyPos])

  const hasMap = !!(pos && pos.u != null && pos.img && !imgError)

  // 测量视口尺寸(视口元素随 hasMap 出现/消失)。
  useEffect(() => {
    const el = vpRef.current
    if (!el) return
    const ro = new ResizeObserver(() => setVp({ w: el.clientWidth, h: el.clientHeight }))
    ro.observe(el)
    setVp({ w: el.clientWidth, h: el.clientHeight })
    return () => ro.disconnect()
  }, [hasMap])

  // 以视口某点(px,py,相对视口左上)为锚缩放:保持该点下的地图坐标不动。
  const zoomAround = useCallback((factor, px, py) => {
    const { zoom: z, vp: v } = st.current
    const f = focusRef.current
    const nz = clamp(z * factor, ZOOM_MIN, ZOOM_MAX)
    if (nz === z || !v.w) return
    const base = Math.min(v.w, v.h)
    const mapU = f.u + (px - v.w / 2) / (base * z)
    const mapV = f.v + (py - v.h / 2) / (base * z)
    setFollow(false)
    setZoom(nz)
    focusRef.current = { u: mapU - (px - v.w / 2) / (base * nz), v: mapV - (py - v.h / 2) / (base * nz) }
  }, [])

  // 指针拖动(单指/鼠标)平移 + 双指捏合缩放。
  const ptrs = useRef(new Map())
  const pinch = useRef(0)
  const onPointerDown = (e) => {
    // 点在缩放/回中控件上:不捕获指针、不启动平移,否则 setPointerCapture 会把 pointerup
    // 重定向到视口,桌面端按钮的 click 事件就不触发(移动端触摸 click 合成方式不同,不受影响)。
    if (e.target.closest?.('.map-ctrl')) return
    vpRef.current.setPointerCapture?.(e.pointerId)
    ptrs.current.set(e.pointerId, { x: e.clientX, y: e.clientY })
  }
  const onPointerMove = (e) => {
    const p = ptrs.current.get(e.pointerId)
    if (!p) return
    const prev = { x: p.x, y: p.y }
    p.x = e.clientX; p.y = e.clientY
    const pts = [...ptrs.current.values()]
    if (pts.length >= 2) {
      // 捏合:按两指距离变化缩放,锚点为两指中点(相对视口)。
      const [a, b] = pts
      const dist = Math.hypot(a.x - b.x, a.y - b.y)
      if (pinch.current) {
        const rect = vpRef.current.getBoundingClientRect()
        zoomAround(dist / pinch.current, (a.x + b.x) / 2 - rect.left, (a.y + b.y) / 2 - rect.top)
      }
      pinch.current = dist
    } else {
      // 平移:把屏幕位移换算成归一化坐标偏移(下一帧 applyFrame 即生效)。
      const { zoom: z, vp: v } = st.current
      const base = Math.min(v.w, v.h) || 1
      const f = focusRef.current
      setFollow(false)
      focusRef.current = { u: f.u - (e.clientX - prev.x) / (base * z), v: f.v - (e.clientY - prev.y) / (base * z) }
    }
  }
  const onPointerUp = (e) => {
    ptrs.current.delete(e.pointerId)
    if (ptrs.current.size < 2) pinch.current = 0
  }
  const onWheel = (e) => {
    const rect = vpRef.current.getBoundingClientRect()
    zoomAround(e.deltaY < 0 ? 1.15 : 1 / 1.15, e.clientX - rect.left, e.clientY - rect.top)
  }

  const recenter = () => setFollow(true) // 跟随打开后,下一帧 applyFrame 即把视口对准玩家

  // 地图层尺寸只跟缩放/视口走(平移与箭头位置逐帧写 transform,不在渲染里算)。
  const mapPx = (Math.min(vp.w, vp.h) || 1) * zoom

  return (
    <div className="map-page">
      <div className="toolbar">
        <h3 style={{ margin: 0 }}>实时地图</h3>
        <span className="muted toolbar-hint">当前账号自己的实时位置与朝向(玩家移动时更新)</span>
        <div className="spacer" />
        {pos && (
          <span className="map-loc">
            <b>{pos.sceneName || '未知场景'}</b>
            <span className="muted"> ({pos.x}, {pos.y})</span>
            <span className="muted"> {clock(pos.ts)}</span>
          </span>
        )}
      </div>

      {!pos && <div className="empty">等待位置数据…(需后端正在抓包/回放,且玩家已登录并移动过)</div>}

      {pos && (hasMap ? (
        <div
          className="map-vp" ref={vpRef}
          onPointerDown={onPointerDown} onPointerMove={onPointerMove}
          onPointerUp={onPointerUp} onPointerCancel={onPointerUp} onWheel={onWheel}
        >
          <div className="map-world" ref={worldRef} style={{ width: mapPx, height: mapPx }}>
            <img className="map-base" src={`/img/bigmap/${pos.img}.webp`} alt={pos.sceneName}
              draggable={false} onError={() => setImgError(true)} />
            {pos.layer && !layerError && (
              <img className="map-layer" src={`/img/bigmap/${pos.layer.img}.webp`} alt="" draggable={false}
                onError={() => setLayerError(true)}
                style={{
                  left: pos.layer.u0 * mapPx, top: pos.layer.v0 * mapPx,
                  width: (pos.layer.u1 - pos.layer.u0) * mapPx, height: (pos.layer.v1 - pos.layer.v0) * mapPx,
                }} />
            )}
          </div>
          <div className="map-arrow" ref={arrowRef}>
            <svg viewBox="0 0 24 24" width="30" height="30">
              <path d="M12 2 L20 21 L12 16 L4 21 Z" fill="var(--red)" stroke="#fff" strokeWidth="1.5" strokeLinejoin="round" />
            </svg>
          </div>
          <div className="map-ctrl">
            <button className="map-btn" title="放大" onClick={() => zoomAround(1.4, vp.w / 2, vp.h / 2)}>＋</button>
            <button className="map-btn" title="缩小" onClick={() => zoomAround(1 / 1.4, vp.w / 2, vp.h / 2)}>－</button>
            <button className={'map-btn' + (follow ? ' on' : '')} title="回到当前位置" onClick={recenter}>◎</button>
          </div>
        </div>
      ) : (
        <div className="map-nomap">
          <div className="map-nomap-name">{pos.sceneName || '未知场景'}</div>
          <div className="muted">该场景无底图,仅显示坐标</div>
          <div className="map-coords">X {pos.x} · Y {pos.y} · Z {pos.z}</div>
        </div>
      ))}
    </div>
  )
}
