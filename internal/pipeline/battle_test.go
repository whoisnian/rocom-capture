package pipeline

import (
	"testing"

	"github.com/whoisnian/rocom-capture/internal/gamedata"
	"github.com/whoisnian/rocom-capture/internal/pb"
)

// battleEnterPet 用 battle_cfg 的真宠物 conf/base 拼合成 Pet:
// 种类/属性取真宠物,等级/性别/身高/体重/声音/性格取龙棋士;血脉/变异必须清掉,
// 否则前端在 enter 阶段会显示与最终捕捉不一致的值。
func TestBattleEnterPet(t *testing.T) {
	db, err := gamedata.Load()
	if err != nil {
		t.Skipf("名称库未生成(先跑 make gamedata): %v", err)
	}
	conf, base := uint32(1646), uint32(9006)
	lv, gender, height, weight := uint32(42), uint32(1), uint32(210), uint32(653123)
	voice := int32(45)
	nature, blood, mutation := uint32(16), uint32(4), uint32(0)
	enemy := &pb.PetData{
		ConfId: &conf, BaseConfId: &base, Level: &lv, Gender: &gender,
		Height: &height, Weight: &weight, Voice: &voice,
		Nature: &nature, BloodId: &blood, MutationType: &mutation,
	}
	pp := (&Pipeline{db: db}).battleEnterPet(enemy, 300355)
	if pp.Species != "深蓝鲸" || pp.BaseConfID != 3381 {
		t.Errorf("种类 = %q/%d, 期望 深蓝鲸/3381", pp.Species, pp.BaseConfID)
	}
	if pp.Level != 42 || pp.Gender != "♂" || pp.HeightM != 2.1 || pp.WeightKg != 653.123 || pp.Voice != 45 {
		t.Errorf("个体字段不符: %+v", pp)
	}
	if pp.Nature != "警惕" {
		t.Errorf("性格 = %q, 期望 警惕(龙棋士身上就是最终宠物的性格)", pp.Nature)
	}
	if pp.Blood != "" || pp.Shiny || pp.Colorful {
		t.Errorf("enter 阶段不应带血脉/变异: blood=%q shiny=%v colorful=%v",
			pp.Blood, pp.Shiny, pp.Colorful)
	}
}

// normalBattlePet 处理普通对战:性格一律不展示;污染战斗连血脉也不展示——那条血脉是临时的,
// 捕捉后会变回种族血脉(2026-11-08 污染小电企鹅:23→电 9)。
func TestNormalBattlePet(t *testing.T) {
	db, err := gamedata.Load()
	if err != nil {
		t.Skipf("名称库未生成(先跑 make gamedata): %v", err)
	}
	conf, base := uint32(410430), uint32(3434)
	lv, gender, height, weight := uint32(37), uint32(1), uint32(71), uint32(4761)
	voice := int32(27)
	nature, blood, mutation := uint32(30), uint32(23), uint32(4)
	enemy := func() *pb.PetData {
		return &pb.PetData{
			ConfId: &conf, BaseConfId: &base, Level: &lv, Gender: &gender,
			Height: &height, Weight: &weight, Voice: &voice,
			Nature: &nature, BloodId: &blood, MutationType: &mutation,
		}
	}

	pp := (&Pipeline{db: db}).normalBattlePet(enemy(), true)
	if pp.Species != "小电企鹅" || pp.Level != 37 || pp.Gender != "♂" ||
		pp.WeightKg != 4.761 || pp.Voice != 27 {
		t.Errorf("个体字段不符: %+v", pp)
	}
	if pp.Nature != "" || pp.Blood != "" {
		t.Errorf("污染战斗不应展示性格/血脉: nature=%q blood=%q", pp.Nature, pp.Blood)
	}

	// 非污染:只清性格,血脉保留。
	pd := enemy()
	pd.MutationType = nil
	pp = (&Pipeline{db: db}).normalBattlePet(pd, false)
	if pp.Nature != "" || pp.Blood != "污染" {
		t.Errorf("非污染战斗应只清性格: nature=%q blood=%q", pp.Nature, pp.Blood)
	}
}
