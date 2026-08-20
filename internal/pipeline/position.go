package pipeline

import (
	"time"

	"github.com/whoisnian/rocom-capture/internal/capture"
	"github.com/whoisnian/rocom-capture/internal/gamedata"
	"github.com/whoisnian/rocom-capture/internal/gcp"
	"github.com/whoisnian/rocom-capture/internal/scene"
	"github.com/whoisnian/rocom-capture/internal/store"
)

// handleScene 处理实时地图与星星相关的场景消息;返回是否已消费。
// s2c 进入/传送更新当前场景 res 与落点、区域进出更新所在层;c2s 移动包投影后推送。
func (p *Pipeline) handleScene(m capture.Message, acc string) bool {
	switch {
	case m.Direction == gcp.S2C && m.Opcode == scene.OpEnterSceneRsp:
		p.onEnterScene(m, acc)
	case m.Direction == gcp.S2C && m.Opcode == scene.OpEnterSceneFinishAck:
		p.onSceneSnapshot(m, acc)
	case m.Direction == gcp.S2C && m.Opcode == scene.OpTeleportNotify:
		p.onTeleport(m, acc)
	case m.Direction == gcp.S2C && m.Opcode == scene.OpPlayActsBatchNotify:
		p.observeStars(m.Session, acc, m.AppBody)
		p.observeWilds(m.Session, acc, m.AppBody, m.Time, false)
		p.observeHome(m.Session, acc, m.AppBody)
	case m.Direction == gcp.C2S && m.Opcode == scene.OpNpcPendantInteractReq:
		p.onPendantReq(m)
	case m.Direction == gcp.S2C && m.Opcode == scene.OpNpcPendantInteractRsp:
		p.onPendantRsp(m, acc)
	case m.Direction == gcp.S2C && m.Opcode == scene.OpPlayActsNotify:
		p.onPlayActs(m, acc)
		p.observeHome(m.Session, acc, m.AppBody)
	case m.Direction == gcp.C2S && m.Opcode == scene.OpNpcNextActReq:
		p.onNpcInteract(m.Session, m.AppBody, m.Time)
	case m.Direction == gcp.S2C && m.Opcode == scene.OpBattleFinishNotify:
		p.onBattleFinish(m.Session, acc, m.AppBody, m.Time)
	case m.Direction == gcp.C2S && m.Opcode == scene.OpSceneMoveReq:
		p.onMove(m, acc)
	default:
		return false
	}
	return true
}

// onEnterScene 处理进入场景回包:更新场景态并重置区域/星星观测,另取按区域的收集进度。
func (p *Pipeline) onEnterScene(m capture.Message, acc string) {
	if _, res, room, ok := scene.ParseEnterScene(m.AppBody); ok {
		cs := p.conn(m.Session)
		cs.res, cs.room = res, room
		p.st.SaveSessionScene(m.Session, res, room) // 落盘供重启恢复
		p.leaveHome(m.Session, acc, res)            // 出了家园就撤掉小窝图层
		// 换场景/传送后旧区域一律作废:服务器不为它们补发离开事件,只在落地后重发进入事件
		// (客户端同样在传送时清空区域,见 AreaAndZoneModule:OnTeleportClearAreaInfo)。
		p.resetAreas(m.Session)
		// 星星观测态按场景重置:上个场景的实体不算数。周边实体快照(0x014a)随后才到。
		cs.stars = newStarTracker(res)
		cs.wildSeen = nil                          // 涂地跟踪的实体同样按场景清零(见 paintSeen)
		p.switchWilds(m.Session, acc, res, m.Time) // 野生宠按场景保留;当前 AOI 目标先转为最后所见
	}
	p.applyZoneProgress(m, acc)
}

