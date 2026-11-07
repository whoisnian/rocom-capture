package scene

import (
	"google.golang.org/protobuf/encoding/protowire"

	"github.com/whoisnian/rocom-capture/internal/wire"
)

// 眠枭之星的收集状态判定(见 docs/map.md 4)。
//
// 核心事实(已用 pcap 实测):星/光点**已收集的服务器根本不刷**——只有未收集的才会作为 NPC 实体
// (ActorInfo)下发,且实体带 `npc_content_cfg_id` = 刷新点 id(NPC_REFRESH_CONTENT_CONF.id),
// 与 names.json 里 POI 的 `r` 一一对应。故:
//
//	收到某刷新点的实体            ⇒ 该点**未收集**
//	玩家走到该点附近却没收到实体  ⇒ 该点**已收集**
//
// **石像是例外**:本体收集后不消失、实体一直下发,收集状态在实体的挂件字段里(NpcActor.Pendant);
// 星星魔法命中后石像上方浮现一颗星,触碰收集 = c2s 挂件交互(OpNpcPendantInteractReq,带刷新行 id)。
//
// 实体有两个来源:进场景/传送后的周边快照(OpEnterSceneFinishAck),以及移动中随 AOI 变化补发的
// 区域动作通知(OpPlayActsNotify / OpPlayActsBatchNotify 的 actor_enter)。
const (
	OpEnterSceneFinishAck   = 0x014a // ZONE_SCENE_CLIENT_ENTER_SCENE_FINISH_NTY_ACK,s2c:周边实体快照
	OpPlayActsBatchNotify   = 0x0413 // ZONE_SCENE_PLAY_ACTS_BATCH_NOTIFY,s2c:批量区域动作(同 0x0414)
	OpNpcPendantInteractReq = 0x0272 // ZONE_SCENE_NPC_PENDANT_INTERACT_REQ,c2s:触碰石像的星(收集)
	OpNpcPendantInteractRsp = 0x0273 // ZONE_SCENE_NPC_PENDANT_INTERACT_RSP,s2c:回包(ret=0 成功)
)

// 可收集物的 NPC_CONF id。眠枭之星:A1=蓝、A2=黄、A2-2=紫;「之星」「光点」
// 「石像」三种形态都算一颗星(光点交互后出一颗星;石像被星星魔法命中后浮现一颗星,触碰收集)。
// 不咕钟零件(55901)是收集品,实体行为与星/光点同套(未收集才刷)。
// 与 gen_gamedata.py 的 NPC_WHITELIST 同一批;蓝 147/黄 228/紫 104 的构成见 docs/map.md 3。
var starNpc = map[int32]bool{
	55162: true, 55163: true, 55601: true, // 独立星
	55500: true, 55510: true, 55602: true, // 光点
	58308: true, 58318: true, 55632: true, // 石像
	55901: true, // 不咕钟零件(51 点,无分区计数,见 docs/map.md 3)
}

// 石像与星/光点的**实体行为不同**(见 docs/map.md 4):石像本体收集后不消失、实体一直下发,
// 「出现/消失」不携带任何收集信息;它的星是实体上的**挂件**(pendant),状态在 ActorInfo 里
// (见 NpcActor.Pendant),收集动作是 c2s 挂件交互(OpNpcPendantInteractReq)。
var statueNpc = map[int32]bool{58308: true, 58318: true, 55632: true}

// 挂件(石像的星)状态,ActorInfo 的 NpcPendantItemInfo.status 取值。全集见客户端枚举
// ProtoEnum.PendantItemStatus:PIS_NONE=0、PIS_DISABLE=1、PIS_ENABLE=2、PIS_TRANSPARENT=3
// (Scene/Component/Pendant)。14 份 pcap 实测石像只出现过 1/2 两值。
//
// 判「这颗星该不该在地图上显示」= 是否仍可收集:客户端 AttacheeComponent 在
// status∈{ENABLE(2),TRANSPARENT(3)} 时才发收集请求,故这两者都是「未收集/仍显示」;
// NONE(0) 是初始态(挂件字段常缺省),同样保守显示。只有 DISABLE(1) 才是已收走。
// (注:PendantComponent:IsAllCollected 把 TRANSPARENT 也算「已完成」,那是 UI 全收集徽章
// 的另一口径,与地图显示无关,勿据此把 3 判为已收集。)
// 故下面只需区分「已收走(1)」与「其余一律仍显示」,starSee/parseActorInfo 即按此二分。
const (
	PendantUncollected = 2 // PIS_ENABLE:星还挂着(未收集);0/3 同样走「仍显示」分支
	PendantCollected   = 1 // PIS_DISABLE:已收走
)

