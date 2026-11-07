package scene

import (
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

// wildNpcBase 构造一个野生宠物的 ActorInfo_NpcBase:npc_cfg_id(1)、height(11)、weight(12)、
// mutation_type(14)、glass_info(30){glass_type(1), glass_value(2)}、voice(31,int32 可为负)。
func wildNpcBase(cfgID, height, weight, voice, mutation, glassType, glassValue int32) []byte {
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(uint32(cfgID)))
	b = protowire.AppendTag(b, 11, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(uint32(height)))
	b = protowire.AppendTag(b, 12, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(uint32(weight)))
	if mutation != 0 {
		b = protowire.AppendTag(b, 14, protowire.VarintType)
		b = protowire.AppendVarint(b, uint64(mutation))
	}
	if glassType != 0 || glassValue != 0 {
		var g []byte
		g = protowire.AppendTag(g, 1, protowire.VarintType)
		g = protowire.AppendVarint(g, uint64(glassType))
		g = protowire.AppendTag(g, 2, protowire.VarintType)
		g = protowire.AppendVarint(g, uint64(glassValue))
		b = protowire.AppendTag(b, 30, protowire.BytesType)
		b = protowire.AppendBytes(b, g)
	}
	b = protowire.AppendTag(b, 31, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(uint64(int64(voice)))) // int32 负值按补码
	return b
}

// npcActorInfo 构造一条 ActorInfo:npc(11) → {base(1){actor_id(2), born_time(5,可选), lv(11), pt(8).pos(1)},
// npc_base(3), npc_bonus_info(27,可选)}。
func npcActorInfo(actorID uint64, lv int32, p Position, nb []byte) []byte {
	return npcActorInfoFull(actorID, lv, 0, nil, p, nb)
}

func npcActorInfoBorn(actorID uint64, lv int32, bornTime int64, p Position, nb []byte) []byte {
	return npcActorInfoFull(actorID, lv, bornTime, nil, p, nb)
}

func npcActorInfoFull(actorID uint64, lv int32, bornTime int64, bonus []byte, p Position, nb []byte) []byte {
	var base []byte
	base = protowire.AppendTag(base, 2, protowire.VarintType)
	base = protowire.AppendVarint(base, actorID)
	if bornTime != 0 {
		base = protowire.AppendTag(base, 5, protowire.VarintType)
		base = protowire.AppendVarint(base, uint64(bornTime))
	}
	base = protowire.AppendTag(base, 11, protowire.VarintType)
	base = protowire.AppendVarint(base, uint64(lv))
	var pt []byte
	pt = protowire.AppendTag(pt, 1, protowire.BytesType)
	pt = protowire.AppendBytes(pt, pos(p.X, p.Y, p.Z))
	base = protowire.AppendTag(base, 8, protowire.BytesType)
	base = protowire.AppendBytes(base, pt)

	var npc []byte
	npc = protowire.AppendTag(npc, 1, protowire.BytesType)
	npc = protowire.AppendBytes(npc, base)
	npc = protowire.AppendTag(npc, 3, protowire.BytesType)
	npc = protowire.AppendBytes(npc, nb)
	if bonus != nil {
		npc = protowire.AppendTag(npc, 27, protowire.BytesType)
		npc = protowire.AppendBytes(npc, bonus)
	}

	var actor []byte
	actor = protowire.AppendTag(actor, 11, protowire.BytesType)
	actor = protowire.AppendBytes(actor, npc)
	return actor
}

// TestParseSceneActorsWildPet 用 2026-08-02 pcap 里那只被捕捉的珀尔鼬的真实数值,
// 核对野生宠物个体属性(捕捉后原样进 PetData,见 docs/map.md 5)。
func TestParseSceneActorsWildPet(t *testing.T) {
	var body []byte
	body = protowire.AppendTag(body, 7, protowire.BytesType) // other_actors
	body = protowire.AppendBytes(body, npcActorInfo(9279722995167884497, 38,
		Position{X: 561976, Y: 508635, Z: 2648},
		wildNpcBase(10782, 74, 11048, -75, 0, 0, 0)))

	actors := ParseSceneActors(body)
	if len(actors) != 1 {
		t.Fatalf("实体数 = %d, 期望 1", len(actors))
	}
	a := actors[0]
	if a.ActorID != 9279722995167884497 || a.CfgID != 10782 || a.Lv != 38 {
		t.Errorf("身份 = %+v", a)
	}
	if a.Height != 74 || a.Weight != 11048 {
		t.Errorf("身高体重 = %d/%d, 期望 74/11048", a.Height, a.Weight)
	}
	if a.Voice != -75 { // int32 负值(varint 补码 10 字节)
		t.Errorf("Voice = %d, 期望 -75", a.Voice)
	}
	if a.Mutation != 0 || a.GlassType != 0 || a.GlassValue != 0 {
		t.Errorf("无变异个体不该有 mutation/glass: %d %d/%d", a.Mutation, a.GlassType, a.GlassValue)
	}
	if a.IsShiny() || a.IsPolluted() {
		t.Error("mutation=0 不该判为异色/污染")
	}
	if !a.IsWildPet() {
		t.Error("IsWildPet 应为 true(带身高体重)")
	}
	if a.Pos != (Position{X: 561976, Y: 508635, Z: 2648}) {
		t.Errorf("Pos = %+v", a.Pos)
	}
}

