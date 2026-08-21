package pipeline

import (
	"sort"
	"strconv"
	"time"

	"github.com/whoisnian/rocom-capture/internal/gamedata"
	"github.com/whoisnian/rocom-capture/internal/pet"
	"github.com/whoisnian/rocom-capture/internal/scene"
)

// ---- 实时地图的野生宠物图层(见 docs/data.md 3.5)----
//
// 大世界的野生宠物是普通 NPC 实体(ActorInfo),个体属性就挂在 npc_base 上:身高、体重、嗓音、
// 变异(mutation_type)、炫彩(glass_info)——**捕捉后这些原样进 PetData**(2026-08-02 用捕捉
// 珀尔鼬/友爱天天的 pcap 逐字段核对过),所以丢球之前就能筛。实体来源与星星同两处:进场景/传送后的周边快照(0x014a)
// 与移动中随 AOI 补发的动作通知(0x0413/0x0414 的 actor_enter)。
//
// **下发范围**:普通野生宠是水平 80m 的圆(高度不计,走到 88-90m 才收到 actor_leave),首领 150/200m;
// 跨进边界到实体真正下发另有滞后(中位 6.3s、最大 47s),故「附近没标记」不等于「附近没有」。
// 见 docs/data.md 3.7。
//
// 只跟踪**值得跑一趟**的几类(其余野生宠满地都是,全画出来只会糊住地图):
//   - 炫彩(glass_info.glass_type != GT_NULL,等价于 mutation_type 的 MDT_GLASS 位);
//   - 异色(mutation_type 的 MDT_SHINING 位);
//   - 污染(mutation_type 的 MDT_CHAOS 家族);这类丢球即进战斗,打完才解除污染。
//   - 嗓音取到极值(voice == pet.VoiceHigh / pet.VoiceLow):两端各对应「婉转声」/「粗嗓门」奖牌
//     的百分位边界,且都是随机不出第二次的顶格值,故两端各给一个图层。
//
// 炫彩不另按 mutation 位判:两者严格等价(全部 pcap 363 只变异宠物零反例),
// 用 glass_info 还能顺带说出是哪一种炫彩。MDT_VACANT(空缺态)客户端自己都不出变异标,忽略。
//
// **标记位置是实体下发时的位置,之后不再更新**:野生宠的 AI 跑在客户端(NpcBase.is_server_ai
// 为假),它在刷新点附近的溜达根本不过网——16 份 pcap 里 server_move 只出现 1 次、client_move
// 全是玩家 avatar,没有一条属于野生宠。故位置≈刷新点,误差是它自己绕的那几米。
const (
	// 出 AOI 后「最后所见」的灰点还留多久(超时由 pushWilds 顺手丢弃)。取 4 小时是为了
	// 让灰点当作「本次上线在这一带见过什么」的备忘:野生宠刷新周期远长于几分钟,隔一阵回来
	// 多半还在。灰点不会无限堆积——换场景即清空,自己捉走的当场撤。
	wildStaleTTL = 4 * time.Hour
)

// wildPet 是一只被跟踪的野生宠物(当前场景会话内)。
type wildPet struct {
	actorID    uint64
	cfgID      int32
	lv         int32
	height     int32
	weight     int32
	voice      int32
	mutation   int32
	glassType  int32
	glassValue int32
	pos        scene.Position
	seenAt     time.Time // 最近一次确认它还在 AOI 里的时刻
	left       bool      // 已离开 AOI:标记转为「最后所见」,置灰显示,wildStaleTTL 后丢弃
}

// wildTracker 是一个连接在当前场景会话内的野生宠物观测态(换场景即重置;同场景内传送只置灰,
// 见 resetWilds)。
type wildTracker struct {
	pets map[uint64]*wildPet
	res  int32
}

func newWildTracker(res int32) *wildTracker {
	return &wildTracker{pets: map[uint64]*wildPet{}, res: res}
}

