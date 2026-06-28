import React, { useState, useEffect, useRef } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { toPng } from 'html-to-image'
import { getPet } from '../api'
import { Types, Marks, Portrait, boxLabel, fmtTime } from '../components/bits'

const SIX = [
  ['生命', 'hp'], ['物攻', 'attack'], ['魔攻', 'spAttack'],
  ['物防', 'defense'], ['魔防', 'spDefense'], ['速度', 'speed'],
]

export default function PetDetail() {
  const { gid } = useParams()
  const nav = useNavigate()
  const [pet, setPet] = useState(null)
  const [err, setErr] = useState(false)
  const cardRef = useRef(null)

  useEffect(() => {
    getPet(gid).then(setPet).catch(() => setErr(true))
  }, [gid])

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

  if (err) return <div className="empty">未找到该宠物</div>
  if (!pet) return <div className="empty">加载中…</div>

  return (
    <div className="detail-wrap">
      <div className="toolbar">
        <button className="btn" onClick={() => nav(-1)}>← 返回</button>
        <div className="spacer" />
        <button className="btn primary" onClick={exportImg}>保存为图片</button>
      </div>

      <div className="detail-card" ref={cardRef}>
        <div className="detail-head">
          <span className="detail-no">No.{pet.gid}</span>
          <span>{pet.species} {pet.gender}</span>
        </div>
        <Portrait p={pet} />
        <div className="detail-title">
          <h2>{pet.name || pet.species}</h2>
          <span className="lv">Lv.{pet.level}</span>
          <Marks p={pet} />
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
            <Item k="身高" v={pet.heightM + ' m'} />
            <Item k="体重" v={pet.weightKg + ' kg'} />
            <Item k="声音" v={pet.voice} />
            <Item k="标记" v={pet.partnerMark || '无'} />
            <Item k="盒子位置" v={boxLabel(pet.box)} />
            <Item k="捕捉时间" v={fmtTime(pet.catchTime)} />
            <Item k="异色" v={pet.shiny ? '是' : '否'} />
            <Item k="炫彩" v={pet.colorful ? '是' : '否'} />
          </div>

          {pet.medal && (
            <div>
              <div className="muted" style={{ marginBottom: 6 }}>奖牌墙</div>
              <div className="medals">
                <div className="medal">🏅 {pet.medal}{pet.medalDesc ? ` · ${pet.medalDesc}` : ''}</div>
              </div>
            </div>
          )}

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
  )
}

function Item({ k, v }) {
  return (
    <div className="item">
      <div className="k">{k}</div>
      <div className="v">{v ?? '-'}</div>
    </div>
  )
}