// onTeleport 处理传送通知。传送落点(to_pt)此刻已知,而客户端要过几秒(加载)才落地并开始发
// 移动包:立刻按落点推一条位置,否则地图会停在原地干等,玩家落地后不动更是一直不更新(分层也
// 跟着不出现)。落点是静止的一点:无速度、无轨迹;分层留待落地后的区域进入事件补上。
func (p *Pipeline) onTeleport(m capture.Message, acc string) {
	tp, ok := scene.ParseTeleport(m.AppBody)
	if !ok {
		return
	}
	cs := p.conn(m.Session)
	cs.res, cs.room = tp.ResID, tp.Room
	p.st.SaveSessionScene(m.Session, tp.ResID, tp.Room)
	p.leaveHome(m.Session, acc, tp.ResID) // 传送走了就撤掉小窝图层(进家园时由快照重建)
	p.resetAreas(m.Session)
	cs.wildSeen = nil                               // 同上:涂地跟踪的实体也作废
	cs.pos = tp.Pos                                 // 落点即当前位置:落地快照里的宠物就从这儿起画走廊
	p.switchWilds(m.Session, acc, tp.ResID, m.Time) // AOI 全换;旧目标置灰,跨场景目标返回时恢复
	pos := p.buildPos(acc, tp.ResID, tp.Room, scene.MoveReq{
		Pos: tp.Pos, Yaw: tp.Yaw, StopMove: true, SceneCfgID: tp.CfgID,
	}, m.Time)
	p.pushPos(acc, pos)
}

// onPlayActs 处理区域动作通知:同一个通知里既有区域进/出(选层),
// 也有 AOI 实体进/离(星星判定、野生宠物图层)。
func (p *Pipeline) onPlayActs(m capture.Message, acc string) {
	p.observeStars(m.Session, acc, m.AppBody)
	p.observeWilds(m.Session, acc, m.AppBody, m.Time, false)
	// 区域进/出:玩家真正踩进/离开区域触发体(3D 体积)时服务器才下发,是选层的权威依据。
	acts := scene.ParseAreaActs(m.AppBody)
	if len(acts) == 0 {
		return
	}
	cs := p.conn(m.Session)
	for _, a := range acts {
		if a.Enter {
			if cs.areas == nil {
				cs.areas = map[uint32]map[uint32]bool{}
			}
			if cs.areas[a.FuncID] == nil {
				cs.areas[a.FuncID] = map[uint32]bool{}
			}
			cs.areas[a.FuncID][a.AreaID] = true
			continue
		}
		if cs.areas[a.FuncID] != nil {
			delete(cs.areas[a.FuncID], a.AreaID)
			if len(cs.areas[a.FuncID]) == 0 { // 该 func 下的区域都离开了,才算离开这一层
				delete(cs.areas, a.FuncID)
			}
		}
	}
	p.saveAreas(m.Session)
	// 层可能就此变了(尤其传送落地时的进入事件):此刻玩家可能站着不动、下一个移动包遥遥无期,
	// 故当场推一条**只更新分层**的消息(layerOnly),前端据此叠上/撤下切片图而不动位置锚点。
	p.settleLayer(m.Session, acc, m.Time)
}

// onMove 处理 c2s 移动包:投影成地图坐标逐包推送,不节流——客户端本就只在操作变化时上报
// (约 0.1s 一包,输入不变时退化成 2.5-3s 心跳),峰值仅约 8 条/秒。丢包会丢掉转向事件,
// 前端外推便会偏出去(见 buildPos 的 vu/vv)。
func (p *Pipeline) onMove(m capture.Message, acc string) {
	mr, ok := scene.ParseMoveReq(m.AppBody)
	if !ok {
		return
	}
	cs := p.conn(m.Session)
	res := cs.res
	if res == 0 { // 未知 res(中途开抓/无缓存):用移动包的 scene_cfg_id 兜底默认 res
		res = p.db.DefaultSceneRes(mr.SceneCfgID)
	}
	prev := cs.pos  // 涂地的贴身安全带要沿「上一包 → 这一包」这段路涂,故先留住旧位置
	cs.pos = mr.Pos // 之后画到每只野生宠的走廊都从这儿起(实体通知里没有玩家坐标)
	pos := p.buildPos(acc, res, cs.room, mr, m.Time)
	// 分层地图:玩家当前所在区域(服务器区域进/出事件维护)命中某层的 area_func_id 即在该层,
	// 经 layerDebounce 去抖(滤掉走动中擦出/擦进触发体接缝的百毫秒级抖动)。见 docs/data.md 3.2。
	if l, ok := p.layerOf(m.Session, res, m.Time, true); ok {
		if lp := p.layerPayload(res, l); lp != nil {
			pos["sceneName"] = l.Name
			pos["layer"] = lp
		}
	}
	p.pushPos(acc, pos)
	// 玩家走到哪,就把周围的星星判一遍(走近了却没实体 ⇒ 已收集;z 供洞穴层守卫)。
	p.sweepStars(m.Session, acc, res, mr.Pos.X, mr.Pos.Y, mr.Pos.Z, m.Time)
	// 涂地:贴身安全带沿这一段路涂,再把「玩家 ↔ 此刻视野里每只野生宠」的走廊涂上
	// (见 docs/data.md 3.8)。人一动,同样几只宠的走廊也会扫过新的一片,故每包都涂一次。
	p.paintSeen(m.Session, acc, res, p.movePath(prev, mr))
}

