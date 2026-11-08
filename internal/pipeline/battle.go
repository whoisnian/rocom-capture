package pipeline

import (
	"google.golang.org/protobuf/proto"

	"github.com/whoisnian/rocom-capture/internal/pb"
	"github.com/whoisnian/rocom-capture/internal/pet"
	"github.com/whoisnian/rocom-capture/internal/scene"
	"github.com/whoisnian/rocom-parse/capture"
	"github.com/whoisnian/rocom-parse/gcp"
)

// 捕获事件页的「战斗信息」行:跟踪当前连接正在进行的战斗,数据只在前端 localStorage,
// 后端不落库、不持久化。三个阶段:
//
//	enter  0x1316:进战斗,普通对战直接展示敌方第一只宠物;若敌方是赛季奇遇的龙棋士
//	               (1646/9006),用 battle_cfg_id 查 BATTLE_CONF 得到真正的宠物种类,
//	               等级/性别/身高/体重/声音/性格取自龙棋士的 battle_common_pet_info,
//	               这几项与最终捕捉一致(见 docs/parsing.md),拼一只合成 Pet 推给前端。
//	               异色按敌方护盾预判:最大护盾 = 最大生命 × 3 即异色(× 2 普通),
//	               进战斗就展示异色头像与标记;普通对战的性格不能提前确定,清掉由前端
//	               显示未知;处于污染状态的血脉也是临时的(捕捉后变回种族血脉),同样不展示。
//	reveal 0x131a:龙棋士的护盾打破,敌方 401 号位从龙棋士变成真宠物;推完整 Pet(种类/
//	               性格/血脉/异色/污染/奇异),这些字段也已经与最终捕捉一致。普通对战若是
//	               污染个体,污染位清零时也推一次(阶段变已打破),但血脉仍不展示。
//	end    0x132c:战斗结束,只推 stage=end,前端置灰并保留上一份数据。
type battleState struct {
	chess    bool // 赛季奇遇龙棋士战斗(才有 battle_cfg)
	polluted bool // 普通对战且进战斗时处于污染状态:等 0x131a 的污染位清零
	revealed bool
}

const (
	chessNpcConf = 1646 // MONSTER_CONF/PET_CONF:龙棋士
	chessPetBase = 9006 // PETBASE_CONF:龙棋士
)

