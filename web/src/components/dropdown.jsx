import React, { useState, useRef, useEffect, useLayoutEffect } from 'react'
import { createPortal } from 'react-dom'

// 全站下拉统一用这套自绘控件,不再用原生 <select>:多选原生做不来(<select multiple> 展开成
// 一整块列表框,桌面要按住 Ctrl 点、移动端又变成整屏选择器),而单选若还留着原生,同一栏里
// 两种下拉的箭头与展开面板就对不齐了。故单选/多选共用同一套外观。
//
// opts 每项写作 '值'(值即显示文本)或 ['值', '显示文本'];值一律按字符串比对,数值选项
// (每页条数)照样能用,回调仍把原值交回去。

const GAP = 4  // 面板与按钮之间的间距
const EDGE = 8 // 面板离视口边缘至少留出的余量

// norm 把 opts 归一成 [值, 文本] 对。
const norm = (opts) => (opts || []).map((o) => (Array.isArray(o) ? o : [o, o]))
const same = (a, b) => String(a ?? '') === String(b ?? '')

// Caret 自绘下拉箭头:原生 <select> 的箭头各浏览器长得不一样,自绘才能全站一致。
const Caret = () => (
  <svg className="dd-caret" viewBox="0 0 10 6" aria-hidden="true">
    <path d="M1 1l4 4 4-4" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
  </svg>
)

// useDropdown 管开合与展开面板的落位。面板用 fixed 定位:侧栏(桌面 max-height + overflow:auto,
// 移动端抽屉同理)会把 absolute 的面板裁掉。
// 点空白/滚动/改窗口/Esc 关闭;面板自身的点击靠 .dd-menu 判断放行,多选才能连点几项不关。
function useDropdown() {
  const [rect, setRect] = useState(null) // 展开时按钮的位置(null=收起)
  const [at, setAt] = useState(null)     // 量过面板高度后算出的落位
  const btnRef = useRef(null)
  const menuRef = useRef(null)

  // 朝上还是朝下,得先知道面板摊开有多高——按固定阈值猜必然猜错:15 个蛋组要 400 多 px,
  // 下方还剩 250px 也算"够",于是稳稳地朝下开再出一条滚动条。故先把面板渲染出来
  // (visibility:hidden 且不限高)量一把 scrollHeight,再决定往哪边放:哪边放得下就整个摊开,
  // 两边都放不下才取宽敞的那边限高。fixed 元素不撑文档,量的时候超出视口也不会带出页面滚动条。
  useLayoutEffect(() => {
    if (!rect || !menuRef.current) { setAt(null); return }
    const need = menuRef.current.scrollHeight + 2 // 加上面板自身上下边框
    const below = window.innerHeight - rect.bottom - GAP - EDGE
    const above = rect.top - GAP - EDGE
    const down = need <= below || below >= above
    setAt(down
      ? { left: rect.left, top: rect.bottom + GAP, width: rect.width, maxHeight: below }
      : { left: rect.left, bottom: window.innerHeight - rect.top + GAP, width: rect.width, maxHeight: above })
  }, [rect])

  const close = () => { setRect(null); setAt(null) }
  useEffect(() => {
    if (!rect) return
    // 面板内部的点击与滚动都放行:点是为了多选连点几项,滚是因为上下都放不下时面板自己带
    // 滚动条——scroll 监听挂在捕获阶段,面板自身滚出来的事件也会到这儿,不放行就一滚就关。
    // (侧栏/页面滚动仍要关:面板是 fixed 的,跟着滚会与按钮脱开。)
    const inMenu = (e) => e && e.target && e.target.closest && e.target.closest('.dd-menu')
    const onClick = (e) => { if (!inMenu(e)) close() }
    const onScroll = (e) => { if (!inMenu(e)) close() }
    const onKey = (e) => { if (e.key === 'Escape') close() }
    window.addEventListener('click', onClick)
    window.addEventListener('scroll', onScroll, true)
    window.addEventListener('resize', close)
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('click', onClick)
      window.removeEventListener('scroll', onScroll, true)
      window.removeEventListener('resize', close)
      window.removeEventListener('keydown', onKey)
    }
  }, [rect])

  const toggle = (e) => {
    e.stopPropagation() // 否则这一下点击紧接着就被上面的 onClick 收回去
    if (rect) { close(); return }
    setAt(null) // 先清掉上次的落位,免得用旧限高量出个矮的 scrollHeight
    setRect(btnRef.current.getBoundingClientRect())
  }
  return { rect, at, btnRef, menuRef, toggle, close }
}

// Field 收起态:与原来的 .select 同款外观,右侧是自绘箭头。
function Field({ dd, className, title, text }) {
  return (
    <button ref={dd.btnRef} className={'select dd' + (className ? ' ' + className : '')} title={title} onClick={dd.toggle}>
      <span className="dd-text">{text}</span>
      <Caret />
    </button>
  )
}

