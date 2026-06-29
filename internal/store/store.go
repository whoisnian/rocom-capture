// Package store 用 SQLite(纯 Go 驱动)持久化宠物当前状态与事件历史，并支持筛选查询。
package store

import (
	"database/sql"
	"encoding/json"
	"time"

	_ "modernc.org/sqlite"

	"github.com/whoisnian/rocom-capture/internal/pet"
)

// EventKind 是宠物变更事件类型。
type EventKind string

const (
	EventObtain EventKind = "obtain" // 获得(捕捉/孵蛋/赠送获得)
	EventLose   EventKind = "lose"   // 失去(放生/赠送出)
)

// Event 是一条宠物变更事件。
type Event struct {
	ID      int64     `json:"id"`
	Time    int64     `json:"time"`
	Kind    EventKind `json:"kind"`
	SubKind string    `json:"subKind"` // 捕捉/孵蛋/赠送 等(由 catch_way 推断)
	Gid     uint32    `json:"gid"`
	Pet     *pet.Pet  `json:"pet"`
}

// Store 封装 SQLite 连接。
type Store struct{ db *sql.DB }

// New 打开(或创建)数据库并建表。
func New(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite 写入串行化，避免 database is locked
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS pets (
  gid INTEGER PRIMARY KEY,
  conf_id INTEGER, species TEXT, name TEXT, level INTEGER,
  nature_id INTEGER, nature TEXT, gender TEXT, types TEXT,
  height REAL, weight REAL, voice INTEGER,
  talent_rank TEXT, medal TEXT, medal_id INTEGER, partner_mark TEXT,
  speciality TEXT, speciality_id INTEGER,
  catch_time INTEGER, shiny INTEGER, colorful INTEGER,
  hp INTEGER, attack INTEGER, defense INTEGER,
  sp_attack INTEGER, sp_defense INTEGER, speed INTEGER,
  data TEXT, updated_at INTEGER
);
CREATE INDEX IF NOT EXISTS idx_pets_species ON pets(species);
CREATE INDEX IF NOT EXISTS idx_pets_level ON pets(level);
CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  time INTEGER, kind TEXT, sub_kind TEXT, gid INTEGER,
  species TEXT, nature TEXT, medal TEXT, shiny INTEGER,
  data TEXT
);
CREATE INDEX IF NOT EXISTS idx_events_time ON events(time);
CREATE TABLE IF NOT EXISTS pet_box (
  gid INTEGER PRIMARY KEY,
  box_id INTEGER, slot INTEGER, box_name TEXT, mark INTEGER
);
CREATE TABLE IF NOT EXISTS pet_team (
  gid INTEGER PRIMARY KEY,
  team_idx INTEGER, pos INTEGER
);
`)
	return err
}

// ReplacePetBoxes 用一份完整背包快照替换所有宠物盒子位置(整体 DELETE + 批量插入)。
func (s *Store) ReplacePetBoxes(entries []pet.BoxEntry) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM pet_box`); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO pet_box(gid,box_id,slot,box_name,mark) VALUES(?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, e := range entries {
		if _, err = stmt.Exec(e.Gid, e.BoxID, e.Slot, e.BoxName, e.Mark); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ApplyBoxMoves 增量应用盒位变更(box_pet_change):把每只宠物移到新(盒,格);
// 盒名/标记沿用该盒既有行(随盒不随宠);宠物入盒即不在队伍,清除其队伍位置。
func (s *Store) ApplyBoxMoves(entries []pet.BoxEntry) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	up, err := tx.Prepare(`INSERT OR REPLACE INTO pet_box(gid,box_id,slot,box_name,mark) VALUES(?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer up.Close()
	for _, e := range entries {
		var name string
		var mark int32
		// 盒名/标记是盒的属性,取该盒任一既有行(增量包不携带)。
		tx.QueryRow(`SELECT box_name,mark FROM pet_box WHERE box_id=? AND gid<>? LIMIT 1`, e.BoxID, e.Gid).Scan(&name, &mark)
		if _, err = up.Exec(e.Gid, e.BoxID, e.Slot, name, mark); err != nil {
			return err
		}
		if _, err = tx.Exec(`DELETE FROM pet_team WHERE gid=?`, e.Gid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// boxLocFor 读取单只宠物的盒子位置(无则 nil),供 GetPet 注入。
func (s *Store) boxLocFor(gid uint32) *pet.PetBoxLoc {
	var boxID, slot, mark int32
	var name string
	err := s.db.QueryRow(`SELECT box_id,slot,box_name,mark FROM pet_box WHERE gid=?`, gid).Scan(&boxID, &slot, &name, &mark)
	if err != nil {
		return nil
	}
	return &pet.PetBoxLoc{BoxID: boxID, Slot: slot, BoxName: name, Mark: pet.MarkName(mark)}
}

// ReplacePetTeams 用一份大世界队伍快照替换所有宠物队伍位置。
func (s *Store) ReplacePetTeams(entries []pet.TeamEntry) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM pet_team`); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO pet_team(gid,team_idx,pos) VALUES(?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, e := range entries {
		if _, err = stmt.Exec(e.Gid, e.TeamIdx, e.Pos); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// teamLocFor 读取单只宠物的队伍位置(无则 nil),供 GetPet 注入。
func (s *Store) teamLocFor(gid uint32) *pet.PetTeamLoc {
	var teamIdx, pos int32
	if s.db.QueryRow(`SELECT team_idx,pos FROM pet_team WHERE gid=?`, gid).Scan(&teamIdx, &pos) != nil {
		return nil
	}
	return &pet.PetTeamLoc{TeamIdx: teamIdx, Pos: pos}
}

// UpsertPet 插入或更新一只宠物，返回是否为新增(库中此前无该 gid)。
func (s *Store) UpsertPet(p *pet.Pet) (isNew bool, err error) {
	var one int
	err = s.db.QueryRow(`SELECT 1 FROM pets WHERE gid=?`, p.Gid).Scan(&one)
	isNew = err == sql.ErrNoRows
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}

	data, _ := json.Marshal(p)
	types, _ := json.Marshal(p.Types)
	_, err = s.db.Exec(`
INSERT INTO pets(gid,conf_id,species,name,level,nature_id,nature,gender,types,
  height,weight,voice,talent_rank,medal,medal_id,partner_mark,speciality,speciality_id,
  catch_time,shiny,colorful,hp,attack,defense,sp_attack,sp_defense,speed,data,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(gid) DO UPDATE SET
  conf_id=excluded.conf_id,species=excluded.species,name=excluded.name,level=excluded.level,
  nature_id=excluded.nature_id,nature=excluded.nature,gender=excluded.gender,types=excluded.types,
  height=excluded.height,weight=excluded.weight,voice=excluded.voice,talent_rank=excluded.talent_rank,
  medal=excluded.medal,medal_id=excluded.medal_id,partner_mark=excluded.partner_mark,
  speciality=excluded.speciality,speciality_id=excluded.speciality_id,catch_time=excluded.catch_time,
  shiny=excluded.shiny,colorful=excluded.colorful,hp=excluded.hp,attack=excluded.attack,defense=excluded.defense,
  sp_attack=excluded.sp_attack,sp_defense=excluded.sp_defense,speed=excluded.speed,
  data=excluded.data,updated_at=excluded.updated_at`,
		p.Gid, p.ConfID, p.Species, p.Name, p.Level, p.NatureID, p.Nature, p.Gender, string(types),
		p.HeightM, p.WeightKg, p.Voice, p.TalentRank, p.Medal, p.WearMedalConfID, p.PartnerMark,
		p.Speciality, p.SpecialityID, p.CatchTime, b2i(p.Shiny), b2i(p.Colorful),
		p.HP.Value, p.Attack.Value, p.Defense.Value, p.SpAttack.Value, p.SpDefense.Value, p.Speed.Value,
		string(data), time.Now().Unix())
	return isNew, err
}

// RemovePet 删除宠物，返回被删除的快照(若存在)。
func (s *Store) RemovePet(gid uint32) (*pet.Pet, error) {
	p, err := s.GetPet(gid)
	if err != nil || p == nil {
		return nil, err
	}
	_, err = s.db.Exec(`DELETE FROM pets WHERE gid=?`, gid)
	return p, err
}

// GetPet 按 gid 返回宠物。
func (s *Store) GetPet(gid uint32) (*pet.Pet, error) {
	var data string
	err := s.db.QueryRow(`SELECT data FROM pets WHERE gid=?`, gid).Scan(&data)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p pet.Pet
	if err := json.Unmarshal([]byte(data), &p); err != nil {
		return nil, err
	}
	p.Box = s.boxLocFor(gid)
	p.Team = s.teamLocFor(gid)
	return &p, nil
}

// AddEvent 写入一条事件。
func (s *Store) AddEvent(e *Event) error {
	data, _ := json.Marshal(e.Pet)
	res, err := s.db.Exec(`INSERT INTO events(time,kind,sub_kind,gid,species,nature,medal,shiny,data)
VALUES(?,?,?,?,?,?,?,?,?)`,
		e.Time, e.Kind, e.SubKind, e.Gid,
		nz(e.Pet, func(p *pet.Pet) any { return p.Species }),
		nz(e.Pet, func(p *pet.Pet) any { return p.Nature }),
		nz(e.Pet, func(p *pet.Pet) any { return p.Medal }),
		nzb(e.Pet), string(data))
	if err != nil {
		return err
	}
	e.ID, _ = res.LastInsertId()
	return nil
}

// ListEvents 返回最近事件(按时间倒序)。
func (s *Store) ListEvents(limit, beforeID int) ([]*Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id,time,kind,sub_kind,gid,data FROM events`
	var args []any
	if beforeID > 0 {
		q += ` WHERE id < ?`
		args = append(args, beforeID)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Event
	for rows.Next() {
		var e Event
		var data string
		if err := rows.Scan(&e.ID, &e.Time, &e.Kind, &e.SubKind, &e.Gid, &data); err != nil {
			return nil, err
		}
		var p pet.Pet
		if json.Unmarshal([]byte(data), &p) == nil {
			e.Pet = &p
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nz(p *pet.Pet, f func(*pet.Pet) any) any {
	if p == nil {
		return ""
	}
	return f(p)
}

func nzb(p *pet.Pet) int {
	if p == nil {
		return 0
	}
	return b2i(p.Shiny)
}