// handleBattleInfo 处理 0x1316/0x131a/0x132c,对每场战斗广播 "battle";PVP 整场不推。
func (p *Pipeline) handleBattleInfo(m capture.Message, acc string) {
	if m.Direction != gcp.S2C {
		return
	}
	cs := p.conn(m.Session)
	switch m.Opcode {
	case scene.OpBattleEnterNotify:
		be, ok := scene.ParseBattleEnter(m.AppBody)
		if !ok || scene.IsPVP(be.BattleMode) {
			cs.battle = nil
			return
		}
		// 赛季奇遇的龙棋士:种类查 battle_cfg,性格/等级/性别/身高/体重/声音取它身上
		// (这些与最终捕捉一致)。普通对战直接用敌方宠物,但性格不可提前确定,清掉由前端显示未知。
		chess := be.Enemy.GetConfId() == chessNpcConf && be.Enemy.GetBaseConfId() == chessPetBase
		cfg, hasCfg := p.db.BattleCfg(be.BattleCfgID)
		chess = chess && hasCfg && cfg.PetConf != 0
		var pp *pet.Pet
		var polluted bool
		if chess {
			pp = p.battleEnterPet(be.Enemy, cfg.PetConf, scene.BattleShieldShiny(be.EnemyBattleAttr))
		} else {
			pd, _ := proto.Clone(be.Enemy).(*pb.PetData)
			if pd == nil {
				pd = &pb.PetData{}
			}
			polluted = scene.IsPollutedMutation(pd.GetMutationType())
			pp = p.normalBattlePet(pd, polluted)
		}
		pet.FillDerived(p.db, pp)
		cs.battle = &battleState{chess: chess, polluted: polluted}
		p.srv.Hub().Broadcast("battle", acc, map[string]any{
			"stage": "enter", "revealed": false,
			"time": m.Time.Unix(), "species": pp.Species, "pet": pp,
		})
	case scene.OpBattleRoundStartNotify:
		// reveal 只在两种情况下需要:龙棋士护盾破后宠物现身;普通污染对战把污染打掉。
		// 之后的回合都会带同一只宠物,只推第一次,避免重复刷新前端。
		if cs.battle == nil || cs.battle.revealed {
			return
		}
		var pp *pet.Pet
		switch {
		case cs.battle.chess:
			pd := scene.ParseBattleRoundReveal(m.AppBody)
			if pd == nil {
				return
			}
			pp = pet.ToPet(pd, p.db)
		case cs.battle.polluted:
			pd := scene.ParseBattleRoundReveal(m.AppBody)
			if pd == nil || scene.IsPollutedMutation(pd.GetMutationType()) {
				return // 污染还没打破,等下一回合
			}
			// 破污染后血脉字段仍是污染血脉(实测 23),捕捉时才知道种族血脉(实测 9),先不展示。
			pp = p.normalBattlePet(pd, true)
		default:
			return
		}
		cs.battle.revealed = true
		pet.FillDerived(p.db, pp)
		p.srv.Hub().Broadcast("battle", acc, map[string]any{
			"stage": "reveal", "revealed": true,
			"time": m.Time.Unix(), "species": pp.Species, "pet": pp,
		})
	case scene.OpBattleFinishNotify:
		if cs.battle == nil {
			return
		}
		// 兜底:龙棋士正常已从 0x131a 推过 reveal,只有没等到时才用结算里的 PetData 补一份。
		// 结算的 caught PetData 可能带捕捉球附加的炫彩(棱镜球),故仅作兜底。
		// 普通对战不用兜底:enter 已有敌方宠物,结算里的性格/炫彩是捕捉时才确定的。
		if cs.battle.chess && !cs.battle.revealed {
			if pd := scene.ParseBattleCaught(m.AppBody); pd != nil && pd.GetBaseConfId() != chessPetBase {
				pp := pet.ToPet(pd, p.db)
				pet.FillDerived(p.db, pp)
				p.srv.Hub().Broadcast("battle", acc, map[string]any{
					"stage": "reveal", "revealed": true,
					"time": m.Time.Unix(), "species": pp.Species, "pet": pp,
				})
			}
		}
		p.srv.Hub().Broadcast("battle", acc, map[string]any{"stage": "end", "time": m.Time.Unix()})
		cs.battle = nil
	}
}

// normalBattlePet 把普通对战的敌方 PetData 转成展示用 Pet:性格不能提前确定,一律清空;
// hideBlood 时连血脉也清空——处于污染状态(MDT_CHAOS)的敌方,血脉是临时的污染血脉,
// 捕捉后会变回种族血脉(实测污染小电企鹅:23→电 9),不能提前展示。
func (p *Pipeline) normalBattlePet(pd *pb.PetData, hideBlood bool) *pet.Pet {
	pd.Nature = nil
	if hideBlood {
		pd.BloodId = nil
	}
	return pet.ToPet(pd, p.db)
}

// battleEnterPet 用 battle_cfg 的真宠物 conf/base 拼一只合成 Pet:
// 种类/头像/属性取真宠物,等级/性别/身高/体重/声音/性格取龙棋士(逐字段与最终捕捉一致),
// 异色由护盾倍率预判(shiny),用于异色头像与标记;血脉/炫彩/污染等其余字段清掉——
// 它们在 enter 阶段还不是最终宠物的值。
func (p *Pipeline) battleEnterPet(enemy *pb.PetData, petConf uint32, shiny bool) *pet.Pet {
	pd, ok := proto.Clone(enemy).(*pb.PetData)
	if !ok {
		pd = &pb.PetData{}
	}
	conf := petConf
	pd.ConfId = &conf
	if base, _, ok := p.db.PetBaseOf(petConf); ok && base != 0 {
		b := base
		pd.BaseConfId = &b
	}
	pd.Name = nil // 龙棋士的名字不是最终宠物的
	// 性格保留:龙棋士战斗里的 nature 就是最终宠物的性格(实测一致);普通对战在别处清掉。
	pd.BloodId = nil
	pd.MutationType = nil
	if shiny {
		mutation := uint32(1) // MDT_SHINING
		pd.MutationType = &mutation
	}
	pd.GlassInfo = nil
	pd.TalentRank = nil
	pd.SpecialityId = nil
	pd.WearMedalConfId = nil
	return pet.ToPet(pd, p.db)
}
