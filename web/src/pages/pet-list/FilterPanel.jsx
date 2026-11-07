import React, { useContext } from 'react'
import { IconsContext } from '../../context'
import { ALL_TYPES, ALL_EGG_GROUPS, HOT_NATURES, HOT_NATURE_NAMES } from '../../constants'
import { InlineIcon } from '../../components/icons'
import { Gender } from '../../components/badges'
import { Dropdown, MultiDropdown } from '../../components/dropdown'
import { CATCH_RANGES, asList } from './filters'

// FilterLabel 筛选组的标签行:标签与 ↺ 合成一个按钮,点标签本身就能重置这一项(符号只有
// 十几像素,单独当按钮太难点)。该项没有筛选条件时整颗按钮灰掉,既看得见每项都有重置入口,
// 又不会让一列按钮都像在生效。
function FilterLabel({ text, note, active, onReset }) {
  return (
    <button type="button" className="filter-label" disabled={!active}
      onClick={onReset} title={`重置${text}`} aria-label={`重置${text}`}>
      {text}{note && <span className="muted">{note}</span>}
      <span className="filter-reset-ic" aria-hidden="true">↺</span>
    </button>
  )
}

// FilterPanel 筛选侧栏:桌面常驻左列,移动端为侧滑抽屉(collapsed 控制开合)。
// children 为顶部的位置示意图(BoxMap)插槽;筛选状态由父级持有,经 set 增量更新。
export default function FilterPanel({ filter, options, total, collapsed, onClose, set, toggleType, reset, children }) {
  const icons = useContext(IconsContext)
  return (
    <>
      {/* 移动端筛选抽屉的背景遮罩:点击关闭 */}
      <div className={'filters-backdrop' + (collapsed ? '' : ' show')} onClick={onClose} />
      <aside className={'filters' + (collapsed ? ' collapsed' : '')}>
        {/* 抽屉标题栏(仅移动端显示):关闭入口与打开处的「筛选」按钮同侧 */}
        <div className="filters-bar">
          <span className="filters-title">筛选</span>
          <button className="icon-btn" onClick={onClose} aria-label="关闭筛选">✕</button>
        </div>
        {children}
        <div className="filter-group filter-reset">
          <button className="btn" onClick={reset}>重置筛选</button>
        </div>
        <div className="filter-group">
          <FilterLabel text="系别" active={(filter.types || []).length > 0} onReset={() => set({ types: [] })} />
          <div className="chips">
            {ALL_TYPES.map((t) => (
              <span key={t} className={'chip' + ((filter.types || []).includes(t) ? ' on' : '')} onClick={() => toggleType(t)}>
                <InlineIcon src={icons.type && icons.type[t]} className="chip-ic" alt="" />{t}
              </span>
            ))}
          </div>
        </div>
        <NatureSelect filter={filter} set={set} />
        <Select label="天分" opts={options.talentRank} value={filter.talentRank} onChange={(v) => set({ talentRank: v })} />
        <Select label="特长" opts={options.speciality} value={filter.speciality} onChange={(v) => set({ speciality: v })} />
        <MultiSelect label="蛋组" opts={ALL_EGG_GROUPS} value={filter.eggGroup} onChange={(v) => set({ eggGroup: v })} />
        <Select label="奖牌" opts={options.medal} value={filter.medal} onChange={(v) => set({ medal: v })} />
        <MultiSelect label="宠物盒" opts={boxOpts(options.box)} value={filter.box} onChange={(v) => set({ box: v })} />
        <div className="filter-group">
          <FilterLabel text="捕捉时间" active={!!filter.catchRange} onReset={() => set({ catchRange: '' })} />
          <Dropdown opts={CATCH_RANGES} value={filter.catchRange} onChange={(v) => set({ catchRange: v })} />
        </div>
        <div className="filter-group">
          <FilterLabel text="性别" active={!!filter.gender} onReset={() => set({ gender: '' })} />
          <div className="radios">
            {['', '♂', '♀'].map((v) => (
              <label key={v || 'all'} className="radio">
                <input type="radio" name="gender" checked={(filter.gender || '') === v} onChange={() => set({ gender: v })} />
                {v ? <Gender g={v} /> : '全部'}
              </label>
            ))}
          </div>
        </div>
        <div className="filter-group">
          <FilterLabel text="变异" active={filter.shiny === '1' || filter.colorful === '1'}
            onReset={() => set({ shiny: '', colorful: '' })} />
          <div className="checks">
            <label className="check">
              <input type="checkbox" checked={filter.shiny === '1'} onChange={(e) => set({ shiny: e.target.checked ? '1' : '' })} />异色
            </label>
            <label className="check">
              <input type="checkbox" checked={filter.colorful === '1'} onChange={(e) => set({ colorful: e.target.checked ? '1' : '' })} />炫彩
            </label>
          </div>
        </div>
        {/* 抽屉底部操作条(仅移动端显示):重置 + 查看结果并关闭 */}
        <div className="filters-foot">
          <button className="btn" onClick={reset}>重置</button>
          <button className="btn primary" onClick={onClose}>查看 {total} 只</button>
        </div>
      </aside>
    </>
  )
}

// 性格下拉:热门性格逐项列出(带六维影响),「其他」= 排除全部热门项(natureExclude)。
function NatureSelect({ filter, set }) {
  return (
    <div className="filter-group">
      <FilterLabel text="性格" active={!!(filter.nature || filter.natureExclude)}
        onReset={() => set({ nature: '', natureExclude: '' })} />
      <Dropdown
        opts={[['', '全部'], ...HOT_NATURES.map(([n, eff]) => [n, `${n}（${eff}）`]), ['__other__', '其他']]}
        value={filter.natureExclude ? '__other__' : filter.nature}
        onChange={(v) => {
          if (v === '__other__') set({ nature: '', natureExclude: HOT_NATURE_NAMES.join(',') })
          else set({ nature: v, natureExclude: '' })
        }}
      />
    </div>
  )
}

// MultiSelect 多选筛选组:标签 + 多选下拉(控件本体见 components/dropdown.jsx)。
// 选中的多项之间是**或**,标签上标一下,免得与其它「与」的条件混起来。
function MultiSelect({ label, opts, value, onChange }) {
  if (!opts || !opts.length) return null // 该维度本账号没有可选值(如还没抓到宠物盒)
  const cur = asList(value)
  return (
    <div className="filter-group">
      <FilterLabel text={label} active={cur.length > 0} onReset={() => onChange([])}
        note={cur.length > 1 ? '（任一）' : ''} />
      <MultiDropdown opts={opts} value={cur} onChange={onChange} />
    </div>
  )
}

// 盒子选项形如 "13-性格1",下拉里只显示盒名(值仍是原串,后端取前导整数当 box_id)。
const boxOpts = (boxes) => (boxes || []).map((b) => [b, b.replace(/^\d+-/, '')])

function Select({ label, opts, value, onChange }) {
  return (
    <div className="filter-group">
      <FilterLabel text={label} active={!!value} onReset={() => onChange('')} />
      <Dropdown opts={[['', '全部'], ...(opts || [])]} value={value} onChange={onChange} />
    </div>
  )
}