// paintSeen 涂一次地(见 docs/data.md 3.8):path 是玩家刚走过的一段(空则只用当前位置),
// 沿它涂贴身安全带;再把「玩家 ↔ 当前 AOI 里每只野生宠」的走廊涂上。
// 玩家一动、或收到新的实体通知都要涂——前者让同几只宠的走廊扫过新的一片,后者是新看到的方向。
func (p *Pipeline) paintSeen(conn, acc string, res int32, path [][2]int32) {
	cs := p.conns[conn]
	if cs == nil || cs.pos == (scene.Position{}) {
		return
	}
	if len(path) == 0 {
		path = [][2]int32{{cs.pos.X, cs.pos.Y}}
	}
	pets := make([][2]int32, 0, len(cs.wildSeen))
	for _, q := range cs.wildSeen {
		pets = append(pets, [2]int32{q.X, q.Y})
	}
	p.srv.PaintSeen(acc, res, p.curLayerID(conn), path, pets)
}

// movePath 把玩家自上一包以来实际走过的路拼成折线(世界坐标):上一包位置 → 补报的轨迹点 →
// 本包位置。客户端输入不变时退化成 2.5-3s 一次心跳,那几秒真走的路只在 move_seg_list 里
// (见 buildPos),不带上它,贴身安全带就会在两包之间断成一截一截。
// prev 为零值(刚换场景/传送落地,还没有上一包)时只回本包位置。
func (p *Pipeline) movePath(prev scene.Position, mr scene.MoveReq) [][2]int32 {
	path := make([][2]int32, 0, len(mr.Segs)+2)
	if prev != (scene.Position{}) {
		path = append(path, [2]int32{prev.X, prev.Y})
	}
	for _, sg := range mr.Segs {
		path = append(path, [2]int32{sg.Pos.X, sg.Pos.Y})
	}
	path = append(path, [2]int32{mr.Pos.X, mr.Pos.Y})
	return path
}

// curLayerID 返回该连接当前所在的分层地图 id(0=地表)。涂地按层各存一张:洞穴与地表
// 是两个空间,AOI 不相通(见 docs/data.md 3.4 的洞穴层守卫)。
func (p *Pipeline) curLayerID(conn string) int32 {
	cs := p.conns[conn]
	if cs == nil || cs.layer == nil || !cs.layer.curOK {
		return 0
	}
	return int32(cs.layer.cur.ID)
}

// pushPos 缓存并广播一条位置(缓存供地图页加载即时回显)。
func (p *Pipeline) pushPos(acc string, pos map[string]any) {
	p.acct(acc).lastPos = pos
	p.srv.SetLastPosition(acc, pos)
	p.srv.Hub().Broadcast("position", acc, pos)
}

// minSegSpan 是「值得回放的真实轨迹」的最短跨度(秒)。移动包按操作事件上报:持续改方向/变速时
// 约 0.1s 一包(轨迹点为空或只有一两个,回放毫无意义且会拖慢箭头);推住摇杆盘旋或直线巡航时输入
// 不变,退化成 2.5-3s 一次心跳,那几秒实际走的路(含大转弯)只在 move_seg_list 里。取 0.6s 为界。
const minSegSpan = 0.6

