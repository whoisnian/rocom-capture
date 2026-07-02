import React, { useState, useEffect, useRef } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { toPng } from 'html-to-image'
import { getPet, getMedals, getEvolution } from '../api'
import { Types, Marks, Gender, Form, Portrait, ImgAvatar, StatRange, locText, fmtTime } from '../components/bits'

const SIX = [
  ['生命', 'hp'], ['物攻', 'attack'], ['魔攻', 'spAttack'],
  ['物防', 'defense'], ['魔防', 'spDefense'], ['速度', 'speed'],
]

// 路由页:直接访问 /pets/:gid 或从其他页跳转时,以弹窗形式呈现,关闭即返回上一页。
export default function PetDetail() {
  const { gid } = useParams()
  const nav = useNavigate()
  return <PetDetailModal gid={gid} onClose={() => nav(-1)} />
}

// PetDetailModal 宠物详情弹窗:覆盖在当前页面之上,不打断底层正在操作的列表/事件页。
// 点击卡片外区域、按 Esc、点返回均触发 onClose。
export function PetDetailModal({ gid, onClose }) {
  const [pet, setPet] = useState(null)
  const [err, setErr] = useState(false)
  const [medals, setMedals] = useState([])
  const [chain, setChain] = useState([])
  const cardRef = useRef(null)

  useEffect(() => {
    setPet(null)
    setErr(false)
    setChain([])
    getPet(gid).then(setPet).catch(() => setErr(true))
  }, [gid])
  useEffect(() => { getMedals().then(setMedals).catch(() => {}) }, [])
  // 进化链:按当前形态 petbase(base_conf_id)拉取整条链
  useEffect(() => {
    if (pet && pet.baseConfId) getEvolution(pet.baseConfId).then((c) => setChain(c || [])).catch(() => {})
  }, [pet && pet.baseConfId])
  // Esc 关闭弹窗
  useEffect(() => {
    const onKey = (e) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  const exportImg = () => {
    if (!cardRef.current) return
    toPng(cardRef.current, { pixelRatio: 2, backgroundColor: '#0e1116', cacheBust: true })
      .then((url) => {
        const a = document.createElement('a')
        a.href = url
        a.download = `${pet.name || pet.species}_${pet.gid}.png`
        a.click()
      })
      .catch(() => alert('导出失败'))
  }

  // 点击卡片与工具栏之外的区域 → 关闭
  const onBackdrop = (e) => {
    if (cardRef.current && cardRef.current.contains(e.target)) return
    if (e.target.closest && e.target.closest('.toolbar')) return
    onClose()
  }

  if (err) return (
    <div className="detail-backdrop" onClick={onClose}>
      <div className="detail-wrap"><div className="empty">未找到该宠物</div></div>
    </div>
  )
  if (!pet) return (
    <div className="detail-backdrop" onClick={onClose}>
      <div className="detail-wrap"><div className="empty">加载中…</div></div>
    </div>
  )

  return (
    <div className="detail-backdrop" onClick={onBackdrop}>
      <div className="detail-wrap">
      <div className="toolbar">
        <button className="btn" onClick={onClose}>← 返回</button>
        <div className="spacer" />
        <button className="btn primary" onClick={exportImg}>保存为图片</button>
      </div>

      <div className="detail-card" ref={cardRef}>
        <div className="detail-head">
          <span className="detail-no">No.{pet.gid}{pet.book ? ` · 图鉴#${pet.book}` : ''}</span>
          <span>{pet.species} <Gender g={pet.gender} /></span>
        </div>
        <Portrait p={pet} />
        <div className="detail-title">
          <h2>{pet.name || pet.species}</h2>
          <span className="lv">Lv.{pet.level}</span>
          <Marks p={pet} />
          <Form form={pet.form} />
        </div>

        <div className="detail-body">
          <div className="rule-row">
            {pet.talentRank && <span className="pill">{pet.talentRank}</span>}
            <Types types={pet.types} />
          </div>

          <div className="stats">
            {SIX.map(([label, key]) => {
              const s = pet[key] || {}
              return (
                <div className="stat" key={key}>
                  <div className="n">
                    {s.value ?? 0}
                    {s.nature === 1 && <span className="up"> ↑</span>}
                    {s.nature === -1 && <span className="down"> ↓</span>}
                  </div>
                  <div className="l">
                    {label}
                    {s.talentLv > 0 && <span className="talent"> +{s.talentLv}</span>}
                  </div>
                </div>
              )
            })}
          </div>

          <div className="kv">
            <Item k="性格" v={pet.nature} />
            <Item k="特长" v={pet.speciality || '无'} />
            <Item k="身高" v={<StatRange value={pet.heightM} min={pet.heightMin} max={pet.heightMax} pct={pet.heightPct} unit=" m" />} />
            <Item k="体重" v={<StatRange value={pet.weightKg} min={pet.weightMin} max={pet.weightMax} pct={pet.weightPct} unit=" kg" />} />
            <Item k="声音" v={pet.voice} />
            <Item k="标记" v={pet.partnerMark || '无'} />
            <Item k="位置" v={locText(pet)} />
            <Item k="捕捉时间" v={fmtTime(pet.catchTime)} />
            <Item k="异色" v={pet.shiny ? '是' : '否'} />
            <Item k="炫彩" v={pet.colorful ? '是' : '否'} />
          </div>

          {chain.length > 1 && (
            <div>
              <div className="muted" style={{ marginBottom: 6 }}>进化链{pet.form ? `（${pet.form}）` : ''}</div>
              <div className="evo-chain">
                {evoStages(chain).map((forms, i) => (
                  <React.Fragment key={forms[0].stage}>
                    {i > 0 && <span className="evo-arrow">→</span>}
                    {/* 同阶段多形态=分支进化(蓝珠天鹅→翠顶夫人/黑羽夫人),纵向并列 */}
                    <div className="evo-stage">
                      {forms.map((s) => (
                        <div key={s.petbase} className={'evo-step' + (s.petbase === pet.baseConfId ? ' on' : '')} title={`图鉴#${s.book}`}>
                          <ImgAvatar src={s.image && s.image.head} alt={s.name} className="evo-avatar" />
                          <div className="evo-name">{s.name}</div>
                        </div>
                      ))}
                    </div>
                  </React.Fragment>
                ))}
              </div>
            </div>
          )}

          {(() => {
            const owned = medals.filter((m) => (pet.medalIds || []).includes(m.id))
            if (owned.length === 0) return null
            return (
              <div>
                <div className="muted" style={{ marginBottom: 6 }}>奖牌墙</div>
                <div className="medals">
                  {owned.map((m) => (
                    <div key={m.id} className="medal medal-tip" data-tip={m.desc || m.name}>
                      🏅 {m.name}
                    </div>
                  ))}
                </div>
              </div>
            )
          })()}

          {pet.skillIds?.length > 0 && (
            <div>
              <div className="muted" style={{ marginBottom: 6 }}>技能</div>
              <div className="medals">
                {pet.skillIds.map((id, i) => <div className="medal" key={i}>技能 #{id}</div>)}
              </div>
            </div>
          )}
        </div>
      </div>
      </div>
    </div>
  )
}

// evoStages 把进化链(后端已按 阶段,图鉴号 排序)按阶段分组:每组=同一进化阶段的形态,
// 同组有多项即分支进化(如三阶的 翠顶夫人/黑羽夫人)。返回 [[stage1 形态...], [stage2...], ...]。
function evoStages(chain) {
  const stages = []
  for (const s of chain) {
    const last = stages[stages.length - 1]
    if (last && last[0].stage === s.stage) last.push(s)
    else stages.push([s])
  }
  return stages
}

function Item({ k, v }) {
  return (
    <div className="item">
      <div className="k">{k}</div>
      <div className="v">{v ?? '-'}</div>
    </div>
  )
}
