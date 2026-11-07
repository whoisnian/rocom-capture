import React, { useState, useEffect, useContext } from 'react'
import { getEggs, subscribe } from '../../api'
import { AccountContext } from '../../context'
import { imgURL } from '../../components/icons'
import { PetDetailModal } from '../../components/PetDetailModal'
import { fmtTime, pctHot, voiceHot } from '../../utils/format'
import { Marks, Gender } from '../../components/badges'
import { hatchProgress } from './hatch'

// 精灵蛋页面:左栏孵蛋器(3x3 九宫格,点中一颗在下方看卡片)、右栏背包(其余的,每行六个,
// 同游戏内布局)。不分标签页——在孵的蛋本来就不出现在背包格子里
// (客户端 BagModuleData.IsRemoveEggItem),两栏一摆就把「在孵 / 在包」说清楚了,不必再给个过滤器。
// 排序复刻游戏内背包的两种(见 docs/eggs.md 与 internal/pet.SortEggs)。
const SORTS = [
  { k: 'quality', label: '品质' },
  { k: 'obtained', label: '获取时间' },
]

// 孵蛋器格子数:9 格,固定按 3x3 九宫格展示(玩家若再扩容则跟着在孵数加行)。
const HATCH_SLOTS = 9

export default function EggList() {
  const account = useContext(AccountContext)
  const [sort, setSort] = useState('quality')
  const [order, setOrder] = useState('desc')
  const [search, setSearch] = useState('')
  const [data, setData] = useState({ eggs: [] })
  const [detailGid, setDetailGid] = useState(null)
  const [selGid, setSelGid] = useState(null) // 九宫格里选中的那颗蛋(见下方 sel)
  const [now, setNow] = useState(() => Date.now())

  const load = () => getEggs({ search, sort, order })
    .then((d) => setData(d || { eggs: [] })).catch(() => {})

  useEffect(() => { load() }, [account, sort, order, search]) // eslint-disable-line react-hooks/exhaustive-deps
  // 后端在蛋有变动(收蛋/入孵/进度/破壳)时推 eggs,收到就重拉。
  useEffect(() => subscribe((m) => { if (m.type === 'eggs') load() }), [account, sort, order, search]) // eslint-disable-line react-hooks/exhaustive-deps
  // 孵化进度随时间涨:秒级刷新即可。
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [])

  const hatching = data.eggs.filter((e) => e.hatching)
  const bag = data.eggs.filter((e) => !e.hatching)
  const slots = Math.max(HATCH_SLOTS, hatching.length)
  // 九宫格里点中的那颗:按 gid 记(gid 唯一,重拉列表后仍能对上;它破壳了就回落到第一颗)
  const sel = hatching.find((e) => e.gid === selGid) || hatching[0] || null

  return (
    <div className="eggs-page">
      {/* 两栏的标题都是 eggs-cols 的直接子级(同一网格行),行高一致,两栏的卡片才从同一条
          基线开始;标题若各自塞回栏内,右栏被搜索框撑高就会比左栏低一截(见 eggs.css)。 */}
      <div className="eggs-cols">
        <div className="eggs-col-t">孵蛋器 <span className="muted">{hatching.length}/{slots}</span></div>
        <div className="eggs-bar">
          <div className="eggs-col-t">背包 <span className="muted">{bag.length} 颗</span></div>
          <div className="eggs-sorts">
            {SORTS.map((s) => (
              <button key={s.k} className={'chip' + (sort === s.k ? ' on' : '')}
                onClick={() => setSort(s.k)}>{s.label}</button>
            ))}
            <button className="btn" onClick={() => setOrder((o) => (o === 'desc' ? 'asc' : 'desc'))}
              title="反向排序(同游戏内那个箭头)">{order === 'desc' ? '↓' : '↑'}</button>
          </div>
          <input className="input eggs-search" placeholder="搜索蛋名/物种" value={search}
            onChange={(e) => setSearch(e.target.value)} />
        </div>

        {/* 孵蛋器九宫格:每格只放蛋图,点中后在下方看整张卡片;可破壳的蛋右上角点红点。
            九格固定画满(空着也画),与游戏内的孵蛋器布局一致。 */}
        <aside className="eggs-incu">
          <div className="egg-slots">
            {Array.from({ length: slots }, (_, i) => hatching[i]).map((e, i) => {
              if (!e) return <div key={'s' + i} className="egg-slot egg-slot-empty">空格子</div>
              const p = hatchProgress(e, now)
              const ready = !!p && p.pct >= 100
              return (
                <button key={e.gid} className={'egg-slot' + (e === sel ? ' on' : '')}
                  onClick={() => setSelGid(e.gid)} aria-pressed={e === sel}
                  title={`${e.name} · ${ready ? '可破壳' : p ? p.pct + '%' : '进度未知'}`}>
                  <img src={imgURL(e.icon)} alt="" draggable={false} />
                  {ready && <span className="egg-slot-dot" title="可破壳" />}
                </button>
              )
            })}
          </div>
          {sel
            ? <EggCard key={sel.gid} egg={sel} now={now} onPet={setDetailGid} />
            : <div className="empty">孵蛋器里还没有蛋</div>}
        </aside>

        <section className="eggs-bag">
          {bag.length === 0
            ? <div className="empty">背包里没有精灵蛋(需后端抓到背包全量:游戏内打开一次背包即可)</div>
            : (
              <div className="egg-grid">
                {bag.map((e) => <EggCard key={e.gid} egg={e} now={now} onPet={setDetailGid} />)}
              </div>
            )}
        </section>
      </div>
      {detailGid != null && <PetDetailModal gid={detailGid} onClose={() => setDetailGid(null)} />}
    </div>
  )
}