// wildMatch 报告该实体是否是要标出的野生宠物。两道闸各有分工:
//   - IsWildPet:实体自带身高体重 ⇒ 是一只宠物实体(静态 NPC/采集物/星星都没有这两项);
//   - NpcPetBase:该 NPC 在**可丢球捕捉**清单里 ⇒ 是野外能抓的,不是家园里摆着的自己的宠物
//     (实测家园场景的宠物 NPC——幽星光 710346、鸭吉吉 710012 等——同样带身高体重叫声,
//     只靠第一道闸会把它们也标出来;它们的 NPC_CONF 没有 throwing_interact_type)。
func (p *Pipeline) wildMatch(a scene.NpcActor) bool {
	if !a.IsWildPet() || len(wildKinds(a)) == 0 {
		return false
	}
	_, catchable := p.db.NpcPetBase(uint32(a.CfgID))
	return catchable
}

// wildKinds 返回该实体命中的类别键;一只可同时命中多类。前端 WILD_LAYERS 的每个开关
// 覆盖其中一个或多个(异色/炫彩合成一个开关),悬浮提示则仍按这里的细粒度分开说。
func wildKinds(a scene.NpcActor) []string {
	var out []string
	if a.GlassType != gamedata.GlassNull {
		out = append(out, "colorful")
	}
	if a.IsShiny() {
		out = append(out, "shiny")
	}
	if a.IsPolluted() {
		out = append(out, "pollution")
	}
	// 嗓音顶格(±100,PET_GLOBAL_CONFIG.pet_voice_high/low);两端分别是「婉转声」/「粗嗓门」
	// 那两枚奖牌的极限值,故分成两个类别,前端两个开关各管一端。
	if a.Voice == pet.VoiceHigh {
		out = append(out, "voiceMax")
	}
	if a.Voice == pet.VoiceLow {
		out = append(out, "voiceMin")
	}
	return out
}

// resetWilds 换场景时重置野生宠物观测态并推空列表(前端随即清掉上个场景的标记)。
//
// **同一场景内的传送不算换场景**(res 不变:大地图传送点、营地魔力之源之间跳来跳去,实测
// 2026-08-20 那份 pcap 的 0x015c 是 res 10003 → 10003):旧标记的世界坐标仍属这张底图,
// 玩家走回去还能再遇上,只是此刻不在 AOI 里了——这与「走远了出 AOI」是同一回事,
// 故一律转成「最后所见」置灰保留(seenAt 不动,TTL 仍从最后一次确认它在算起),而不是抹掉。
// 服务器传送时不为旧实体补发 actor_leave(客户端自己清空 AOI),故只能在这里代劳。
func (p *Pipeline) resetWilds(conn, acc string, res int32, now time.Time) {
	cs := p.conn(conn)
	if ts := cs.wilds; ts != nil && ts.res == res {
		for _, w := range ts.pets {
			w.left = true
		}
	} else { // 真换了场景(或首次进场景):上个场景的实体与坐标一律作废
		cs.wilds = newWildTracker(res)
	}
	p.pushWilds(conn, acc, now)
}