// Menu 展开态。挂到 body 下(portal):侧栏是 position:sticky,而 sticky 会**建立层叠上下文**,
// 面板留在里面的话 z-index 再高也只在侧栏内部管用,对外整个侧栏是 z-index:auto,顶栏(30)照样
// 盖在它上面——朝上开时露在顶栏底下的那几行就点不着了。挂到 body 下才真的浮在最上层。
// at 还没算出来的那一帧是"量高度"用的:摆在按钮下方、不限高、不可见。
function Menu({ dd, children }) {
  const { at, rect } = dd
  const style = at
    ? { left: at.left, top: at.top, bottom: at.bottom, width: at.width, maxHeight: at.maxHeight }
    : { left: rect.left, top: rect.bottom + GAP, width: rect.width, visibility: 'hidden' }
  return createPortal(
    <div ref={dd.menuRef} className="dd-menu" style={style}>{children}</div>,
    document.body,
  )
}

// Dropdown 单选下拉,替代原生 <select>。选项里要有「全部」这类空值项就自行写进 opts。
export function Dropdown({ opts, value, onChange, className, title }) {
  const dd = useDropdown()
  const list = norm(opts)
  const hit = list.find(([v]) => same(v, value))
  return (
    <>
      <Field dd={dd} className={className} title={title} text={hit ? hit[1] : ''} />
      {dd.rect && (
        <Menu dd={dd}>
          {list.map(([v, l]) => (
            <div key={'o' + v} className={'dd-opt' + (hit && hit[0] === v ? ' on' : '')}
              onClick={() => { dd.close(); onChange(v) }}>{l}</div>
          ))}
        </Menu>
      )}
    </>
  )
}

// MultiDropdown 多选下拉。选中的多项之间是**或**(妖精+巨灵 = 属于其中任一蛋组的都算),
// 这正是找配对候选要的语义;若要「同时属于两组」得改成 AND,那是另一回事,现在没有这种需求。
// 收起态显示 `全部` / `妖精` / `妖精 等 2 项`,完整清单在 title 里。
//
// 按住 Shift 点击按区段操作(宠物盒动辄几十个,逐项点太慢):以最近一次普通点击为锚点,
// 把锚点到本次点击之间整段设成与本次点击相同的状态——点未选的整段选中,点已选的整段取消;
// 锚点不随区段操作移动,继续 Shift 点别处就是在此前锚点上伸缩这一区段。
export function MultiDropdown({ opts, value, onChange, className, allLabel = '全部' }) {
  const dd = useDropdown()
  const list = norm(opts)
  const cur = value || []
  const anchor = useRef(-1) // 区段起点(上次普通点击的下标);面板收起后失效
  useEffect(() => { if (!dd.rect) anchor.current = -1 }, [dd.rect])

  const lbl = (v) => { const o = list.find(([ov]) => same(ov, v)); return o ? o[1] : v }
  const toggle = (v, idx, shift) => {
    if (shift && anchor.current >= 0 && anchor.current < list.length) {
      const lo = Math.min(anchor.current, idx)
      const hi = Math.max(anchor.current, idx)
      const want = !cur.includes(v) // 本次点击后这一项应有的状态
      const next = new Set(cur)
      for (let i = lo; i <= hi; i++) {
        if (want) next.add(list[i][0])
        else next.delete(list[i][0])
      }
      onChange([...next])
      return
    }
    anchor.current = idx
    onChange(cur.includes(v) ? cur.filter((x) => x !== v) : [...cur, v])
  }
  const text = cur.length === 0 ? allLabel
    : cur.length === 1 ? lbl(cur[0])
      : `${lbl(cur[0])} 等 ${cur.length} 项`
  return (
    <>
      <Field dd={dd} className={className} text={text} title={cur.length ? cur.map(lbl).join(' / ') : allLabel} />
      {dd.rect && (
        <Menu dd={dd}>
          <div className={'dd-opt' + (cur.length ? '' : ' on')}
            onClick={() => { dd.close(); anchor.current = -1; onChange([]) }}>{allLabel}</div>
          {list.map(([v, l], i) => (
            <label key={'o' + v} className={'dd-opt' + (cur.includes(v) ? ' on' : '')}
              // 按住 Shift 点文字时浏览器把它当成「扩展选区」,既不选宠物盒还会留下一片高亮文字;
              // 先在 mousedown 拦掉选字(点勾选框本身不拦),再自己处理整行的点击。
              onMouseDown={(e) => { if (e.shiftKey && e.target.tagName !== 'INPUT') e.preventDefault() }}
              onClick={(e) => {
                if (e.target.tagName === 'INPUT') return // 点勾选框交给它的 onChange,否则一次点击切两下
                e.preventDefault() // 拦掉 label 转发给 checkbox 的默认动作
                toggle(v, i, e.shiftKey)
              }}>
              <input type="checkbox" checked={cur.includes(v)}
                onChange={(e) => toggle(v, i, e.nativeEvent.shiftKey)} />{l}
            </label>
          ))}
        </Menu>
      )}
    </>
  )
}