// EggCard 一颗蛋。布局固定(缺什么都留位置,免得同一行的卡片高低不齐):
//   [蛋图] 名称 / 奖牌标签        [品类角标][孵出物种头像]
//   重量 / 声音 / 高度 / 性格 / 时间
//   (在孵才有的进度条)
//   双亲两行(非家园蛋留占位)
function EggCard({ egg, now, onPet }) {
  const p = hatchProgress(egg, now)
  const src = egg.srcName ? `来源:${egg.srcName}` : ''
  return (
    <div className="egg-card">
      <div className="egg-head">
        <img className="egg-icon" src={imgURL(egg.icon)} alt="" draggable={false} />
        <div className="egg-title">
          <div className="egg-name" title={[egg.name, egg.species && `孵出 ${egg.species}`, src]
            .filter(Boolean).join(' · ')}>{egg.name}</div>
          <div className="egg-tags">
            {(egg.medals || []).map((m) => (
              <span key={m.dim} className="egg-chip" title={`${DIM_NAME[m.dim] || ''}奖牌`}>{m.name}</span>
            ))}
          </div>
        </div>
        <div className="egg-marks">
          {/* 异色/炫彩蛋用全站统一的那两个标记(同宠物列表),其余品类(珍贵/唯一/噩梦…)
              用游戏自己的蛋品类角标;普通蛋什么都不画(卡片高度由右边的头像撑着,不会塌)。 */}
          {egg.shiny || egg.colorful
            ? <span className="egg-type" title={egg.typeName}><Marks p={egg} /></span>
            : egg.typeIcon
              ? <img className="egg-type" src={imgURL(egg.typeIcon)} alt="" title={egg.typeName} draggable={false} />
              : null}
          {egg.petImg
            ? <img className="egg-pet" src={imgURL(egg.petImg)} alt="" title={egg.species} draggable={false} />
            : <span className="egg-pet ph" title="孵出前无从得知是谁">?</span>}
        </div>
      </div>

      <div className="egg-rows">
        <Row k="重量" v={egg.weightKg ? `${egg.weightKg} kg` : ''} pct={egg.weightPct}
          title={egg.adultWeightKg ? `孵出后约 ${egg.adultWeightKg} kg(百分位破壳后原样保留)` : ''} />
        <Row k="声音" v={voiceText(egg)} hot={egg.voice != null && egg.voiceMax == null ? voiceHot(egg.voice) : ''}
          title={egg.voice != null
            ? '按双亲嗓音均值向下取整推出(蛋上没有嗓音字段)' + (egg.voiceMax != null ? ',串窝父本不唯一故给区间' : '')
            : '蛋上没有嗓音字段,双亲也没记下,破壳才知道'} />
        <Row k="高度" v={egg.heightM ? `${egg.heightM} m` : ''} pct={egg.heightPct}
          title={egg.adultHeightM ? `孵出后约 ${egg.adultHeightM} m(百分位破壳后原样保留)` : ''} />
        <Row k="性格" v={natureText(egg)}
          title={egg.natures?.length
            ? (egg.natureSure
              ? '双亲之一住学院小窝:蛋必定继承那只的性格'
              : '性格有概率继承双亲之一,没中就另滚(「其他」);蛋上没有性格字段')
            : '蛋上没有性格字段,双亲也没记下,破壳才知道'} />
        <Row k="时间" v={timeNode(egg.obtainedAt)} title={`获得时间 ${fmtTime(egg.obtainedAt)}`} />
      </div>

      {p && (
        <div className="egg-hatch">
          <div className="egg-bar"><div className="egg-bar-fill" style={{ width: p.pct + '%' }} /></div>
          <span className={p.pct >= 100 ? 'val-hot-hi' : undefined}>{p.pct >= 100 ? '可破壳' : p.pct + '%'}</span>
        </div>
      )}

      <Parents p={egg.parents} onPet={onPet} />
    </div>
  )
}

