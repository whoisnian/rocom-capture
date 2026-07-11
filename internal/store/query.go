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
	MedalIDs      []uint32 // 拥有该奖牌(pet_medal 里含任一 id)即命中;由服务层将奖牌名解析为 id
	Speciality    string
	EggGroup      string // 蛋组名(精确匹配组名,含该组即命中)
	PartnerMark   string
	Shiny         string // "", "1", "0"
	Colorful      string // "", "1", "0"
	Form          string // 地区/季节形态名(精确匹配)
	Box           string // 宠物盒,形如 "13-性格1"(取前导整数为 box_id 过滤)
	CatchAfter    int64  // 捕捉时间下限(unix 秒;>0 时筛 catch_time>=该值,由前端按所选区间算)
	LevelMin      int
	LevelMax      int
	Sort          string
	Order         string
	Page          int
	PageSize      int
}

var sortColumns = map[string]string{
	"gid": "gid", "level": "level", "catchTime": "catch_time",
	// 体重/身高按「形态范围内百分位」排序,便于跨种族找相对自身偏大/偏小的宠物(缺范围者排末尾)。
	"weight": "weight_pct", "height": "height_pct", "voice": "voice",
	"hp": "hp", "attack": "attack", "defense": "defense",
	"spAttack": "sp_attack", "spDefense": "sp_defense", "speed": "speed",
	"name": "name", "species": "species",
}

// buildWhere 由筛选条件构造 WHERE 子句与参数(列名均属 pets 表)。首谓词恒为
// account=?(account 作 args[0]);盒子筛选子查询用相关子查询 account=pets.account
// 收窄到同账号,避免额外占位符。
func buildWhere(f Filter, account string) (string, []any) {
	where := []string{"account=?"}
	args := []any{account}
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
	if f.CatchAfter > 0 {
		where = append(where, "catch_time>=?")
		args = append(args, f.CatchAfter)
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
	if f.EggGroup != "" { // egg_groups 亦为 JSON 组名数组,LIKE 匹配含该组的宠物
		where = append(where, "egg_groups LIKE ?")
		args = append(args, "%\""+f.EggGroup+"\"%")
	}
	if len(f.MedalIDs) > 0 { // 拥有任一目标奖牌即命中(关联 pet_medal,限本账号)
		ph := make([]string, len(f.MedalIDs))
		for i, id := range f.MedalIDs {
			ph[i] = "?"
			args = append(args, id)
		}
		where = append(where, "gid IN (SELECT gid FROM pet_medal WHERE medal_id IN ("+strings.Join(ph, ",")+") AND account=pets.account)")
	}
	if f.Box != "" { // 取前导整数为 box_id,关联 pet_box 表(限本账号)
		idStr := f.Box
		if i := strings.IndexByte(idStr, '-'); i >= 0 {
			idStr = idStr[:i]
		}
		if id, err := strconv.Atoi(idStr); err == nil {
			where = append(where, "gid IN (SELECT gid FROM pet_box WHERE box_id=? AND account=pets.account)")
			args = append(args, id)
		}
	}
	return " WHERE " + strings.Join(where, " AND "), args
}

// 位置排序键:大世界队伍在前(team_idx*6+pos),其后按盒子(1000+box_id*100+slot),其余末尾。
// 子查询用相关条件 account=pets.account 限本账号,无需额外占位符。
const boxPosExpr = `COALESCE(` +
	`(SELECT team_idx*6+pos FROM pet_team WHERE pet_team.gid=pets.gid AND pet_team.account=pets.account),` +
	`(SELECT 1000+box_id*100+slot FROM pet_box WHERE pet_box.gid=pets.gid AND pet_box.account=pets.account),999999)`

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
	// 百分位列可空(缺形态范围),用 `col IS NULL` 前置把 NULL 无论升降都排到末尾。
	if strings.HasSuffix(col, "_pct") {
		return col + " IS NULL, " + col + " " + dir + ", gid"
	}
	return col + " " + dir + ", gid"
}

func clampPageSize(n int) int {
	if n <= 0 || n > 200 {
		return 12
	}
	return n
}

