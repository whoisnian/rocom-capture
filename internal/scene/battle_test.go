package scene

import (
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

// petData 构造 PetData 的测试子集:conf_id(2)/nature(7)/gender(8)/level(10)/
// base_conf_id(15)/height(26)/weight(27)/mutation_type(45)/blood_id(47)/voice(93)。
func petData(conf, base, lv, nature, gender, height, weight, mutation, blood uint32, voice int32) []byte {
	var b []byte
	add := func(num protowire.Number, v uint64) {
		b = protowire.AppendTag(b, num, protowire.VarintType)
		b = protowire.AppendVarint(b, v)
	}
	add(2, uint64(conf))
	add(7, uint64(nature))
	add(8, uint64(gender))
	add(10, uint64(lv))
	add(15, uint64(base))
	add(26, uint64(height))
	add(27, uint64(weight))
	add(45, uint64(mutation))
	add(47, uint64(blood))
	add(93, uint64(int64(voice)))
	return b
}

// battleInside 构造 battle_inside_pet_info 的 battle_attr(6,重复 int32):
// 索引 1 为最大生命,末项为最大护盾,其余值此处不关心。
func battleInside(attrs ...uint64) []byte {
	var b []byte
	for _, v := range attrs {
		b = protowire.AppendTag(b, 6, protowire.VarintType)
		b = protowire.AppendVarint(b, v)
	}
	return b
}

// BattleInitInfo: battle_mode(1) + battle_cfg_id(2) + enemy_team(6) → pets(2) →
// battle_inside_pet_info(1) + battle_common_pet_info(2)。
func TestParseBattleEnter(t *testing.T) {
	var pet []byte
	pet = protowire.AppendTag(pet, 1, protowire.BytesType)
	pet = protowire.AppendBytes(pet, battleInside(0, 258, 0, 774, 774)) // 异色:护盾 3×生命
	pet = protowire.AppendTag(pet, 2, protowire.BytesType)
	pet = protowire.AppendBytes(pet, petData(1646, 9006, 42, 16, 1, 210, 653123, 0, 4, 45))

	var team []byte
	team = protowire.AppendTag(team, 2, protowire.BytesType)
	team = protowire.AppendBytes(team, pet)

	var init []byte
	init = protowire.AppendTag(init, 2, protowire.VarintType)
	init = protowire.AppendVarint(init, 250355)
	init = protowire.AppendTag(init, 6, protowire.BytesType)
	init = protowire.AppendBytes(init, team)

	var body []byte
	body = protowire.AppendTag(body, 1, protowire.VarintType) // battle_mode = BT_PVE
	body = protowire.AppendVarint(body, 1)
	body = protowire.AppendTag(body, 6, protowire.BytesType)
	body = protowire.AppendBytes(body, init)

	be, ok := ParseBattleEnter(body)
	if !ok {
		t.Fatal("未解出战斗进入信息")
	}
	if be.BattleCfgID != 250355 {
		t.Errorf("battle_cfg_id = %d, 期望 250355", be.BattleCfgID)
	}
	if be.BattleMode != 1 || IsPVP(be.BattleMode) {
		t.Errorf("battle_mode = %d, 期望 PVE(1)", be.BattleMode)
	}
	if e := be.Enemy; e.GetConfId() != 1646 || e.GetBaseConfId() != 9006 ||
		e.GetLevel() != 42 || e.GetGender() != 1 || e.GetHeight() != 210 ||
		e.GetWeight() != 653123 || e.GetVoice() != 45 {
		t.Errorf("敌方 PetData 不符: %+v", e)
	}
	if len(be.EnemyBattleAttr) != 5 || be.EnemyBattleAttr[1] != 258 || be.EnemyBattleAttr[4] != 774 {
		t.Errorf("敌方 battle_attr 不符: %v", be.EnemyBattleAttr)
	}
	if !BattleShieldShiny(be.EnemyBattleAttr) {
		t.Error("护盾 774 = 3×258,应判为异色")
	}
}

// 异色判据:最大护盾 = 最大生命 × 3;× 2 普通,护盾缺失(0)不适用。
func TestBattleShieldShiny(t *testing.T) {
	cases := []struct {
		name  string
		attrs []int32
		want  bool
	}{
		{"异色", []int32{0, 258, 0, 0, 774, 774}, true},
		{"普通", []int32{0, 258, 0, 0, 516, 516}, false},
		{"非龙棋士护盾为 0", []int32{0, 188, 0, 0, 0}, false},
		{"无 battle_attr", nil, false},
		{"只有生命", []int32{0, 258}, false},
	}
	for _, c := range cases {
		if got := BattleShieldShiny(c.attrs); got != c.want {
			t.Errorf("%s: BattleShieldShiny(%v) = %v, 期望 %v", c.name, c.attrs, got, c.want)
		}
	}
}

// battle_attr 也可能按 packed 下发,两种编码都要能解。
func TestBattleAttrsPacked(t *testing.T) {
	var packed []byte
	for _, v := range []uint64{0, 258, 0, 774, 774} {
		packed = protowire.AppendVarint(packed, v)
	}
	inside := protowire.AppendTag(nil, 6, protowire.BytesType)
	inside = protowire.AppendBytes(inside, packed)
	attrs := battleAttrs(inside)
	if len(attrs) != 5 || !BattleShieldShiny(attrs) {
		t.Errorf("packed battle_attr 解出 %v,期望 5 项且判为异色", attrs)
	}
}

// TestIsPVP 覆盖 PVP 类型与常见非 PVP 类型。
func TestIsPVP(t *testing.T) {
	for _, mode := range []uint32{2, 15, 16, 17, 18, 22, 25, 35, 39, 40, 44, 45} {
		if !IsPVP(mode) {
			t.Errorf("battle_mode=%d 应判为 PVP", mode)
		}
	}
	for _, mode := range []uint32{0, 1, 9, 11, 12, 13, 36} {
		if IsPVP(mode) {
			t.Errorf("battle_mode=%d 不应判为 PVP", mode)
		}
	}
}

// ZoneBattleRoundStartNotify: perform_cmd(3) → perform_info(2) → data_update(44) → pet(3) →
// battle_inside_pet_info(1, pet_id=401) + battle_common_pet_info(2, PetData)。
func roundStart(petID uint64, common []byte) []byte {
	var inside []byte
	inside = protowire.AppendTag(inside, 1, protowire.VarintType)
	inside = protowire.AppendVarint(inside, petID)

	var pet []byte
	pet = protowire.AppendTag(pet, 1, protowire.BytesType)
	pet = protowire.AppendBytes(pet, inside)
	pet = protowire.AppendTag(pet, 2, protowire.BytesType)
	pet = protowire.AppendBytes(pet, common)

	var du []byte
	du = protowire.AppendTag(du, 3, protowire.BytesType)
	du = protowire.AppendBytes(du, pet)

	var pi []byte
	pi = protowire.AppendTag(pi, 44, protowire.BytesType)
	pi = protowire.AppendBytes(pi, du)

	var cmd []byte
	cmd = protowire.AppendTag(cmd, 2, protowire.BytesType)
	cmd = protowire.AppendBytes(cmd, pi)

	var body []byte
	body = protowire.AppendTag(body, 3, protowire.BytesType)
	body = protowire.AppendBytes(body, cmd)
	return body
}

// 对手在 data_update.other(8)→pets(3);玩家的在 data_update.pet(3),两条都要能识别。
func roundStartOther(petID uint64, common []byte) []byte {
	var inside []byte
	inside = protowire.AppendTag(inside, 1, protowire.VarintType)
	inside = protowire.AppendVarint(inside, petID)

	var pet []byte
	pet = protowire.AppendTag(pet, 1, protowire.BytesType)
	pet = protowire.AppendBytes(pet, inside)
	pet = protowire.AppendTag(pet, 2, protowire.BytesType)
	pet = protowire.AppendBytes(pet, common)

	var other []byte
	other = protowire.AppendTag(other, 3, protowire.BytesType)
	other = protowire.AppendBytes(other, pet)

	var du []byte
	du = protowire.AppendTag(du, 8, protowire.BytesType)
	du = protowire.AppendBytes(du, other)

	var pi []byte
	pi = protowire.AppendTag(pi, 44, protowire.BytesType)
	pi = protowire.AppendBytes(pi, du)

	var cmd []byte
	cmd = protowire.AppendTag(cmd, 2, protowire.BytesType)
	cmd = protowire.AppendBytes(cmd, pi)

	var body []byte
	body = protowire.AppendTag(body, 3, protowire.BytesType)
	body = protowire.AppendBytes(body, cmd)
	return body
}

func TestParseBattleRoundReveal(t *testing.T) {
	// 护盾破后:401 号位变成异色深蓝鲸(mutation=1)。真实抓包里对手在 other.pets 下。
	body := roundStartOther(401, petData(300355, 3381, 42, 16, 1, 210, 653123, 1, 4, 45))
	pd := ParseBattleRoundReveal(body)
	if pd == nil {
		t.Fatal("未解出已现身的宠物")
	}
	if pd.GetConfId() != 300355 || pd.GetBaseConfId() != 3381 || pd.GetMutationType() != 1 {
		t.Errorf("宠物字段不符: conf=%d base=%d mutation=%d", pd.GetConfId(), pd.GetBaseConfId(), pd.GetMutationType())
	}
	// 玩家自己的宠物在 data_update.pet 下,同样能识别。
	if pd := ParseBattleRoundReveal(roundStart(401, petData(300355, 3381, 42, 16, 1, 210, 653123, 1, 4, 45))); pd == nil {
		t.Error("data_update.pet 路径未解出")
	}
	// 还是龙棋士的回合不算 reveal。
	if pd := ParseBattleRoundReveal(roundStartOther(401, petData(1646, 9006, 42, 16, 1, 210, 653123, 0, 4, 45))); pd != nil {
		t.Error("龙棋士本体不应被当成已现身的宠物")
	}
	// 玩家自己的宠物(pet_id 1 等)不算。
	if pd := ParseBattleRoundReveal(roundStart(1, petData(300355, 3381, 42, 16, 1, 210, 653123, 1, 4, 45))); pd != nil {
		t.Error("玩家宠物不应被当成对手")
	}
}

// ZoneBattleFinishNotify: settle_info(1) → monster_info(8) → state(2) + caught_monster(6, PetData)。
func TestParseBattleCaught(t *testing.T) {
	build := func(state uint64) []byte {
		var mi []byte
		mi = protowire.AppendTag(mi, 2, protowire.VarintType)
		mi = protowire.AppendVarint(mi, state)
		mi = protowire.AppendTag(mi, 6, protowire.BytesType)
		mi = protowire.AppendBytes(mi, petData(300355, 3381, 42, 16, 1, 210, 653123, 0, 4, 45))

		var settle []byte
		settle = protowire.AppendTag(settle, 8, protowire.BytesType)
		settle = protowire.AppendBytes(settle, mi)

		var body []byte
		body = protowire.AppendTag(body, 1, protowire.BytesType)
		body = protowire.AppendBytes(body, settle)
		return body
	}
	if pd := ParseBattleCaught(build(1)); pd == nil || pd.GetBaseConfId() != 3381 {
		t.Error("CATCHED 状态的怪物未解出")
	}
	if pd := ParseBattleCaught(build(3)); pd != nil {
		t.Error("ALIVE 状态不应解出")
	}
}
