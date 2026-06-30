package store

import (
	"encoding/json"
	"fmt"
	"strconv"
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
	Form          string // 地区/季节形态名(精确匹配)
	Box           string // 宠物盒,形如 "13-性格1"(取前导整数为 box_id 过滤)
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

// buildWhere 由筛选条件构造 WHERE 子句与参数(列名均属 pets 表)。
func buildWhere(f Filter) (string, []any) {
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
	addEq("form", f.Form)
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
	if f.Box != "" { // 取前导整数为 box_id,关联 pet_box 表
		idStr := f.Box
		if i := strings.IndexByte(idStr, '-'); i >= 0 {
			idStr = idStr[:i]
		}
		if id, err := strconv.Atoi(idStr); err == nil {
			where = append(where, "gid IN (SELECT gid FROM pet_box WHERE box_id=?)")
			args = append(args, id)
		}
	}
	if len(where) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(where, " AND "), args
}

// 位置排序键:大世界队伍在前(team_idx*6+pos),其后按盒子(1000+box_id*100+slot),其余末尾。
const boxPosExpr = `COALESCE(` +
	`(SELECT team_idx*6+pos FROM pet_team WHERE pet_team.gid=pets.gid),` +
	`(SELECT 1000+box_id*100+slot FROM pet_box WHERE pet_box.gid=pets.gid),999999)`

// buildOrder 构造 ORDER BY 表达式(含 gid 兜底,保证稳定顺序)。
func buildOrder(f Filter) string {
	dir := "ASC"
	if strings.EqualFold(f.Order, "desc") {
		dir = "DESC"
	}
	if f.Sort == "boxpos" {
		return boxPosExpr + " " + dir + ", gid"
	}
	col := sortColumns[f.Sort]
	if col == "" {
		col = "gid"
	}
	return col + " " + dir + ", gid"
}

func clampPageSize(n int) int {
	if n <= 0 || n > 200 {
		return 12
	}
	return n
}

// PetPage 返回 gid 在当前筛选+排序下所处的页码(1 起,未命中返回 1)。
func (s *Store) PetPage(gid uint32, f Filter) int {
	whereSQL, args := buildWhere(f)
	rows, err := s.db.Query("SELECT gid FROM pets"+whereSQL+" ORDER BY "+buildOrder(f), args...)
	if err != nil {
		return 1
	}
	defer rows.Close()
	idx := 0
	for rows.Next() {
		var g uint32
		if rows.Scan(&g) == nil {
			if g == gid {
				return idx/clampPageSize(f.PageSize) + 1
			}
			idx++
		}
	}
	return 1
}

// ListPets 按筛选条件返回宠物列表与命中总数。
func (s *Store) ListPets(f Filter) (pets []*pet.Pet, total int, err error) {
	whereSQL, args := buildWhere(f)

	if err = s.db.QueryRow("SELECT COUNT(*) FROM pets"+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	pageSize := clampPageSize(f.PageSize)
	page := f.Page
	if page < 1 {
		page = 1
	}

	q := "SELECT data FROM pets" + whereSQL + " ORDER BY " + buildOrder(f) + " LIMIT ? OFFSET ?"
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
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	s.attachLocations(pets)
	return pets, total, nil
}

// attachLocations 给一页宠物批量注入盒子/队伍位置(各一次查询,按 gid 映射)。
func (s *Store) attachLocations(pets []*pet.Pet) {
	if len(pets) == 0 {
		return
	}
	byGid := make(map[uint32]*pet.Pet, len(pets))
	ph := make([]string, len(pets))
	args := make([]any, len(pets))
	for i, p := range pets {
		byGid[p.Gid] = p
		ph[i] = "?"
		args[i] = p.Gid
	}
	in := "(" + strings.Join(ph, ",") + ")"

	if rows, err := s.db.Query(`SELECT gid,box_id,slot,box_name,mark FROM pet_box WHERE gid IN `+in, args...); err == nil {
		for rows.Next() {
			var gid uint32
			var boxID, slot, mark int32
			var name string
			if rows.Scan(&gid, &boxID, &slot, &name, &mark) == nil {
				if p := byGid[gid]; p != nil {
					p.Box = &pet.PetBoxLoc{BoxID: boxID, Slot: slot, BoxName: name, Mark: pet.MarkName(mark)}
				}
			}
		}
		rows.Close()
	}
	if rows, err := s.db.Query(`SELECT gid,team_idx,pos FROM pet_team WHERE gid IN `+in, args...); err == nil {
		for rows.Next() {
			var gid uint32
			var teamIdx, pos int32
			if rows.Scan(&gid, &teamIdx, &pos) == nil {
				if p := byGid[gid]; p != nil {
					p.Team = &pet.PetTeamLoc{TeamIdx: teamIdx, Pos: pos}
				}
			}
		}
		rows.Close()
	}
	// 拥有的奖牌(覆盖 ToPet 里仅佩戴的那枚);先清空有 pet_medal 记录的宠物再填,避免回退。
	if rows, err := s.db.Query(`SELECT gid,medal_id FROM pet_medal WHERE gid IN `+in+` ORDER BY medal_id`, args...); err == nil {
		seen := map[uint32]bool{}
		for rows.Next() {
			var gid, mid uint32
			if rows.Scan(&gid, &mid) == nil {
				if p := byGid[gid]; p != nil {
					if !seen[gid] {
						p.MedalIDs = nil
						seen[gid] = true
					}
					p.MedalIDs = append(p.MedalIDs, mid)
				}
			}
		}
		rows.Close()
	}
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
		"form": "form",
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
	// 宠物盒:取 pet_box 里出现的盒子,形如 "13-性格1"(未命名 → "18-盒18")。
	if rows, err := s.db.Query(`SELECT DISTINCT box_id, box_name FROM pet_box ORDER BY box_id`); err == nil {
		for rows.Next() {
			var id int
			var name string
			if rows.Scan(&id, &name) == nil {
				if name == "" {
					name = fmt.Sprintf("盒%d", id)
				}
				out["box"] = append(out["box"], fmt.Sprintf("%d-%s", id, name))
			}
		}
		rows.Close()
	}
	return out
}