// PetPage 返回 gid 在本账号当前筛选+排序下所处的页码(1 起)及是否命中筛选。
// found=false 表示该宠物不在当前筛选结果内(此时 page 退回 1,调用方可据此决定是否清空筛选)。
func (sc *Scoped) PetPage(gid uint32, f Filter) (page int, found bool) {
	whereSQL, args := buildWhere(f, sc.account)
	rows, err := sc.db.Query("SELECT gid FROM pets"+whereSQL+" ORDER BY "+buildOrder(f), args...)
	if err != nil {
		return 1, false
	}
	defer rows.Close()
	idx := 0
	for rows.Next() {
		var g uint32
		if rows.Scan(&g) == nil {
			if g == gid {
				return idx/clampPageSize(f.PageSize) + 1, true
			}
			idx++
		}
	}
	return 1, false
}

// ListPets 按筛选条件返回本账号宠物列表与命中总数。
func (sc *Scoped) ListPets(f Filter) (pets []*pet.Pet, total int, err error) {
	whereSQL, args := buildWhere(f, sc.account)

	if err = sc.db.QueryRow("SELECT COUNT(*) FROM pets"+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	pageSize := clampPageSize(f.PageSize)
	page := f.Page
	if page < 1 {
		page = 1
	}

	q := "SELECT data FROM pets" + whereSQL + " ORDER BY " + buildOrder(f) + " LIMIT ? OFFSET ?"
	args = append(args, pageSize, (page-1)*pageSize)
	rows, err := sc.db.Query(q, args...)
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
	sc.attachLocations(pets)
	return pets, total, nil
}

// attachLocations 给一页宠物批量注入盒子/队伍位置(各一次查询,按 gid 映射)。
// 每个查询各构一份 args(account 置首),避免共享 slice 被 append 污染。
func (sc *Scoped) attachLocations(pets []*pet.Pet) {
	if len(pets) == 0 {
		return
	}
	byGid := make(map[uint32]*pet.Pet, len(pets))
	ph := make([]string, len(pets))
	gidArgs := make([]any, len(pets))
	for i, p := range pets {
		byGid[p.Gid] = p
		ph[i] = "?"
		gidArgs[i] = p.Gid
	}
	in := "(" + strings.Join(ph, ",") + ")"
	argsWith := func() []any { return append([]any{sc.account}, gidArgs...) }

	if rows, err := sc.db.Query(`SELECT gid,box_id,slot,box_name,mark FROM pet_box WHERE account=? AND gid IN `+in, argsWith()...); err == nil {
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
	if rows, err := sc.db.Query(`SELECT gid,team_idx,pos FROM pet_team WHERE account=? AND gid IN `+in, argsWith()...); err == nil {
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
	if rows, err := sc.db.Query(`SELECT gid,medal_id FROM pet_medal WHERE account=? AND gid IN `+in+` ORDER BY medal_id`, argsWith()...); err == nil {
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

// CountPets 返回本账号宠物总数。
func (sc *Scoped) CountPets() (int, error) {
	var n int
	err := sc.db.QueryRow("SELECT COUNT(*) FROM pets WHERE account=?", sc.account).Scan(&n)
	return n, err
}

// OwnedMedalIDs 返回本账号所有宠物拥有过的奖牌 id(去重升序),供服务层映射为名称做筛选下拉。
func (sc *Scoped) OwnedMedalIDs() []uint32 {
	rows, err := sc.db.Query(`SELECT DISTINCT medal_id FROM pet_medal WHERE account=? ORDER BY medal_id`, sc.account)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []uint32
	for rows.Next() {
		var id uint32
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}

// FilterOptions 返回本账号各维度的可选值(用于前端筛选下拉)。
func (sc *Scoped) FilterOptions() map[string][]string {
	out := map[string][]string{}
	for key, col := range map[string]string{
		"nature": "nature", "talentRank": "talent_rank",
		"speciality": "speciality", "partnerMark": "partner_mark",
		"form": "form",
	} {
		rows, err := sc.db.Query("SELECT DISTINCT "+col+" FROM pets WHERE account=? AND "+col+"!='' ORDER BY "+col, sc.account)
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
	if rows, err := sc.db.Query(`SELECT DISTINCT box_id, box_name FROM pet_box WHERE account=? ORDER BY box_id`, sc.account); err == nil {
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
