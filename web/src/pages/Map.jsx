import React, { useState, useEffect, useRef, useContext, useCallback } from 'react'
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

// 实时地图页:地图软件式交互——方向箭头指示朝向、可缩放平移、默认放大跟随玩家。
// 位置来自 SSE position(移动时推送)+ 加载时 GET /api/position。仅自己。
// 注:组件名不能叫 Map——会遮蔽内置 Map 构造器,下方 new Map() 将递归调用本组件。
export default function MapPage() {
  const account = useContext(AccountContext)
  const [pos, setPos] = useState(null)
  const [imgError, setImgError] = useState(false)
  const [layerError, setLayerError] = useState(false)
  const sceneRef = useRef(null) // 当前底图名(换底图=换场景/等级才重置缩放/跟随)
  const layerRef = useRef(null) // 当前叠加层图名(换层仅重试层图,不动缩放)

  // 视口尺寸(用于把归一化坐标换算成像素;地图边长 = min(w,h)*zoom)。
  const vpRef = useRef(null)
  const [vp, setVp] = useState({ w: 0, h: 0 })
  // 视图:zoom 缩放;focus 为视口中心对应的地图归一化坐标;follow=跟随玩家(玩家居中)。
  const [zoom, setZoom] = useState(ZOOM_FALLBACK)
  const [focus, setFocus] = useState({ u: 0.5, v: 0.5 })
  const [follow, setFollow] = useState(true)
  // follow/zoom 放进 ref 供指针/滚轮回调即时读取,避免闭包过期。
  const st = useRef({ zoom, focus, follow, vp })
  st.current = { zoom, focus, follow, vp }

  const applyPos = useCallback((p) => {
    setPos(p)
    // 底图变化(换场景、家园换等级)才重置缩放/跟随并重试底图;同底图内移动不打断手动缩放/平移。
    if (p.img !== sceneRef.current) {
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
    if (p.u != null && st.current.follow) setFocus({ u: p.u, v: p.v })
  }, [])

  useEffect(() => {
    let alive = true
    sceneRef.current = null
    layerRef.current = null
    setPos(null); setImgError(false); setLayerError(false); setFollow(true); setZoom(ZOOM_FALLBACK)
    getPosition().then((p) => { if (alive && p) applyPos(p) }).catch(() => {})
    return () => { alive = false }
  }, [account, applyPos])

  useEffect(() => subscribe((m) => { if (m.type === 'position') applyPos(m.data) }), [account, applyPos])

  // 测量视口尺寸。
  useEffect(() => {
    const el = vpRef.current
    if (!el) return
    const ro = new ResizeObserver(() => setVp({ w: el.clientWidth, h: el.clientHeight }))
    ro.observe(el)
    setVp({ w: el.clientWidth, h: el.clientHeight })
    return () => ro.disconnect()
  }, [pos, imgError])

  const hasMap = pos && pos.u != null && pos.img && !imgError

  // 以视口某点(px,py,相对视口左上)为锚缩放:保持该点下的地图坐标不动。
  const zoomAround = useCallback((factor, px, py) => {
    const { zoom: z, focus: f, vp: v } = st.current
    const nz = clamp(z * factor, ZOOM_MIN, ZOOM_MAX)
    if (nz === z || !v.w) return
    const base = Math.min(v.w, v.h)
    const mapU = f.u + (px - v.w / 2) / (base * z)
    const mapV = f.v + (py - v.h / 2) / (base * z)
    setFollow(false)
    setZoom(nz)
    setFocus({ u: mapU - (px - v.w / 2) / (base * nz), v: mapV - (py - v.h / 2) / (base * nz) })
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
      // 平移:把屏幕位移换算成归一化坐标偏移。
      const { zoom: z, vp: v } = st.current
      const base = Math.min(v.w, v.h) || 1
      const dx = (e.clientX - prev.x) / (base * z)
      const dy = (e.clientY - prev.y) / (base * z)
      setFollow(false)
      setFocus((f) => ({ u: f.u - dx, v: f.v - dy }))
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

  const recenter = () => {
    setFollow(true)
    if (pos && pos.u != null) setFocus({ u: pos.u, v: pos.v })
  }

  // 计算地图层与箭头的像素位置。
  const base = Math.min(vp.w, vp.h) || 1
  const mapPx = base * zoom
  const mapLeft = vp.w / 2 - focus.u * mapPx
  const mapTop = vp.h / 2 - focus.v * mapPx
  const arrowX = hasMap ? mapLeft + pos.u * mapPx : 0
  const arrowY = hasMap ? mapTop + pos.v * mapPx : 0
  // 世界 yaw(0=东/右,逆时针+)→ 默认朝上的箭头旋转 heading+90(CSS 顺时针,屏幕Y向下)。
  const arrowRot = (pos?.heading || 0) + 90

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
          <div className="map-world" style={{ width: mapPx, height: mapPx, transform: `translate3d(${mapLeft}px, ${mapTop}px, 0)` }}>
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
          <div className="map-arrow" style={{ left: arrowX, top: arrowY, transform: `translate(-50%,-50%) rotate(${arrowRot}deg)` }}>
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