// NpcActor 是服务器下发的一个 NPC 实体(只取判定收集状态与野生宠物图层需要的字段)。
type NpcActor struct {
	ActorID   uint64 // base.actor_id;离开 AOI/被收走时服务器只给这个 id(见 ParseActorLeave)
	CfgID     int32  // npc_cfg_id(NPC_CONF.id)
	RefreshID int32  // npc_content_cfg_id(NPC_REFRESH_CONTENT_CONF.id),对应 POI.R
	Pendant   int32  // 挂件状态(仅石像有:PendantUncollected/PendantCollected;其余为 0)
	Pos       Position
	// 以下为**野生宠物**独有的个体属性(静态 NPC 的 npc_base 里根本没有这些字段)。
	// 捕捉后原样进 PetData,故丢球前就能筛;判据与坑见 docs/map.md 5。
	Lv         int32 // base.lv
	BornTime   int64 // base.born_time(秒级时间戳;世界 NPC 的出生时刻,大龙棋士据此算 4 小时存活期)
	Height     int32 // npc_base.height(÷100=米)
	Weight     int32 // npc_base.weight(÷1000=千克)
	Voice      int32 // npc_base.voice(嗓音,-100~100)
	Mutation   int32 // npc_base.mutation_type,位标志,见下 Mutation* 常量(与 PetData 同一套)
	GlassType  int32 // npc_base.glass_info.glass_type(0=GT_NULL 非炫彩 / 1=普通炫彩 / 2=隐藏炫彩)
	GlassValue int32 // npc_base.glass_info.glass_value(是哪一种炫彩,见 gamedata.DB.GlassDesc)
	// 以下为**家园**实体独有(见 home.go 与 docs/eggs.md):
	AttachItem uint64   // attach_item_info.attach_item_id;窝上的蛋即所属小窝的 furniture_guid
	HomePet    *HomePet // 入住小窝的宠物(home_pet_info);非家园宠物实体为 nil
	// 以下为赛季奇遇棋子独有(npc_bonus_info):chess_emotion_id 非 0 表示是龙棋士;
	// chess_drop_dice 为真的是**大**龙棋士(客户端据此放大到 110%,否则缩到 80%,见
	// SceneNpc:GetBonusInfoDiceSize)。
	ChessEmotionID int32
	ChessDropDice  bool
}

// 变异位标志,取自客户端 Enum.MutationDiffType(npc_base 与 PetData 的 mutation_type 同一套)。
// MDT_GLASS(炫彩)与 glass_info 非空严格等价,故不在此单列——炫彩看 GlassType 即可(见 3.5)。
// MDT_CHAOS 家族即玩家口中的**污染**(游戏文案「被邪恶气息污染的精灵」「污染血脉」;
// 客户端渲染函数叫 SetNightmare*,是同一件事的两种叫法)。
const (
	MutationShiny           = 1   // MDT_SHINING,异色
	MutationChaos           = 2   // MDT_CHAOS,噩梦一型
	MutationChaosTwo        = 4   // MDT_CHAOS_TWO,噩梦二型
	MutationGlass           = 8   // MDT_GLASS,炫彩(≡ glass_info 非空)
	MutationChaosThree      = 32  // MDT_CHAOS_THREE,噩梦(按 id 掩码)
	MutationVacant          = 64  // MDT_VACANT,空缺态;客户端 UIUtils 不给它出变异标,本项目同样忽略
	MutationChaosPrimordial = 128 // MDT_CHAOS_PRIMORDIAL,太初噩梦
	mutationPolluted        = MutationChaos | MutationChaosTwo | MutationChaosThree | MutationChaosPrimordial
)

// IsShiny 报告该实体是不是异色个体(MDT_SHINING)。
func (a NpcActor) IsShiny() bool { return a.Mutation&MutationShiny != 0 }

// IsPollutedMutation 报告 mutation_type 是否带 MDT_CHAOS 家族位(野外「污染」状态)。
// 野生宠实体与战斗里的 PetData 用同一套位标志,故两处共用。
func IsPollutedMutation(t uint32) bool { return t&uint32(mutationPolluted) != 0 }

