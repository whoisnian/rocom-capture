package scene

import "google.golang.org/protobuf/encoding/protowire"

// 眠枭之星的收集状态判定(见 docs/data.md 3.4)。
//
// 核心事实(已用 pcap 实测):**已收集的星星服务器根本不刷**——只有未收集的才会作为 NPC 实体
// (ActorInfo)下发,且实体带 `npc_content_cfg_id` = 刷新点 id(NPC_REFRESH_CONTENT_CONF.id),
// 与 names.json 里 POI 的 `r` 一一对应。故:
//
//	收到某刷新点的实体            ⇒ 该点**未收集**
//	玩家走到该点附近却没收到实体  ⇒ 该点**已收集**
//
// 实体有两个来源:进场景/传送后的周边快照(OpEnterSceneFinishAck),以及移动中随 AOI 变化补发的
// 区域动作通知(OpPlayActsNotify / OpPlayActsBatchNotify 的 actor_enter)。
const (
	OpEnterSceneFinishAck = 0x014a // ZONE_SCENE_CLIENT_ENTER_SCENE_FINISH_NTY_ACK,s2c:周边实体快照
	OpPlayActsBatchNotify = 0x0413 // ZONE_SCENE_PLAY_ACTS_BATCH_NOTIFY,s2c:批量区域动作(同 0x0414)
)

// 眠枭之星的 NPC_CONF id:A1=蓝、A2=黄;「之星」与「光点」两种形态都算(见 gen_gamedata 的图层)。
var starNpc = map[int32]bool{55162: true, 55163: true, 55500: true, 55510: true}

// NpcActor 是服务器下发的一个 NPC 实体(只取判定收集状态需要的字段)。
type NpcActor struct {
	ActorID   uint64 // base.actor_id;离开 AOI/被收走时服务器只给这个 id(见 ParseActorLeave)
	CfgID     int32  // npc_cfg_id(NPC_CONF.id)
	RefreshID int32  // npc_content_cfg_id(NPC_REFRESH_CONTENT_CONF.id),对应 POI.R
	Pos       Position
}

// IsStar 报告该实体是不是眠枭之星(含光点形态)。
func (a NpcActor) IsStar() bool { return starNpc[a.CfgID] }

// ParseSceneActors 从 s2c ZoneSceneClientEnterSceneFinishNtyAck(0x014a)取周边实体快照:
// other_actors(field 7,重复 ActorInfo)。进场景/传送后下发一次。
func ParseSceneActors(body []byte) []NpcActor {
	var out []NpcActor
	scanFields(body, func(num protowire.Number, typ protowire.Type, val []byte, _ uint64) {
		if num == 7 && typ == protowire.BytesType {
			if a, ok := parseActorInfo(val); ok {
				out = append(out, a)
			}
		}
	})
	return out
}

// ParseActorEnter 从 s2c ZoneScenePlayActsNotify/BatchNotify(0x0414/0x0413)取新进入 AOI 的实体:
// acts(1,SpaceActionCollection) → actor_enter(1,SpaceAct_ActorEnter) → actors(1,重复 ActorInfo)。
// 批量包(0x0413)里 acts 出现多次,scanFields 会逐个回调,故两者同一套解析。
func ParseActorEnter(body []byte) []NpcActor {
	var out []NpcActor
	scanFields(body, func(num protowire.Number, typ protowire.Type, acts []byte, _ uint64) {
		if num != 1 || typ != protowire.BytesType { // acts
			return
		}
		scanFields(acts, func(n2 protowire.Number, t2 protowire.Type, enter []byte, _ uint64) {
			if n2 != 1 || t2 != protowire.BytesType { // actor_enter
				return
			}
			scanFields(enter, func(n3 protowire.Number, t3 protowire.Type, actor []byte, _ uint64) {
				if n3 != 1 || t3 != protowire.BytesType { // actors
					return
				}
				if a, ok := parseActorInfo(actor); ok {
					out = append(out, a)
				}
			})
		})
	})
	return out
}

