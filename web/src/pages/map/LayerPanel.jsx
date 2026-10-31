import React from 'react'
import { imgURL } from '../../components/icons'
import { WILD_LAYERS } from './useWildPets'

// LayerPanel 图层侧栏:POI 图层开关;可收集图层(眠枭之星/不咕钟零件)行右侧另有收集模式小开关
// (开 = 隐藏该图层已收集的点,判定来源见 usePois.js)。另有「野生宠物」一组:不是固定点位,
// 而是附近实时刷出的稀有个体(见 useWildPets.js);其中最大/最小声音两行也有收集模式开关
// (开 = 已收集的标记划斜线,不隐藏)。「稀兽花种」并入「地图图标」一组:它同样是
// 地图图标,只是点位每天/每两周重投、由流量攒出而非后端 POI 清单(见 useFlowers.js)。
// 家园小窝不在此列:那层始终开着,不给开关也不占图例(见 useHomeNests.js)。
// 复用宠物列表那套 .filters:桌面常驻左列,移动端为侧滑抽屉(collapsed 控制开合)。
export default function LayerPanel({ pois, wilds, flowers, paint, collapsed, onClose }) {
  const { kinds, poiOn, togglePoi, collectOn, toggleCollect } = pois
  // 花种没有收集模式(异色/炫彩随列表就位,不用逐朵去点),故排在同样没有收集开关的图层
  // (魔力之源…眠枭庇护所)之后、带收集开关的那批(眠枭之星/不咕钟零件)之前;
  // 一个收集图层都没有的场景里就排在末尾。
  const flowerAt = (() => {
    const i = kinds.findIndex((k) => k.collect)
    return i < 0 ? kinds.length : i
  })()
  /* 稀兽花种同样是地图图标,只是点位不固定(每天/每两周重投),故不走后端的 POI kinds,
     而由 useFlowers 从流量里攒出来。
     本场景没有花种时整行不出现,与上面那些「本场景无点位就不给开关」的图层一致。
     图上有异色或炫彩花种时整行高亮:开这层就是为了找它,不该还得自己在几十朵里挨个看。
     参观好友世界时画的是**好友的**花,图层名标出来,免得当成自己的。 */
  const flowerRow = flowers.num > 0 ? (
    <div className="map-layer-row" key="flowers">
      <button className={'map-layer-btn' + (flowers.on ? ' on' : '') + (flowers.on && flowers.mutated ? ' mutated' : '')}
        onClick={flowers.toggle}
        title={[flowers.visit ? '正在参观的好友世界里的花种' : null,
          flowers.mutated ? `其中 ${flowers.mutated} 朵是异色或炫彩` : null,
        ].filter(Boolean).join(' · ') || undefined}>
        <img src={imgURL('flower/img_icon_huazhong_png.webp')} alt="" draggable={false} />
        <span className="map-layer-name">{flowers.visit ? '稀兽花种(好友)' : '稀兽花种'}</span>
        <span className="muted">{flowers.num}</span>
      </button>
    </div>
  ) : null
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
          {kinds.length === 0 && flowers.num === 0 &&
            <span className="muted" style={{ fontSize: 13 }}>该场景暂无可显示的图标</span>}
          {kinds.map((k, i) => (
            <React.Fragment key={k.k}>
              {i === flowerAt && flowerRow}
              <div className="map-layer-row">
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
            </React.Fragment>
          ))}
          {flowerAt >= kinds.length && flowerRow}
        </div>
        <div className="filter-group">
          <label>野生宠物</label>
          {WILD_LAYERS.map(({ k, n, color, collect }) => {
            // 计数含灰点(与图上标记一致),悬浮再拆开说明其中多少已离开视野。
            const num = wilds.num[k] || 0
            const gone = wilds.numStale[k] || 0
            return (
              <div className="map-layer-row" key={k}>
                <button className={'map-layer-btn map-wild-btn' + (wilds.on.has(k) ? ' on' : '')}
                  onClick={() => wilds.toggle(k)}
                  title={gone ? `视野内 ${num - gone} · 已离开视野 ${gone}` : undefined}>
                  <span className="map-wild-swatch" style={{ borderColor: color }} />
                  <span className="map-layer-name">{n}</span>
                  <span className="muted">{num}</span>
                </button>
                {/* 收集模式:同眠枭之星那颗小开关,但不隐藏,而是在已收集的标记上划一道斜线 */}
                {collect && (
                  <button className={'map-collect-btn' + (wilds.collectOn.has(k) ? ' on' : '')}
                    onClick={() => wilds.toggleCollect(k)} disabled={!wilds.on.has(k)}
                    title="收集模式:盒子里已有同进化链的雌性同端满声音个体时,标记上划斜线(需先开启图层)"
                    aria-label={`${n}收集模式`} aria-pressed={wilds.collectOn.has(k)}>✓</button>
                )}
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
