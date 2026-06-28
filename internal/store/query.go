package store

import (
	"encoding/json"
	"strings"

	"github.com/whoisnian/rocom-capture/internal/pet"
)

// Filter 是宠物列表的筛选/排序/分页参数。
type Filter struct {
	Search        string   // 名称/种类模糊
	Types         []string // 系别(任一匹配)
	Nature        string
	NatureExclude []string // 性格"其他":排除这些热门性格
	Gender        string
	TalentRank    string
	Medal         string
	Speciality    string
	PartnerMark   string
	Shiny         string // "", "1", "0"
	Colorful      string // "", "1", "0"
	LevelMin      int
	LevelMax      int
	Sort          string
	Order         string
	Page          int
	PageSize      int
}

var sortColumns = map[string]string{
	"gid": "gid", "level": "level", "catchTime": "catch_time",
	"weight": "weight", "height": "height", "voice": "voice",
	"hp": "hp", "attack": "attack", "defense": "defense",
	"spAttack": "sp_attack", "spDefense": "sp_defense", "speed": "speed",
	"name": "name", "species": "species",
}

// ListPets 按筛选条件返回宠物列表与命中总数。
func (s *Store) ListPets(f Filter) (pets []*pet.Pet, total int, err error) {
	var where []string
	var args []any

	if f.Search != "" {
		where = append(where, "(name LIKE ? OR species LIKE ?)")
		args = append(args, "%"+f.Search+"%", "%"+f.Search+"%")
	}
	addEq := func(col, val string) {
		if val != "" {
			where = append(where, col+"=?")
			args = append(args, val)
		}
	}
	addEq("nature", f.Nature)
	if len(f.NatureExclude) > 0 {
		ph := make([]string, len(f.NatureExclude))
		for i, n := range f.NatureExclude {
			ph[i] = "?"
			args = append(args, n)
		}
		where = append(where, "nature NOT IN ("+strings.Join(ph, ",")+")")
	}
	addEq("gender", f.Gender)
	addEq("talent_rank", f.TalentRank)
	addEq("medal", f.Medal)
	addEq("speciality", f.Speciality)
	addEq("partner_mark", f.PartnerMark)
	if f.Shiny == "1" {
		where = append(where, "shiny=1")
	} else if f.Shiny == "0" {
		where = append(where, "shiny=0")
	}
	if f.Colorful == "1" {
		where = append(where, "colorful=1")
	} else if f.Colorful == "0" {
		where = append(where, "colorful=0")
	}
	if f.LevelMin > 0 {
		where = append(where, "level>=?")
		args = append(args, f.LevelMin)
	}
	if f.LevelMax > 0 {
		where = append(where, "level<=?")
		args = append(args, f.LevelMax)
	}
	for _, t := range f.Types { // types 存为 JSON 数组，用 LIKE 匹配带引号的元素
		where = append(where, "types LIKE ?")
		args = append(args, "%\""+t+"\"%")
	}

	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}

	if err = s.db.QueryRow("SELECT COUNT(*) FROM pets"+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	sortCol := sortColumns[f.Sort]
	if sortCol == "" {
		sortCol = "gid"
	}
	order := "ASC"
	if strings.EqualFold(f.Order, "desc") {
		order = "DESC"
	}
	pageSize := f.PageSize
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 12
	}
	page := f.Page
	if page < 1 {
		page = 1
	}

	q := "SELECT data FROM pets" + whereSQL + " ORDER BY " + sortCol + " " + order + " LIMIT ? OFFSET ?"
	args = append(args, pageSize, (page-1)*pageSize)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, 0, err
		}
		var p pet.Pet
		if json.Unmarshal([]byte(data), &p) == nil {
			pets = append(pets, &p)
		}
	}
	return pets, total, rows.Err()
}

// CountPets 返回库中宠物总数。
func (s *Store) CountPets() (int, error) {
	var n int
	err := s.db.QueryRow("SELECT COUNT(*) FROM pets").Scan(&n)
	return n, err
}

// FilterOptions 返回各维度的可选值(用于前端筛选下拉)。
func (s *Store) FilterOptions() map[string][]string {
	out := map[string][]string{}
	for key, col := range map[string]string{
		"nature": "nature", "talentRank": "talent_rank",
		"medal": "medal", "speciality": "speciality", "partnerMark": "partner_mark",
	} {
		rows, err := s.db.Query("SELECT DISTINCT " + col + " FROM pets WHERE " + col + "!='' ORDER BY " + col)
		if err != nil {
			continue
		}
		for rows.Next() {
			var v string
			if rows.Scan(&v) == nil {
				out[key] = append(out[key], v)
			}
		}
		rows.Close()
	}
	return out
}