// buildPos 组装一条位置推送(不含分层)。移动包与**传送落点**共用:传送时用一个只带 Pos/Yaw/StopMove
// 的合成 MoveReq(无速度、无轨迹),这样传送一下发就能把地图切到目的地,不必干等第一个移动包。
func (p *Pipeline) buildPos(acc string, res, room int32, mr scene.MoveReq, t time.Time) map[string]any {
	// 地表底图始终作背景;玩家点用底图投影。坐标系统一为底图。
	pos := map[string]any{
		"account":    acc,
		"sceneResId": res,
		"sceneCfgId": mr.SceneCfgID,
		"sceneName":  p.sceneDisplayName(res, mr.SceneCfgID),
		"img":        p.db.MapImage(uint32(res), room), // 底图文件名(家园按等级 <res>_<lv>);无底图为空
		"x":          mr.Pos.X,
		"y":          mr.Pos.Y,
		"z":          mr.Pos.Z,
		"heading":    float64(mr.Yaw) / 10, // 朝向角(度),UE Yaw:0=世界+X(地图东/右),顺时针增
		"stop":       mr.StopMove,
		"paintable":  p.srv.Paintable(res), // 该场景能否涂地(见 docs/data.md 3.8),前端据此显示图层开关
		"ts":         t.Unix(),
		"tsMs":       t.UnixMilli(), // 前端判断缓存位置是否过期(过期则不外推)
	}
	u, v, ok := p.db.Project(uint32(res), mr.Pos.X, mr.Pos.Y)
	if !ok {
		return pos // 该场景无底图:只回坐标
	}
	pos["u"], pos["v"] = u, v
	mi, ok := p.db.MapInfo(uint32(res))
	if !ok || mi.Side == 0 {
		return pos
	}
	// 速度向量(UE 厘米/秒)按同一投影(纯缩放:u=(x-ox)/side)换算为「归一化底图坐标/秒」,
	// 供前端在两包之间逐帧外推(航位推算),即客户端给其他玩家做平滑的同一套办法。
	// 实测(pcap 回放)上一包 pos+speed*Δt 预测下一包 pos:中位误差 3cm、直线段 3s 也仅几米。
	if !mr.StopMove {
		pos["vu"] = float64(mr.Speed.X) / float64(mi.Side)
		pos["vv"] = float64(mr.Speed.Y) / float64(mi.Side)
	}
	// 客户端沉默一段(直线巡航/推住摇杆盘旋时退化成 2.5-3s 心跳)后补报的真实轨迹:
	// 那几秒里它没报过位置,前端只能外推;等这段轨迹到了就沿它把箭头滑回真实路线上(转弯尤其明显)。
	// 持续操作时上报本就 0.1s 一包(轨迹点为空/极短),不必也不能回放——那会让 0.45s 的滑行跨过好几个
	// 新包,箭头反而落后。故以轨迹跨度为准。
	if mr.SegSpan() >= minSegSpan {
		path := make([]map[string]any, 0, len(mr.Segs)+1)
		for _, sg := range mr.Segs {
			if su, sv, ok := p.db.Project(uint32(res), sg.Pos.X, sg.Pos.Y); ok {
				path = append(path, map[string]any{"u": su, "v": sv})
			}
		}
		// 末段采样略早于包时刻(实测差 0.2–0.6 个采样步长),to_pos 才是最新位置:补作轨迹终点,
		// 前端滑完轨迹正好落在上报位置,与其后的外推无缝衔接。
		if len(path) > 0 {
			if last := path[len(path)-1]; last["u"] != u || last["v"] != v {
				path = append(path, map[string]any{"u": u, "v": v})
			}
		}
		if len(path) >= 2 {
			pos["path"] = path
		}
	}
	return pos
}

// sceneDisplayName 取当前场景显示名:优先 scene_res(区分同一 cfg 下的子场景,如卡洛西亚
// 大陆 vs 魔法学院),缺失时(未见进入/传送通知)回退移动包自带的 scene_cfg_id。
func (p *Pipeline) sceneDisplayName(resID, cfgID int32) string {
	if resID != 0 {
		if n := p.db.SceneResName(resID); n != "" {
			return n
		}
	}
	return p.db.SceneName(cfgID)
}

