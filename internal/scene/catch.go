package scene

import (
	"google.golang.org/protobuf/encoding/protowire"
)

// 「这只野生实体已经没了」的判定(供实时地图的野生宠物图层当场撤掉标记,见 docs/map.md 5)。
//
// 两条路各有各的结果通知,都直接带**实体 actor_id**,不必靠位置猜:
//
//	战斗外直接丢球:0x0414/0x0413 的 acts.throw_catch_notify(173,SpaceAct_DeleteThrowNotify)
//	要打一场的    :结果在 0x132c 战斗结算的 monster_info 里(捉走或打死);污染个体丢球即开战,
//	                大龙棋士则是捉走它身后的宠物后棋子被消耗
//
// 挑 throw_catch_notify 而不挑 0x0203 END_THROW_RSP 的 `catch_results`:后者是较新的字段,
// 16 份 pcap 里只有最近两份填了,而前者从最早的包一路都在,且 `is_catch_success` 是显式布尔
// (`catch_results` 里的 CRT_CATCH_SUCCESS 恰好是枚举 0,成功时反而不上线,判起来更绕)。
const OpBattleFinishNotify = 0x132c // ZONE_BATTLE_FINISH_NOTIFY,s2c:战斗结算(野怪被捉走/打死)

// BATTLE_MONSTER_RESULT_TYPE 里表示「这只野怪已从世界上消失」的两个值。
// 另外两个(RUNAWAY=2 逃跑、ALIVE=3 还在)不算消失:打输/自己跑了都属此列,
// 标记照旧按「离开 AOI」置灰,走回去它还会重新进 AOI 把标记救活。
const (
	battleMonsterDefeated = 0 // BATTLE_MONSTER_DEFEATED,被打死
	battleMonsterCatched  = 1 // BATTLE_MONSTER_CATCHED,被捉走
)

// ParseCaughtByThrow 从 s2c ZoneScenePlayActsNotify/BatchNotify(0x0414/0x0413)取**被丢球捉走**
// 的实体 id:acts(1) → throw_catch_notify(173,SpaceAct_DeleteThrowNotify) =
// {npc_id(3), is_catch_success(4), npc_catch_infos(11,重复 DeleteThrowNpcCatchInfo{npc_id(1),
// is_catch_success(2)})}。顶层那对与 npc_catch_infos[0] 实测始终一致,两处都收、按 id 去重。
//
// 该 act 对**每次投掷物销毁**都下发(扔道具/魔法也发,那时不带 npc_id),且捕捉失败会带
// is_catch_success=false —— 故必须认准「成功」这一位,不能见到 act 就当捉到了。
func ParseCaughtByThrow(body []byte) []uint64 {
	seen := map[uint64]bool{}
	var out []uint64
	add := func(id uint64, ok bool) {
		if id == 0 || !ok || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	scanFields(body, func(num protowire.Number, typ protowire.Type, acts []byte, _ uint64) {
		if num != 1 || typ != protowire.BytesType { // acts
			return
		}
		scanFields(acts, func(n2 protowire.Number, t2 protowire.Type, nty []byte, _ uint64) {
			if n2 != 173 || t2 != protowire.BytesType { // throw_catch_notify
				return
			}
			var topID uint64
			var topOK bool
			scanFields(nty, func(n3 protowire.Number, t3 protowire.Type, info []byte, v uint64) {
				switch {
				case n3 == 3 && t3 == protowire.VarintType: // npc_id
					topID = v
				case n3 == 4 && t3 == protowire.VarintType: // is_catch_success
					topOK = v != 0
				case n3 == 11 && t3 == protowire.BytesType: // npc_catch_infos
					var id uint64
					var ok bool
					scanFields(info, func(n4 protowire.Number, t4 protowire.Type, _ []byte, v4 uint64) {
						switch {
						case n4 == 1 && t4 == protowire.VarintType:
							id = v4
						case n4 == 2 && t4 == protowire.VarintType:
							ok = v4 != 0
						}
					})
					add(id, ok)
				}
			})
			add(topID, topOK)
		})
	})
	return out
}

// ParseBattleGoneNpcs 从 s2c ZoneBattleFinishNotify(0x132c)取**战斗后已从世界上消失**的
// 野生实体 id(被捉走或被打死):settle_info(1) → monster_info(8,重复 BattleMonsterInfo)
// = {state(2), npc_obj_id(16)}。
//
// 被污染的野生宠丢球不会直接捉住而是开战(见 docs/map.md 5),故它的标记只能等这里撤;
// 大龙棋士的结算里 npc_obj_id 也是它自己(宠物藏在棋子身后),捉走/打死同样表示这只棋子
// 已被这场战斗消耗,服务器随后会补 actor_leave。
// 捉走(TRUE_BATTLE_RESULT_WIN_CATCH)与打死(..._WIN_DEFEAT)对地图标记是一回事——那儿
// 已经没这只了。结算里同时含我方队伍的宠物(side=0),它们的 npc_obj_id 为 0,自然被过滤掉。
//
// state 只在**字段确实下发**时才采信:DEFEATED 恰好是枚举 0,而游戏描述符是 proto2
// (显式赋值即上线,实测打死那只的 field2 确实带着 0),故不把「字段缺省」当作打死——
// 万一哪天真缺省了,顶多是标记继续置灰,不会误撤还在的实体。
func ParseBattleGoneNpcs(body []byte) []uint64 {
	var out []uint64
	scanFields(subMsg(body, 1), func(num protowire.Number, typ protowire.Type, mon []byte, _ uint64) {
		if num != 8 || typ != protowire.BytesType { // monster_info
			return
		}
		var id uint64
		var gone bool
		scanFields(mon, func(n protowire.Number, t protowire.Type, _ []byte, v uint64) {
			switch {
			case n == 2 && t == protowire.VarintType: // state
				gone = v == battleMonsterCatched || v == battleMonsterDefeated
			case n == 16 && t == protowire.VarintType: // npc_obj_id
				id = v
			}
		})
		if gone && id != 0 {
			out = append(out, id)
		}
	})
	return out
}