// 奖牌只在**确定**拿得到时才列(后端已判好,判不了的不下发),纯文字,没有就空着——
// 那一行仍占着高度(见 .egg-tags 的 min-height),卡片才不会一行高一行矮。
const DIM_NAME = { 2: '体型', 3: '嗓音' }

// voiceText 渲染推测嗓音:串窝(父本不唯一)时给区间,推不出来时留空(由 Row 显示破折号)。
function voiceText(egg) {
  if (egg.voice == null) return ''
  return egg.voiceMax != null ? `${egg.voice}~${egg.voiceMax}` : String(egg.voice)
}

// natureText 渲染推测性格:学院小窝(必定继承)只列那一个;否则列双亲各自的(去重)再加「其他」
// (概率没中就另滚);推不出来时留空(由 Row 显示破折号)。
function natureText(egg) {
  if (!egg.natures?.length) return ''
  return egg.natureSure ? egg.natures.join('/') : [...egg.natures, '其他'].join('/')
}

// timeNode 渲染获得时间。手机上背包是双列,整串「2026-08-16 03:46:45」放不下会被省略号
// 咬掉分秒(而分秒正是「获取时间」排序看的东西),故把年份单独包一层,窄屏 CSS 藏掉即可
// (见 eggs.css;鼠标悬停的 title 里仍是完整时间)。
function timeNode(ts) {
  const s = fmtTime(ts)
  return <><span className="egg-row-y">{s.slice(0, 5)}</span>{s.slice(5)}</>
}

// Row 一行「标签 值 百分位」。值缺失时留破折号占位。
function Row({ k, v, pct, title, hot }) {
  const cls = hot || pctHot(pct) || ''
  return (
    <div className="egg-row" title={title || ''}>
      <span className="muted egg-row-k">{k}</span>
      <span className={'egg-row-v ' + cls}>{v || '—'}</span>
      <span className={'egg-row-p ' + cls}>{pct != null ? pct.toFixed(2) + '%' : ''}</span>
    </div>
  )
}

// Parents 双亲快照:母本确定(蛋趴在她的窝上),父本取服务器下发的配对候选,
// 几个窝挨太近「串窝」时有多个候选、实际父本无从确定(见 docs/eggs.md)。
// 住学院小窝的那只头像加绿圈(与地图上的小窝标记同色)——蛋的性格必定随它。
// 非家园蛋没有双亲可言,留同样高度的占位,保证卡片等高。
function Parents({ p, onPet }) {
  const rows = []
  if (p?.mother) rows.push(['♀', p.mother])
  for (const f of p?.fathers || []) rows.push(['♂', f])
  return (
    <div className="egg-parents">
      {rows.length === 0 && <div className="egg-parent-ph2">无双亲记录</div>}
      {rows.slice(0, 2).map(([role, x], i) => (
        <button key={i} className={'egg-parent' + (x.academy ? ' academy' : '')} onClick={() => onPet(x.gid)}
          title={`点击查看${role === '♀' ? '母本' : '父本'}详情(已放生也不影响这里的快照)` +
            (x.academy ? ' · 住学院小窝:蛋必定继承它的性格' : '') +
            (p.ambiguous && role === '♂' ? ' · 串窝:父本不唯一' : '')}>
          {x.img ? <img src={imgURL(x.img)} alt="" draggable={false} /> : <span className="egg-parent-noimg">🐾</span>}
          <span className="egg-parent-txt">
            <Gender g={role} /> {x.name}
            {x.weightPct != null && <span className={pctHot(x.weightPct)}> W {Math.round(x.weightPct)}%</span>}
            {x.voice != null && ` V ${x.voice}`}
            {x.nature ? ` ${x.nature}` : ''}
            {p.ambiguous && role === '♂' && <span className="egg-amb">?</span>}
          </span>
        </button>
      ))}
      {rows.length === 1 && <div className="egg-parent-ph2">父本未知</div>}
    </div>
  )
}