// observeWilds 收下一个实体快照/AOI 通知里的野生宠物:匹配的进入即跟踪,离开转为「最后所见」。
// snapshot=true 表示来自进场景快照(0x014a),否则是 AOI 动作通知(0x0413/0x0414)。
func (p *Pipeline) observeWilds(conn, acc string, body []byte, now time.Time, snapshot bool) {
	cs := p.conns[conn]
	if cs == nil || cs.wilds == nil {
		return
	}
	ts := cs.wilds
	changed := false

	actors := scene.ParseActorEnter(body)
	if snapshot {
		actors = scene.ParseSceneActors(body)
	}
	for _, a := range actors {
		// 涂地跟的是**全部**野生宠实体(见 paintSeen):任何一只下发,都证明玩家到它之间
		// 这条线上的东西已经到手了,与它稀不稀有无关。下面的 wildMatch 才是地图标记那一套。
		// **首领除外**:它的下发距离是 150/200m 那两档(普通野生宠 80m,见 docs/data.md 3.7),
		// 拿它当凭据会把中间那段其实没下发过普通野生宠的地方也涂上。
		// 同理只认**可丢球捕捉**的(同 wildMatch 的第二道闸):家园宠、剧情/活动 NPC 也带身高体重,
		// 但它们不是野外刷出来的,下发规则未知,不该拿来当「这条线扫过了」的凭据。
		if _, catchable := p.db.NpcPetBase(uint32(a.CfgID)); a.IsWildPet() && catchable && !p.db.IsNpcBoss(uint32(a.CfgID)) {
			if cs.wildSeen == nil {
				cs.wildSeen = map[uint64]scene.Position{}
			}
			cs.wildSeen[a.ActorID] = a.Pos
		}
		if !p.wildMatch(a) {
			continue
		}
		if old, ok := ts.pets[a.ActorID]; ok { // 重新进入 AOI:复活标记
			old.pos, old.seenAt, old.left = a.Pos, now, false
			changed = true
			continue
		}
		ts.pets[a.ActorID] = &wildPet{
			actorID: a.ActorID, cfgID: a.CfgID, lv: a.Lv,
			height: a.Height, weight: a.Weight, voice: a.Voice,
			mutation: a.Mutation, glassType: a.GlassType, glassValue: a.GlassValue,
			pos: a.Pos, seenAt: now,
		}
		changed = true
	}

	// 离开 AOI:不立刻抹掉,置灰保留一段时间,免得刚瞥见一只稀有的、一转身标记就没了;
	// 超过 wildStaleTTL 由 pushWilds 清理。**自己捉走的另算**(见下),那种要当场撤。
	for _, id := range scene.ParseActorLeave(body) {
		delete(cs.wildSeen, id) // 出了视野就不再从它身上涂(走廊只代表「此刻确实看得见」)
		if w, ok := ts.pets[id]; ok && !w.left {
			w.left = true
			changed = true
		}
	}

	// 自己丢球捉走的:实体不是「走远了」而是真没了,标记当场撤掉,不留灰点。
	// (捕捉失败的 act 同样会来,带 is_catch_success=false,ParseCaughtByThrow 已挡掉。)
	for _, id := range scene.ParseCaughtByThrow(body) {
		delete(cs.wildSeen, id)
		if _, ok := ts.pets[id]; ok {
			delete(ts.pets, id)
			changed = true
		}
	}

	// 新看到的宠物 = 新的方向,当场涂一次(玩家可能站着不动,等不到下一个移动包)。
	p.paintSeen(conn, acc, cs.res, nil)

	if changed {
		p.pushWilds(conn, acc, now)
	}
}

// onBattleFinish 处理战斗结算(0x132c):被污染的野生宠丢球只是开战,结果要等这里
// (见 docs/data.md 3.5)。捉走与打死对标记是一回事——那儿已经没这只了,当场撤掉;
// 战斗期间它早已离开 AOI 被置灰,这一步把灰点也抹掉。打输/它逃跑则不在此列,灰点照旧。
func (p *Pipeline) onBattleFinish(conn, acc string, body []byte, now time.Time) {
	cs := p.conns[conn]
	if cs == nil || cs.wilds == nil {
		return
	}
	changed := false
	for _, id := range scene.ParseBattleGoneNpcs(body) {
		if _, ok := cs.wilds.pets[id]; ok {
			delete(cs.wilds.pets, id)
			changed = true
		}
	}
	// 新看到的宠物 = 新的方向,当场涂一次(玩家可能站着不动,等不到下一个移动包)。
	p.paintSeen(conn, acc, cs.res, nil)

	if changed {
		p.pushWilds(conn, acc, now)
	}
}