// ---- 分层地图(洞穴/楼层)选层与去抖 ----

// layerDebounce 是分层地图切换的去抖时长。区域触发体之间有接缝,玩家在洞内正常走动会短暂「擦出」
// 所有区域(实测空窗 0/0/94ms),贴着楼梯口走也会短暂擦进上层(实测 107ms);若照单全收,叠加图就会
// 一闪一闪,看着像层图与底图不同步。而真正的进出层空窗是秒级的(实测 3.8/5.1/15.7s),两者差一个
// 数量级,故只采纳「稳定超过本时长」的变化。代价是进出洞的切换晚 0.3s 可见,无关痛痒。
const layerDebounce = 300 * time.Millisecond

// layerState 是某连接的分层地图去抖状态:cur 为正在显示的层,pend 为待确认的新值。
type layerState struct {
	cur, pend     gamedata.LayerInfo
	curOK, pendOK bool
	since         time.Time // pend 首次出现的时刻
	// fresh:换场景/传送后到**首个移动包**之间的「落地窗口」。此间的区域事件是落地时的权威状态,
	// 不可能是走动擦出接缝的噪声,故直接采纳、不等去抖——否则传送进洞后若站着不动,进入事件会一直
	// 卡在去抖里(没有下一个移动包来推进它),洞穴层图迟迟不出现。
	fresh bool
}

// settle 收一个「按区域算出的层」,返回去抖后应显示的层。
func (ls *layerState) settle(l gamedata.LayerInfo, ok bool, now time.Time) (gamedata.LayerInfo, bool) {
	same := func(a gamedata.LayerInfo, aok bool, b gamedata.LayerInfo, bok bool) bool {
		return aok == bok && (!aok || a.ID == b.ID)
	}
	switch {
	case ls.fresh: // 落地窗口内(换场景后、玩家尚未移动):直接采纳,不等去抖
		ls.cur, ls.curOK = l, ok
		ls.pend, ls.pendOK, ls.since = l, ok, time.Time{}
	case same(l, ok, ls.cur, ls.curOK): // 与在显示的一致:清掉待定
		ls.pend, ls.pendOK, ls.since = l, ok, time.Time{}
	case !same(l, ok, ls.pend, ls.pendOK) || ls.since.IsZero(): // 新的候选:开始计时
		ls.pend, ls.pendOK, ls.since = l, ok, now
	case now.Sub(ls.since) >= layerDebounce: // 候选稳定够久:采纳
		ls.cur, ls.curOK = l, ok
		ls.since = time.Time{}
	}
	return ls.cur, ls.curOK
}

// activeFuncs 把「func → area 集合」压成 func 集合(玩家当前所在的 area_func_id),供选层。
func activeFuncs(funcs map[uint32]map[uint32]bool) map[uint32]bool {
	out := make(map[uint32]bool, len(funcs))
	for fn, set := range funcs {
		if len(set) > 0 {
			out[fn] = true
		}
	}
	return out
}

// layerOf 按当前区域集合定层(经去抖)。fromMove=true 表示由移动包触发:玩家开始动了,
// 落地窗口就此关闭,其后的层变化一律走去抖(滤掉走动擦出/擦进触发体接缝的抖动)。
func (p *Pipeline) layerOf(conn string, res int32, t time.Time, fromMove bool) (gamedata.LayerInfo, bool) {
	cs := p.conn(conn)
	if cs.layer == nil {
		cs.layer = &layerState{fresh: true} // 换场景/传送后的落地窗口(见 layerState.fresh)
	}
	raw, rawOK := p.db.LayerIn(res, activeFuncs(cs.areas))
	l, ok := cs.layer.settle(raw, rawOK, t)
	if fromMove {
		cs.layer.fresh = false
	}
	return l, ok
}

