import React from 'react'
import { imgURL } from '../../components/icons'
import { WILD_LAYERS } from './useWildPets'

// LayerPanel 图层侧栏:POI 图层开关;可收集图层(眠枭之星/不咕钟零件)行右侧另有收集模式小开关
// (开 = 隐藏该图层已收集的点,判定来源见 usePois.js)。另有「野生宠物」一组:不是固定点位,
// 而是附近实时刷出的稀有个体(见 useWildPets.js)。
// 家园小窝不在此列:那层始终开着,不给开关也不占图例(见 useHomeNests.js)。
// 复用宠物列表那套 .filters:桌面常驻左列,移动端为侧滑抽屉(collapsed 控制开合)。
export default function LayerPanel({ pois, wilds, paint, collapsed, onClose }) {
  const { kinds, poiOn, togglePoi, collectOn, toggleCollect } = pois
  return (
    <>
      <div className={'filters-backdrop' + (collapsed ? '' : ' show')} onClick={onClose} />
      <aside className={'filters map-filters' + (collapsed ? ' collapsed' : '')}>
        <div className="filters-bar">
          <span className="filters-title">图层</span>
          <button className="icon-btn" onClick={onClose} aria-label="关闭图层">✕</button>
        </div>
        <div className="filter-group">
          <label>地图图标</label>
          {kinds.length === 0 && <span className="muted" style={{ fontSize: 13 }}>该场景暂无可显示的图标</span>}
          {kinds.map((k) => (
            <div className="map-layer-row" key={k.k}>
              <button className={'map-layer-btn' + (poiOn.has(k.k) ? ' on' : '')}
                onClick={() => togglePoi(k.k)}>
                <img src={imgURL(k.icon)} alt="" draggable={false} />
                <span className="map-layer-name">{k.n}</span>
                <span className="muted">{k.num}</span>
              </button>
              {k.collect && (
                <button className={'map-collect-btn' + (collectOn.has(k.k) ? ' on' : '')}
                  onClick={() => toggleCollect(k.k)} disabled={!poiOn.has(k.k)}
                  title="收集模式:隐藏已收集的点(需先开启图层)" aria-label={`${k.n}收集模式`}
                  aria-pressed={collectOn.has(k.k)}>✓</button>
              )}
            </div>
          ))}
        </div>
        <div className="filter-group">
          <div className="map-wild-head">
            <label>野生宠物</label>
            <div className="map-wild-mode" role="tablist" aria-label="编辑野生宠物筛选条件">
              {['or', 'and'].map((mode) => (
                <button key={mode} className={wilds.mode === mode ? 'on' : ''}
                  onClick={() => wilds.setMode(mode)} role="tab" aria-selected={wilds.mode === mode}
                  title={`编辑 ${mode.toUpperCase()} 条件；OR 与 AND 同时生效`}>
                  {mode.toUpperCase()}
                </button>
              ))}
            </div>
          </div>
          {WILD_LAYERS.map(({ k, n, color }) => {
            // 计数是该单项条件的命中数(含灰点),悬浮再拆开视野内/已离开。
            const num = wilds.num[k] || 0
            const gone = wilds.numStale[k] || 0
            return (
              <div className="map-layer-row" key={k}>
                <button className={'map-layer-btn map-wild-btn' + (wilds.on.has(k) ? ' on' : '')}
                  onClick={() => wilds.toggle(k)}
                  style={{ '--wild-color': color }}
                  title={gone ? `视野内 ${num - gone} · 已离开视野 ${gone}` : undefined}>
                  <span className="map-wild-swatch" style={{ borderColor: color }} />
                  <span className="map-layer-name">{n}</span>
                  <span className="muted">{num}</span>
                </button>
              </div>
            )
          })}
        </div>
        <div className="filter-group">
          <label>涂色模式</label>
          <div className="map-layer-row">
            <button className={'map-layer-btn' + (paint.on ? ' on' : '')}
              onClick={paint.toggle} disabled={!paint.available}
              title={paint.available ? '把「刷新过野生精灵」的区域涂色' : '该场景没有底图,无法涂色'}>
              <span className="map-paint-swatch" />
              <span className="map-layer-name">刷新过精灵的区域</span>
            </button>
            {/* 重置只清当前场景/当前层。误点代价不小(要重走一遍),故要确认一次。 */}
            <button className="map-collect-btn on" onClick={() => {
              if (window.confirm('清空本场景已涂的区域?重来一遍要重新走。')) paint.reset()
            }} disabled={!paint.available} title="重置本场景的涂色" aria-label="重置涂色">↺</button>
          </div>
        </div>
        <div className="filters-foot">
          <button className="btn primary" onClick={onClose}>查看地图</button>
        </div>
      </aside>
    </>
  )
}