// wildMark 是推送给前端的一只野生宠物标记(u/v 已按底图投影,与玩家位置同一套)。
type wildMark struct {
	ID     string   `json:"id"`            // actor_id;uint64 超出 JS 安全整数,用字符串
	Name   string   `json:"n"`             // 形态名(珀尔鼬…);表里查不到时为空
	Img    string   `json:"img,omitempty"` // 头像相对路径 HeadIcon/<n>.webp
	Kinds  []string `json:"kinds"`         // 命中的类别:colorful / shiny / pollution / voiceMax / voiceMin
	U      float64  `json:"u"`
	V      float64  `json:"v"`
	X      int32    `json:"x"`
	Y      int32    `json:"y"`
	Z      int32    `json:"z"`
	Lv     int32    `json:"lv,omitempty"`
	Voice  int32    `json:"voice"`
	Height int32    `json:"height,omitempty"`
	Weight int32    `json:"weight,omitempty"`
	// 体重在本形态取值范围内的百分位(0-100),与宠物列表/事件页的「W xx%」同一口径
	// (pet.SizePercentile);形态范围缺失时为 nil。
	WeightPct *float64 `json:"weightPct,omitempty"`
	Glass     string   `json:"glass,omitempty"`    // 炫彩外观描述(暗夜拾光 / 四角星·亮X暗 - 浅紫橙);空=非炫彩
	Mutation  int32    `json:"mutation,omitempty"` // 原始 mutation_type 位标志(排查用)
	Stale     bool     `json:"stale,omitempty"`    // 已离开 AOI:位置是最后所见,前端置灰
}

// pushWilds 缓存并广播当前场景的野生宠物标记(顺带清理过期的「最后所见」)。
// 只在成员/状态真的变了时调用:实体进出 AOI 是低频事件,不必节流。
// now 取**消息时刻**而非 time.Now():离线回放的包时间是几小时前的,用挂钟一比就全过期了。
func (p *Pipeline) pushWilds(conn, acc string, now time.Time) {
	cs := p.conns[conn]
	if cs == nil || cs.wilds == nil {
		return
	}
	ts := cs.wilds
	marks := []wildMark{}
	for id, w := range ts.pets {
		if w.left && now.Sub(w.seenAt) > wildStaleTTL {
			delete(ts.pets, id)
			continue
		}
		u, v, ok := p.db.Project(uint32(ts.res), w.pos.X, w.pos.Y)
		if !ok { // 该场景无底图:投影无从谈起,标记也就无处可画
			continue
		}
		m := wildMark{
			ID: strconv.FormatUint(w.actorID, 10), U: u, V: v,
			X: w.pos.X, Y: w.pos.Y, Z: w.pos.Z,
			Lv: w.lv, Voice: w.voice, Height: w.height, Weight: w.weight,
			Mutation: w.mutation, Stale: w.left,
			Kinds: wildKinds(scene.NpcActor{Voice: w.voice, Mutation: w.mutation, GlassType: w.glassType}),
		}
		if w.glassType != gamedata.GlassNull {
			m.Glass = p.db.GlassDesc(w.glassType, w.glassValue)
			if m.Glass == "" { // 配置里查不到(新赛季款/新色号)时至少标出是炫彩
				m.Glass = "炫彩"
			}
		}
		if base, ok := p.db.NpcPetBase(uint32(w.cfgID)); ok {
			if info, ok := p.db.PetBase(base); ok {
				m.Name = info.Name
				// 体重单位与 PetData 一致(÷1000 千克),百分位口径同宠物列表。
				m.WeightPct = pet.SizePercentile(float64(w.weight)/1000,
					float64(info.WeightLow)/1000, float64(info.WeightHigh)/1000)
			}
			// 异色个体有专属头像的就用异色版(无则 PetImageByBase 自动回退普通)。
			m.Img = p.db.PetImageByBase(base, w.mutation&scene.MutationShiny != 0).Head
		}
		marks = append(marks, m)
	}
	// 顺序稳定(前端按 id 作 key,免得每次推送都重排 DOM)。
	sort.Slice(marks, func(i, j int) bool { return marks[i].ID < marks[j].ID })

	payload := map[string]any{"account": acc, "sceneResId": ts.res, "pets": marks}
	p.srv.SetLastWildPets(acc, payload)
	p.srv.Hub().Broadcast("wildpets", acc, payload)
}