// layerPayload 把某分层地图投影成底图上的归一化矩形(u0,v0)-(u1,v1),前端据此定位切片图
// (透明处透出底图);玩家点仍用底图投影,自然落在矩形内。该场景无底图时返回 nil。
func (p *Pipeline) layerPayload(res int32, l gamedata.LayerInfo) map[string]any {
	mi, ok := p.db.MapInfo(uint32(res))
	if !ok || mi.Side == 0 {
		return nil
	}
	return map[string]any{
		"id":  l.ID, // 涂地按层各存一张,前端据此取对应的覆盖位图(见 docs/data.md 3.8)
		"img": "layer/" + l.Img,
		"u0":  float64(l.OX-mi.OX) / float64(mi.Side),
		"v0":  float64(l.OY-mi.OY) / float64(mi.Side),
		"u1":  float64(l.OX+l.Side-mi.OX) / float64(mi.Side),
		"v1":  float64(l.OY+l.Side-mi.OY) / float64(mi.Side),
	}
}

// settleLayer 在区域事件后重新定层;层变了就推一条只更新分层的消息(不动位置锚点)。
func (p *Pipeline) settleLayer(conn, acc string, t time.Time) {
	cs := p.conns[conn]
	if cs == nil || cs.res == 0 {
		return
	}
	var prev gamedata.LayerInfo
	var prevOK bool
	if cs.layer != nil {
		prev, prevOK = cs.layer.cur, cs.layer.curOK
	}
	l, ok := p.layerOf(conn, cs.res, t, false)
	if ok == prevOK && (!ok || l.ID == prev.ID) {
		return // 层没变
	}
	upd := map[string]any{
		"account":   acc,
		"layerOnly": true, // 前端只叠加/撤下切片图,不动位置锚点
		"ts":        t.Unix(),
		"tsMs":      t.UnixMilli(), // 仅供观测(调试页/回放核对);前端合并时不取
	}
	lastPos := p.acct(acc).lastPos
	if ok {
		upd["layer"], upd["sceneName"] = p.layerPayload(cs.res, l), l.Name
	} else {
		cfg := int32(0)
		if lastPos != nil {
			cfg, _ = lastPos["sceneCfgId"].(int32)
		}
		upd["layer"], upd["sceneName"] = nil, p.sceneDisplayName(cs.res, cfg)
	}
	if lastPos != nil { // 同步进缓存,页面加载(GET /api/position)也能带上分层
		lastPos["layer"], lastPos["sceneName"] = upd["layer"], upd["sceneName"]
		p.srv.SetLastPosition(acc, lastPos)
	}
	p.srv.Hub().Broadcast("position", acc, upd)
}

// saveAreas 把某连接的区域集合落盘(map[func][]area 形式)。
func (p *Pipeline) saveAreas(conn string) {
	out := map[uint32][]uint32{}
	if cs := p.conns[conn]; cs != nil {
		for fn, set := range cs.areas {
			for a := range set {
				out[fn] = append(out[fn], a)
			}
		}
	}
	p.st.SaveSessionAreas(conn, out)
}

// resetAreas 清空某连接的区域与去抖状态(换场景/传送时)。
func (p *Pipeline) resetAreas(conn string) {
	if cs := p.conns[conn]; cs != nil {
		cs.areas, cs.layer = nil, nil
	}
	p.saveAreas(conn)
}

// applyZoneProgress 从进场景回包取按区域的收集进度(服务器口径):
// 前端按候选区域整片隐藏,sweepStars 按 got=0 挡误判。
func (p *Pipeline) applyZoneProgress(m capture.Message, acc string) {
	zp := scene.ParseZoneProgress(m.AppBody)
	if len(zp) == 0 {
		return
	}
	rows := make([]store.ZoneProgressRow, 0, len(zp))
	g := map[int32]int32{}
	for _, z := range zp {
		rows = append(rows, store.ZoneProgressRow{Camp: z.Camp, NpcID: z.NpcID, Got: z.Got, Total: z.Total})
		g[z.Camp] += z.Got
	}
	p.acct(acc).zoneGot = g
	p.st.SetStarZones(acc, rows)
	p.srv.Hub().Broadcast("starzones", acc, rows)
}
