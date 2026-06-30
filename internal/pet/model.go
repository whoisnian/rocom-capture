package pet

import (
	"github.com/whoisnian/rocom-capture/internal/gamedata"
	"github.com/whoisnian/rocom-capture/internal/pb"
)

// PetBoxLoc 是宠物在仓库盒子里的位置(box_id 从 1 起,slot 盒内格位从 0 起)。
type PetBoxLoc struct {
	BoxID   int32  `json:"boxId"`             // 盒子编号
	Slot    int32  `json:"slot"`              // 盒内格位(0 起)
	BoxName string `json:"boxName,omitempty"` // 盒子名(玩家命名,可空)
	Mark    string `json:"mark,omitempty"`    // 分类标记中文(首领/污染/奇异/炫彩/闪光)
}

// PetTeamLoc 是宠物在大世界队伍中的位置(teamIdx/pos 均从 0 起,最多 3 队、每队 6 位)。
type PetTeamLoc struct {
	TeamIdx int32 `json:"teamIdx"` // 第几队(0 起)
	Pos     int32 `json:"pos"`     // 队内位置(0 起)
}

// Stat 是一项六维属性。
type Stat struct {
	Value    int32 `json:"value"`    // 最终面板值
	TalentLv int32 `json:"talentLv"` // 天分等级(1-10，0 表示该维度无天分)
	Nature   int8  `json:"nature"`   // 性格影响：1=增益(+10%) -1=减益(-10%) 0=无
}

// Pet 是用于前端展示/存储的业务模型(已中文化)。
type Pet struct {
	Gid        uint32 `json:"gid"`             // 唯一实例 id
	ConfID     uint32 `json:"confId"`          // 种类配置 id(指向进化线一阶 base)
	BaseConfID uint32 `json:"baseConfId"`      // 当前形态 petbase id(进化后随之变化)
	Species    string `json:"species"`         // 种类名(当前形态)
	Book       uint32 `json:"book,omitempty"`  // 图鉴编号
	Form       string `json:"form,omitempty"`  // 地区/季节形态名(普通宠物为空)
	Stage      uint32 `json:"stage,omitempty"` // 进化阶段
	Name       string `json:"name"`            // 昵称
	Level      uint32 `json:"level"`

	NatureID uint32   `json:"natureId"`
	Nature   string   `json:"nature"` // 性格名
	Gender   string   `json:"gender"` // ♂ / ♀
	Types    []string `json:"types"`  // 系别中文(可多系)

	HeightM  float64 `json:"heightM"`  // 身高(米)
	WeightKg float64 `json:"weightKg"` // 体重(千克)
	Voice    int32   `json:"voice"`    // 声音值

	TalentRank      string   `json:"talentRank"` // 天分评价
	Medal           string   `json:"medal"`      // 佩戴奖牌名
	MedalDesc       string   `json:"medalDesc"`
	WearMedalConfID uint32   `json:"wearMedalConfId"`
	MedalIDs        []uint32 `json:"medalIds"`    // 该宠物已拥有的奖牌 id(佩戴+custom+free,去重)
	PartnerMark     string   `json:"partnerMark"` // 标记
	Speciality      string   `json:"speciality"`  // 特长
	SpecialityID    uint32   `json:"specialityId"`

	CatchTime int64 `json:"catchTime"` // 捕捉时间(unix 秒)
	Shiny     bool  `json:"shiny"`     // 异色(mutation_type bit0)
	Colorful  bool  `json:"colorful"`  // 炫彩(mutation_type bit3)

	Image gamedata.PetImage `json:"image"` // 各尺寸图片相对路径(由前端拼到 /img/ 下)

	Box  *PetBoxLoc  `json:"box,omitempty"`  // 仓库盒子位置(来自 PetBackpackInfo,读取时 JOIN 注入)
	Team *PetTeamLoc `json:"team,omitempty"` // 大世界队伍位置(在队宠物不在盒子里,二者互斥)

	HP        Stat `json:"hp"`
	Attack    Stat `json:"attack"`    // 物攻
	Defense   Stat `json:"defense"`   // 物防
	SpAttack  Stat `json:"spAttack"`  // 魔攻
	SpDefense Stat `json:"spDefense"` // 魔防
	Speed     Stat `json:"speed"`

	SkillIDs []uint32 `json:"skillIds"`
}