// IsPolluted 报告该实体是不是被污染的个体(MDT_CHAOS 家族)。野外实测全是 MDT_CHAOS_TWO(4)。
// 污染个体**不能直接丢球捉**:丢球即进战斗,打空血量才解除污染,见 docs/map.md 5。
func (a NpcActor) IsPolluted() bool { return IsPollutedMutation(uint32(a.Mutation)) }

// IsStar 报告该实体是不是收集判定关心的可收集物(眠枭之星含光点/石像形态,及不咕钟零件)。
func (a NpcActor) IsStar() bool { return starNpc[a.CfgID] }

// IsWildPet 报告该实体是不是野生宠物。判据是**实体自带身高体重**:这两项由服务器按
// PETBASE_CONF 的 height_low/high、weight_low/high 逐个体随机后下发,静态 NPC(NPC/传送点/
// 采集物/星星)的 npc_base 里没有这两个字段。不查配置表,故新版本加宠物也无需更表。
func (a NpcActor) IsWildPet() bool { return a.Height > 0 && a.Weight > 0 }

// IsStatue 报告该实体是不是眠枭石像(收集状态看 Pendant,不看实体存在与否)。
func (a NpcActor) IsStatue() bool { return statueNpc[a.CfgID] }

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

// parseActorInfo 解 ActorInfo:npc(11) → {base(1).pt(8).pos(1), npc_base(3), pendant_info(11),
// npc_bonus_info(27)}。npc_base:npc_cfg_id(1)、npc_content_cfg_id(10),及野生宠物个体属性
// height(11)/weight(12)/mutation_type(14)/glass_info(30)/voice(31);base 另取 born_time(5)/lv(11);
// npc_bonus_info 取赛季棋子的大/小判据 chess_emotion_id(2)/chess_drop_dice(3)。非 NPC 实体返回 ok=false。
// pendant_info(ActorInfo_NpcPendant):pendant_item_infos(3,重复 NpcPendantItemInfo)→ status(4),
// 即石像上那颗星的状态(见 NpcActor.Pendant;石像只有一个挂件,取「有未收集则未收集」)。
func parseActorInfo(b []byte) (NpcActor, bool) {
	var a NpcActor
	npc := subMsg(b, 11)
	if npc == nil {
		return a, false
	}
	if nb := subMsg(npc, 3); nb != nil {
		scanFields(nb, func(num protowire.Number, typ protowire.Type, val []byte, v uint64) {
			if num == 30 && typ == protowire.BytesType { // glass_info(GlassInfo)
				scanFields(val, func(n protowire.Number, t protowire.Type, _ []byte, gv uint64) {
					if t != protowire.VarintType {
						return
					}
					switch n {
					case 1:
						a.GlassType = int32(gv)
					case 2:
						a.GlassValue = int32(gv)
					}
				})
				return
			}
			if typ != protowire.VarintType {
				return
			}
			switch num {
			case 1:
				a.CfgID = int32(v)
			case 10:
				a.RefreshID = int32(v)
			case 11:
				a.Height = int32(v)
			case 12:
				a.Weight = int32(v)
			case 14:
				a.Mutation = int32(v)
			case 31:
				a.Voice = int32(v) // int32,可为负(varint 补码 10 字节),转换即得
			}
		})
	}
	if pend := subMsg(npc, 11); pend != nil {
		scanFields(pend, func(num protowire.Number, typ protowire.Type, item []byte, _ uint64) {
			if num != 3 || typ != protowire.BytesType { // pendant_item_infos
				return
			}
			scanFields(item, func(n protowire.Number, t protowire.Type, _ []byte, v uint64) {
				if n == 4 && t == protowire.VarintType { // status
					if int32(v) == PendantUncollected || a.Pendant == 0 {
						a.Pendant = int32(v)
					}
				}
			})
		})
	}
	if att := subMsg(npc, 23); att != nil { // attach_item_info(ActorInfo_AttachItem)
		scanFields(att, func(num protowire.Number, typ protowire.Type, _ []byte, v uint64) {
			if num == 2 && typ == protowire.VarintType {
				a.AttachItem = v
			}
		})
	}
	if bonus := subMsg(npc, 27); bonus != nil { // npc_bonus_info(ActorInfo_NpcBonus)
		scanFields(bonus, func(num protowire.Number, typ protowire.Type, _ []byte, v uint64) {
			if typ != protowire.VarintType {
				return
			}
			switch num {
			case 2: // chess_emotion_id:非 0 = 龙棋士
				a.ChessEmotionID = int32(v)
			case 3: // chess_drop_dice:true = 大龙棋士
				a.ChessDropDice = v != 0
			}
		})
	}
	a.HomePet = parseHomePet(npc)
	if base := subMsg(npc, 1); base != nil {
		scanFields(base, func(num protowire.Number, typ protowire.Type, _ []byte, v uint64) {
			if typ != protowire.VarintType {
				return
			}
			switch num {
			case 2:
				a.ActorID = v
			case 5:
				a.BornTime = int64(v)
			case 11:
				a.Lv = int32(v)
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
	// 家园里入住小窝的宠物实体也算数(它的 npc_base 在实测数据里带 npc_cfg_id,
	// 但判据以 home_pet 为准更贴切:这条路径要的是「这是谁、住哪个窝」)。
	return a, a.CfgID != 0 || a.HomePet != nil
}

// ParseActorLeave 从 0x0414/0x0413 取离开 AOI 的实体 id:
// acts(1) → actor_leave(2,SpaceAct_ActorLeave) → actor_ids(1,重复 uint64)。
//
// 「离开」既可能是走远出了 AOI,也可能是**星星被玩家收走**。两者只能靠距离区分:玩家不可能
// 隔着几十米收集,故只在玩家就在旁边时才据此判已收集(见 pipeline 的 starCollectRadius)。
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
					out = append(out, wire.PackedVarints(packed)...)
				}
			})
		})
	})
	return out
}