// TestParseActorBornTime 大龙棋士(NPC_CONF 29401)靠 base.born_time 算 4 小时存活期,
// 其余字段与野生宠无关(见 pipeline/wildpets.go 的 chessWorldNpc)。
func TestParseActorBornTime(t *testing.T) {
	var bonus []byte // npc_bonus_info:chess_emotion_id(2), chess_drop_dice(3)
	bonus = protowire.AppendTag(bonus, 2, protowire.VarintType)
	bonus = protowire.AppendVarint(bonus, 105)
	bonus = protowire.AppendTag(bonus, 3, protowire.VarintType)
	bonus = protowire.AppendVarint(bonus, 1)

	var body []byte
	body = protowire.AppendTag(body, 7, protowire.BytesType)
	body = protowire.AppendBytes(body, npcActorInfoFull(9279722995167919815, 41, 1794054516, bonus,
		Position{X: 417855, Y: 692317, Z: 56}, wildNpcBase(29401, 0, 0, 0, 0, 0, 0)))

	actors := ParseSceneActors(body)
	if len(actors) != 1 {
		t.Fatalf("实体数 = %d, 期望 1", len(actors))
	}
	a := actors[0]
	if a.CfgID != 29401 || a.BornTime != 1794054516 {
		t.Errorf("cfg/born_time = %d/%d", a.CfgID, a.BornTime)
	}
	if a.ChessEmotionID != 105 || !a.ChessDropDice {
		t.Errorf("棋子 bonus = %d/%v, 期望 105/true", a.ChessEmotionID, a.ChessDropDice)
	}
	if a.IsWildPet() {
		t.Error("大龙棋士没有身高体重,不该判为野生宠")
	}
}

// TestParseActorEnterGlass:AOI 补发的「异色 + 隐藏炫彩」个体。
// mutation=9 = MDT_SHINING|MDT_GLASS,与 glass_info 非空一致(实测口径,见 docs/map.md 5)。
func TestParseActorEnterGlass(t *testing.T) {
	actor := npcActorInfo(123456, 13, Position{X: 1, Y: 2, Z: 3},
		wildNpcBase(10254, 32, 3360, 100, 9, 2, 3)) // 隐藏炫彩「铅字幻梦」
	var enter []byte
	enter = protowire.AppendTag(enter, 1, protowire.BytesType) // actors
	enter = protowire.AppendBytes(enter, actor)
	var acts []byte
	acts = protowire.AppendTag(acts, 1, protowire.BytesType) // actor_enter
	acts = protowire.AppendBytes(acts, enter)
	var body []byte
	body = protowire.AppendTag(body, 1, protowire.BytesType) // acts
	body = protowire.AppendBytes(body, acts)

	actors := ParseActorEnter(body)
	if len(actors) != 1 {
		t.Fatalf("实体数 = %d, 期望 1", len(actors))
	}
	a := actors[0]
	if a.GlassType != 2 || a.GlassValue != 3 {
		t.Errorf("glass = %d/%d, 期望 2/3", a.GlassType, a.GlassValue)
	}
	if !a.IsShiny() || a.IsPolluted() {
		t.Errorf("mutation=%d 应判为异色、非污染", a.Mutation)
	}
	if a.Mutation&MutationGlass == 0 {
		t.Error("MDT_GLASS 位应与 glass_info 非空一致")
	}
	if a.Voice != 100 || !a.IsWildPet() {
		t.Errorf("Voice = %d, IsWildPet = %v", a.Voice, a.IsWildPet())
	}
}

// TestIsWildPetStatic:静态 NPC(无身高体重)不该被当成野生宠物。
func TestIsWildPetStatic(t *testing.T) {
	var nb []byte
	nb = protowire.AppendTag(nb, 1, protowire.VarintType)
	nb = protowire.AppendVarint(nb, 55162) // 眠枭之星
	nb = protowire.AppendTag(nb, 10, protowire.VarintType)
	nb = protowire.AppendVarint(nb, 100200) // npc_content_cfg_id
	var body []byte
	body = protowire.AppendTag(body, 7, protowire.BytesType)
	body = protowire.AppendBytes(body, npcActorInfo(777, 1, Position{}, nb))

	actors := ParseSceneActors(body)
	if len(actors) != 1 {
		t.Fatalf("实体数 = %d", len(actors))
	}
	if actors[0].IsWildPet() {
		t.Error("星星实体不该 IsWildPet")
	}
	if !actors[0].IsStar() || actors[0].RefreshID != 100200 {
		t.Errorf("星星判定失效: %+v", actors[0])
	}
}

// TestParseSceneActorsPolluted 用 2026-08-02 那只污染爬爬的真实数值:
// 污染实测就是 mutation_type=MDT_CHAOS_TWO(4),既不是异色也不是炫彩(见 docs/map.md 5)。
func TestParseSceneActorsPolluted(t *testing.T) {
	var body []byte
	body = protowire.AppendTag(body, 7, protowire.BytesType)
	body = protowire.AppendBytes(body, npcActorInfo(9279722995167795223, 44,
		Position{X: 484917, Y: 634942, Z: 3520},
		wildNpcBase(10160, 38, 6054, -55, MutationChaosTwo, 0, 0)))

	actors := ParseSceneActors(body)
	if len(actors) != 1 {
		t.Fatalf("实体数 = %d, 期望 1", len(actors))
	}
	a := actors[0]
	if !a.IsWildPet() || !a.IsPolluted() {
		t.Errorf("应判为被污染的野生宠: IsWildPet=%v IsPolluted=%v", a.IsWildPet(), a.IsPolluted())
	}
	if a.IsShiny() || a.GlassType != 0 {
		t.Error("污染个体不该被当成异色/炫彩")
	}
	if a.Lv != 44 || a.Height != 38 || a.Weight != 6054 || a.Voice != -55 {
		t.Errorf("个体属性 = lv%d h%d w%d v%d(这几项捕捉后原样保留)", a.Lv, a.Height, a.Weight, a.Voice)
	}
}