// ToPet 把解码后的 PetData 结合名称库转成业务模型。
func ToPet(p *pb.PetData, db *gamedata.DB) *Pet {
	types := make([]string, 0, len(p.GetSkillDamType()))
	for _, t := range p.GetSkillDamType() {
		if name := db.SkillDamType(int32(t)); name != "" {
			types = append(types, name)
		}
	}

	// 当前形态:base_conf_id 直接指向当前 petbase(进化后随之变化),据此取名称/头像/图鉴/形态;
	// 旧逻辑用 conf_id 只会得到进化线一阶 base(火神显示成火花),故优先用 base_conf_id,缺失再回退。
	// mutation_type bit0=异色,异色宠物部分有专属头像/全身图(无则回退普通)。
	shiny := p.GetMutationType()&1 != 0
	confID, base := p.GetConfId(), p.GetBaseConfId()
	species, image := db.Species(confID), db.PetImage(confID, shiny)
	var book, stage uint32
	var form string
	if base != 0 {
		if info, ok := db.PetBase(base); ok {
			if info.Name != "" {
				species = info.Name
			}
			book, form, stage = info.Book, info.Form, info.Stage
			if img := db.PetImageByBase(base, shiny); img != (gamedata.PetImage{}) {
				image = img
			}
		}
	}

	out := &Pet{
		Gid:        p.GetGid(),
		ConfID:     confID,
		BaseConfID: base,
		Species:    species,
		Book:       book,
		Form:       form,
		Stage:      stage,
		Name:       string(p.GetName()),
		Level:      p.GetLevel(),
		NatureID:   p.GetNature(),
		Nature:     db.Nature(p.GetNature()),
		Gender:     gamedata.GenderName(p.GetGender()),
		Types:      types,
		HeightM:    float64(p.GetHeight()) / 100,
		WeightKg:   float64(p.GetWeight()) / 1000,
		Voice:      p.GetVoice(),

		TalentRank:      db.TalentRate(p.GetTalentRank()),
		WearMedalConfID: p.GetWearMedalConfId(),
		PartnerMark:     db.PartnerMark(int32(p.GetPartnerMark())),
		SpecialityID:    p.GetSpecialityId(),
		Speciality:      db.Speciality(p.GetSpecialityId()),

		CatchTime: int64(p.GetAddTime()),
		// mutation_type 为位标志: bit0=异色, bit3=炫彩(实测样本验证)。
		Shiny:    shiny,
		Colorful: p.GetMutationType()&8 != 0,

		Image: image,
	}

	if m, ok := db.Medal(p.GetWearMedalConfId()); ok {
		out.Medal = m.Name
		out.MedalDesc = m.Desc
	}
	// 该宠物已拥有的奖牌(佩戴 + custom + free,去重),供奖牌墙高亮。
	seen := map[uint32]bool{}
	for _, id := range append([]uint32{p.GetWearMedalConfId()}, append(p.GetCustomMedalConfId(), p.GetFreeMedalConfIds()...)...) {
		if id != 0 && !seen[id] {
			seen[id] = true
			out.MedalIDs = append(out.MedalIDs, id)
		}
	}

	// 六维按编号 1-6 顺序: 1生命 2物攻 3魔攻 4物防 5魔防 6速度。
	stats := []*Stat{&out.HP, &out.Attack, &out.SpAttack, &out.Defense, &out.SpDefense, &out.Speed}

	// 性格增减维度(道具修改过则以 changed_nature_* 为准，否则取性格默认)。
	ne := db.NatureEffect(p.GetNature())
	posAttr, negAttr := ne.Pos, ne.Neg
	if t := int32(p.GetChangedNaturePosAttrType()); t != 0 {
		posAttr = t
	}
	if t := int32(p.GetChangedNatureNegAttrType()); t != 0 {
		negAttr = t
	}
	for i, s := range stats {
		idx := int32(i + 1)
		if idx == posAttr {
			s.Nature = 1
		} else if idx == negAttr {
			s.Nature = -1
		}
	}

	if attr := p.GetAttributeInfo(); attr != nil {
		src := []*pb.PetAttributeData{
			attr.GetHp(), attr.GetAttack(), attr.GetSpecialAttack(),
			attr.GetDefense(), attr.GetSpecialDefense(), attr.GetSpeed(),
		}
		for i, a := range src {
			if a != nil {
				stats[i].Value = int32(a.GetBaseValue())
				stats[i].TalentLv = a.GetTalentAddValue() // 天分等级(1-10)
			}
		}
	}

	// attribute_new_info 直接给出最终面板值(已含等级/努力/奖牌加成)，覆盖 base_value。
	if newAttr := p.GetAttributeNewInfo(); newAttr != nil {
		finals := make(map[int32]int32)
		for _, a := range newAttr.GetAddiAttrData() {
			finals[a.GetType()] += a.GetAddiAttr()
		}
		for i, s := range stats {
			if v, ok := finals[int32(i+1)]; ok {
				s.Value = v
			}
		}
	}

	if sk := p.GetSkill(); sk != nil {
		for _, s := range sk.GetSkillData() {
			out.SkillIDs = append(out.SkillIDs, s.GetId())
		}
	}
	return out
}
