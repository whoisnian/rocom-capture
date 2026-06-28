// Package gamedata 提供从游戏解包数据(nrc/bin)提取的 id->中文名 查找表(编译期 embed)。
package gamedata

import (
	_ "embed"
	"encoding/json"
	"strconv"
)

//go:embed data/names.json
var namesJSON []byte

// Medal 是奖牌的名称与描述。
type Medal struct {
	Name string `json:"name"`
	Desc string `json:"desc"`
}

// DB 是只读名称查找库。
type DB struct {
	species      map[string]string
	nature       map[string]string
	skillDamType map[string]string
	talentRate   map[string]string
	partnerMark  map[string]string
	speciality   map[string]string
	medal        map[string]Medal
	opcodes      map[uint16]string
	natureEffect map[string]NatureEffect
}

// NatureEffect 是性格对六维的增减维度(六维编号 1-6:1生命2物攻3魔攻4物防5魔防6速度)。
type NatureEffect struct {
	Pos int32 `json:"pos"` // +10% 维度
	Neg int32 `json:"neg"` // -10% 维度
}

// Load 加载 embed 的名称表。
func Load() (*DB, error) {
	var raw struct {
		Species      map[string]string       `json:"species"`
		Nature       map[string]string       `json:"nature"`
		SkillDamType map[string]string       `json:"skill_dam_type"`
		TalentRate   map[string]string       `json:"talent_rate"`
		PartnerMark  map[string]string       `json:"partner_mark"`
		Speciality   map[string]string       `json:"speciality"`
		Medal        map[string]Medal        `json:"medal"`
		Opcodes      map[string]string       `json:"opcodes"`
		NatureEffect map[string]NatureEffect `json:"nature_effect"`
	}
	if err := json.Unmarshal(namesJSON, &raw); err != nil {
		return nil, err
	}
	opcodes := make(map[uint16]string, len(raw.Opcodes))
	for k, v := range raw.Opcodes {
		if n, err := strconv.ParseUint(k, 10, 16); err == nil {
			opcodes[uint16(n)] = v
		}
	}
	return &DB{
		species:      raw.Species,
		nature:       raw.Nature,
		skillDamType: raw.SkillDamType,
		talentRate:   raw.TalentRate,
		partnerMark:  raw.PartnerMark,
		speciality:   raw.Speciality,
		medal:        raw.Medal,
		opcodes:      opcodes,
		natureEffect: raw.NatureEffect,
	}, nil
}

// NatureEffect 返回性格的 +10%/-10% 维度(六维编号 1-6;0 表示无)。
func (db *DB) NatureEffect(natureID uint32) NatureEffect { return db.natureEffect[key(natureID)] }

// OpcodeNames 返回 opcode 整数到 ZoneSvrCmd 名称的映射。
func (db *DB) OpcodeNames() map[uint16]string { return db.opcodes }

func key(id uint32) string { return strconv.FormatUint(uint64(id), 10) }

// Species 返回种类名(conf_id)。
func (db *DB) Species(confID uint32) string { return db.species[key(confID)] }

// Nature 返回性格名(nature id)。
func (db *DB) Nature(id uint32) string { return db.nature[key(id)] }

// SkillDamType 返回系别名(SkillDamType enum 整数值)。
func (db *DB) SkillDamType(v int32) string { return db.skillDamType[strconv.FormatInt(int64(v), 10)] }

// TalentRate 返回天分评价名(talent_rank)。
func (db *DB) TalentRate(rank uint32) string { return db.talentRate[key(rank)] }

// PartnerMark 返回标记名(PetPartnerMarkType enum 整数值)。
func (db *DB) PartnerMark(v int32) string { return db.partnerMark[strconv.FormatInt(int64(v), 10)] }

// Speciality 返回特长名(speciality_id)。
func (db *DB) Speciality(id uint32) string { return db.speciality[key(id)] }

// Medal 返回奖牌名称与描述(wear_medal_conf_id)。
func (db *DB) Medal(id uint32) (Medal, bool) { m, ok := db.medal[key(id)]; return m, ok }

// GenderName 返回性别符号。
func GenderName(g uint32) string {
	switch g {
	case 1:
		return "♂"
	case 2:
		return "♀"
	default:
		return ""
	}
}
