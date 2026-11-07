package scene

import (
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/whoisnian/rocom-capture/internal/pb"
	"github.com/whoisnian/rocom-capture/internal/wire"
)

// 捕获事件页「战斗信息」行的消息解析:
//
//	0x1316 战斗进入:敌方队伍第一个宠物的 battle_common_pet_info 即对手宠物;若敌方是
//	        龙棋士(conf 1646/base 9006),common 里是等级/性别/身高/体重/声音/性格,
//	        种类由 init_info.battle_cfg_id 查 BATTLE_CONF 得到(names.json 的 battle_cfg),
//	        这是唯一在进战斗时就与最终宠物一致的种类来源。
//	0x131a 回合开始:龙棋士护盾打破后,敌方(401 号位)的 common 从龙棋士变成真正的宠物,
//	        带 conf/base、mutation_type(异色=1/污染=32)、blood_id(奇异=24)、性格等。
//	0x132c 战斗结束:只用于让前端置灰;龙棋士没收到 reveal 时可从这里补一次。
//
// 见 rocom-capture docs/parsing.md 与 docs/architecture.md 的 SSE 类型。
const (
	OpBattleEnterNotify      = 0x1316 // ZONE_BATTLE_ENTER_NOTIFY,s2c:战斗进入(battle_cfg_id + 敌方队伍)
	OpBattleRoundStartNotify = 0x131a // ZONE_BATTLE_ROUND_START_NOTIFY,s2c:回合开始(perform_cmd.data_update)
)

const (
	battleChessConf = 1646 // PET_MONSTER/MONSTER_CONF:龙棋士
	battleChessBase = 9006 // PETBASE_CONF:龙棋士
)

// BattleEnter 是 0x1316 的解析结果。
type BattleEnter struct {
	BattleMode  uint32      // battle_mode:战斗类型(pb.BattleType)
	BattleCfgID uint32      // init_info.battle_cfg_id[0]
	Enemy       *pb.PetData // 敌方队伍第一个宠物的 battle_common_pet_info
}

// ParseBattleEnter 解 s2c ZoneBattleEnterNotify(0x1316)的 battle_mode(1)与 init_info(6) →
// battle_cfg_id(2,重复/packed)、enemy_team(6,重复 BattleRoleInfo) →
// pets(2,重复 BattlePetInfo) → battle_common_pet_info(2, PetData)。
func ParseBattleEnter(body []byte) (*BattleEnter, bool) {
	var mode uint32
	if v, ok := wire.Varint(body, 1); ok { // battle_mode
		mode = uint32(v)
	}
	init := subMsg(body, 6)
	if init == nil {
		return nil, false
	}
	var cfg uint32
	scanFields(init, func(num protowire.Number, typ protowire.Type, val []byte, v uint64) {
		if cfg != 0 || num != 2 {
			return
		}
		switch typ {
		case protowire.VarintType:
			cfg = uint32(v)
		case protowire.BytesType: // packed repeated
			if vs := wire.PackedVarints(val); len(vs) > 0 {
				cfg = uint32(vs[0])
			}
		}
	})
	for _, team := range wire.Subs(init, 6) { // enemy_team
		for _, pet := range wire.Subs(team, 2) { // BattlePetInfo
			common := subMsg(pet, 2)
			if common == nil {
				continue
			}
			var pd pb.PetData
			if proto.Unmarshal(common, &pd) == nil && pd.GetBaseConfId() != 0 {
				return &BattleEnter{BattleMode: mode, BattleCfgID: cfg, Enemy: &pd}, true
			}
		}
	}
	return nil, false
}

// IsPVP 报告该战斗类型是否为玩家对战(口径同客户端 PetUtils.IsPvp)。
func IsPVP(mode uint32) bool {
	switch pb.BattleType(mode) {
	case pb.BattleType_BT_PVP,
		pb.BattleType_BT_PVP_SRANDARD,
		pb.BattleType_BT_PVP_RANDOM,
		pb.BattleType_BT_PVP_WATER,
		pb.BattleType_BT_PVP_INSECT,
		pb.BattleType_BT_PVP_RANK,
		pb.BattleType_BT_PVP_THREE,
		pb.BattleType_BT_PVP_SCARE,
		pb.BattleType_BT_PVP_GAME1,
		pb.BattleType_BT_PVP_GAME2,
		pb.BattleType_BT_PVP_GAME3,
		pb.BattleType_BT_PVP_GAME4:
		return true
	}
	return false
}

// ParseBattleRoundReveal 从 s2c ZoneBattleRoundStartNotify(0x131a)取敌方 401 号位已经现身的宠物:
// perform_cmd(3) → perform_info(2,重复) → data_update(44) →
//
//	pet(3)         玩家自己的宠物
//	other(8).pets(3) 对手队伍(实际抓包里 401 号位在这里)
//
// 每条 BattlePetInfo 取 battle_inside_pet_info(1,pet_id=401)/ battle_common_pet_info(2, PetData)。
// 仍为龙棋士(1646/9006)的回合返回 nil——护盾未破,宠物还没出现。
func ParseBattleRoundReveal(body []byte) *pb.PetData {
	cmd := subMsg(body, 3)
	if cmd == nil {
		return nil
	}
	for _, pi := range wire.Subs(cmd, 2) {
		du := subMsg(pi, 44)
		if du == nil {
			continue
		}
		// 玩家自己的宠物走 data_update.pet(3);对手在 data_update.other(8).pets(3)。
		var pets [][]byte
		if pet := subMsg(du, 3); pet != nil {
			pets = append(pets, pet)
		}
		for _, other := range wire.Subs(du, 8) {
			pets = append(pets, wire.Subs(other, 3)...)
		}
		for _, pet := range pets {
			inside, common := subMsg(pet, 1), subMsg(pet, 2)
			if inside == nil || common == nil {
				continue
			}
			if id, ok := wire.Varint(inside, 1); !ok || id != 401 {
				continue
			}
			var pd pb.PetData
			if proto.Unmarshal(common, &pd) != nil || pd.GetBaseConfId() == 0 {
				continue
			}
			if pd.GetConfId() == battleChessConf || pd.GetBaseConfId() == battleChessBase {
				continue // 还是龙棋士,护盾未破
			}
			return &pd
		}
	}
	return nil
}

// ParseBattleCaught 从 s2c ZoneBattleFinishNotify(0x132c)取被捕捉的怪物 PetData:
// settle_info(1) → monster_info(8,重复) → state(2)=BATTLE_MONSTER_CATCHED(1)、
// caught_monster(6, PetData)。仅用于没等到 0x131a reveal 时兜底。
func ParseBattleCaught(body []byte) *pb.PetData {
	settle := subMsg(body, 1)
	if settle == nil {
		return nil
	}
	for _, mi := range wire.Subs(settle, 8) {
		if state, _ := wire.Varint(mi, 2); state != uint64(battleMonsterCatched) {
			continue
		}
		common := subMsg(mi, 6)
		if common == nil {
			continue
		}
		var pd pb.PetData
		if proto.Unmarshal(common, &pd) == nil && pd.GetBaseConfId() != 0 {
			return &pd
		}
	}
	return nil
}