// ZoneProgress 是某区域某类星星的收集进度(服务器口径,进场景包下发)。
type ZoneProgress struct {
	Camp  int32 // 区域键 = 该区域营地(魔力之源)的刷新点 id;names.json 的 zones 给中文名
	NpcID int32 // 星星 NPC id(独立星/光点/石像各自一条,id 清单见 starNpc)
	Got   int32 // 已收集
	Total int32 // 总数(服务器口径,少数点不计区域,见 docs/map.md 4)
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

// ParsePendantInteract 从 c2s ZoneSceneNpcPendantInteractReq(0x0272)取被触碰的挂件:
//
//	{npc_id(1,石像实体 actor_id), pendant_cfg_id(2,恰为石像刷新行 id = POI.R), id(3,挂件序号)}
//
// 玩家触碰石像上浮现的星时客户端发此包(pcap 实测:随后 s2c 0x0273 ret=0 + 0x0243 奖励)。
// c2s AppBody 有 6 字节子头 + 尾部变长 trailer(同 ParseMoveReq),故扫描候选起点、贪婪解析,
// 取「消费最多且解出 pendant_cfg_id」者;错位起点会在字段号/wire type 上撞停。
func ParsePendantInteract(appBody []byte) (refreshID int32, ok bool) {
	bestConsumed := -1
	for start := 0; start <= len(appBody) && start <= 16; start++ {
		rid, consumed, got := decodePendantReq(appBody[start:])
		if got && consumed > bestConsumed {
			refreshID, bestConsumed = rid, consumed
		}
	}
	return refreshID, bestConsumed >= 0
}

// decodePendantReq 贪婪解析 ZoneSceneNpcPendantInteractReq(字段 1-3 全 varint),
// 遇到不属于该消息的字段号或 wire type 即停(到达 trailer)。
func decodePendantReq(b []byte) (refreshID int32, consumed int, ok bool) {
	rest := b
	for len(rest) > 0 {
		num, typ, n := protowire.ConsumeTag(rest)
		if n < 0 || num < 1 || num > 3 || typ != protowire.VarintType {
			break
		}
		v, m := protowire.ConsumeVarint(rest[n:])
		if m < 0 {
			break
		}
		if num == 2 {
			refreshID, ok = int32(v), true
		}
		rest = rest[n+m:]
		consumed += n + m
	}
	return refreshID, consumed, ok
}

// ParsePendantInteractRsp 从 s2c ZoneSceneNpcPendantInteractRsp(0x0273)取结果:
// ret_info(1) → ret(1),0 = 收集成功。s2c 无子头,直接从头解析。
func ParsePendantInteractRsp(body []byte) (retOK bool) {
	ri := subMsg(body, 1)
	if ri == nil {
		return false
	}
	retOK = true // ret 为 0 时字段可能省略,取到 ret_info 即默认成功
	scanFields(ri, func(num protowire.Number, typ protowire.Type, _ []byte, v uint64) {
		if num == 1 && typ == protowire.VarintType && v != 0 {
			retOK = false
		}
	})
	return retOK
}