// parseActorInfo 解 ActorInfo:npc(11) → {base(1).pt(8).pos(1), npc_base(3)}。
// npc_base:npc_cfg_id(1)、npc_content_cfg_id(10)。非 NPC 实体(玩家/宠物)返回 ok=false。
func parseActorInfo(b []byte) (NpcActor, bool) {
	var a NpcActor
	npc := subMsg(b, 11)
	if npc == nil {
		return a, false
	}
	if nb := subMsg(npc, 3); nb != nil {
		scanFields(nb, func(num protowire.Number, typ protowire.Type, _ []byte, v uint64) {
			if typ != protowire.VarintType {
				return
			}
			switch num {
			case 1:
				a.CfgID = int32(v)
			case 10:
				a.RefreshID = int32(v)
			}
		})
	}
	if base := subMsg(npc, 1); base != nil {
		scanFields(base, func(num protowire.Number, typ protowire.Type, _ []byte, v uint64) {
			if num == 2 && typ == protowire.VarintType { // actor_id
				a.ActorID = v
			}
		})
	}
	if pt := subMsg(subMsg(npc, 1), 8); pt != nil { // base.pt
		if pos := subMsg(pt, 1); pos != nil { // pt.pos
			scanFields(pos, func(num protowire.Number, typ protowire.Type, _ []byte, v uint64) {
				if typ != protowire.VarintType {
					return
				}
				switch num {
				case 1:
					a.Pos.X = int32(v)
				case 2:
					a.Pos.Y = int32(v)
				case 3:
					a.Pos.Z = int32(v)
				}
			})
		}
	}
	return a, a.CfgID != 0
}

// ParseActorLeave 从 0x0414/0x0413 取离开 AOI 的实体 id:
// acts(1) → actor_leave(2,SpaceAct_ActorLeave) → actor_ids(1,重复 uint64)。
//
// 「离开」既可能是走远出了 AOI,也可能是**星星被玩家收走**。两者只能靠距离区分:玩家不可能
// 隔着几十米收集,故只在玩家就在旁边时才据此判已收集(见 cmd/rocom-capture 的 starCollectRadius)。
func ParseActorLeave(body []byte) []uint64 {
	var out []uint64
	scanFields(body, func(num protowire.Number, typ protowire.Type, acts []byte, _ uint64) {
		if num != 1 || typ != protowire.BytesType {
			return
		}
		scanFields(acts, func(n2 protowire.Number, t2 protowire.Type, leave []byte, _ uint64) {
			if n2 != 2 || t2 != protowire.BytesType { // actor_leave
				return
			}
			scanFields(leave, func(n3 protowire.Number, t3 protowire.Type, packed []byte, v uint64) {
				if n3 != 1 {
					return
				}
				if t3 == protowire.VarintType {
					out = append(out, v)
					return
				}
				if t3 == protowire.BytesType { // packed repeated
					rest := packed
					for len(rest) > 0 {
						x, n := protowire.ConsumeVarint(rest)
						if n < 0 {
							return
						}
						out = append(out, x)
						rest = rest[n:]
					}
				}
			})
		})
	})
	return out
}

// ZoneProgress 是某区域某类星星的收集进度(服务器口径,进场景包下发)。
type ZoneProgress struct {
	Camp  int32 // 区域键 = 该区域营地(魔力之源)的刷新点 id;names.json 的 zones 给中文名
	NpcID int32 // 星星 NPC id(55162 蓝 / 55163 黄 / 55500,55510 光点)
	Got   int32 // 已收集
	Total int32 // 总数(服务器口径,与配置候选点数不同,见 docs/data.md 3.4)
}

// ParseZoneProgress 从 s2c ZoneEnterSceneRsp(0x0152)取按区域的收集进度:
//
//	self_info(11) → avatar(12) → world_map_info(19) → layered_world_map_explore_info(4)
//	  → explore_infos(1,重复) = {npc_id(1), belong_camp(2), explore_num(3), total_num(4)}
//
// 只回眠枭之星那几个 npc(同表里还有精灵果实等其它可收集物)。
func ParseZoneProgress(body []byte) []ZoneProgress {
	wm := subMsg(subMsg(subMsg(body, 11), 12), 19)
	if wm == nil {
		return nil
	}
	var out []ZoneProgress
	scanFields(subMsg(wm, 4), func(num protowire.Number, typ protowire.Type, one []byte, _ uint64) {
		if num != 1 || typ != protowire.BytesType {
			return
		}
		var p ZoneProgress
		scanFields(one, func(n protowire.Number, t protowire.Type, _ []byte, v uint64) {
			if t != protowire.VarintType {
				return
			}
			switch n {
			case 1:
				p.NpcID = int32(v)
			case 2:
				p.Camp = int32(v)
			case 3:
				p.Got = int32(v)
			case 4:
				p.Total = int32(v)
			}
		})
		if starNpc[p.NpcID] && p.Camp != 0 {
			out = append(out, p)
		}
	})
	return out
}

// subMsg 取 b 里首个指定字段号的子消息(length-delimited);没有返回 nil。
func subMsg(b []byte, want protowire.Number) []byte {
	var found []byte
	scanFields(b, func(num protowire.Number, typ protowire.Type, val []byte, _ uint64) {
		if found == nil && num == want && typ == protowire.BytesType {
			found = val
		}
	})
	return found
}
