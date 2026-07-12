// Package store 用 SQLite(纯 Go 驱动)持久化宠物当前状态与事件历史，并支持筛选查询。
package store

import (
	"database/sql"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/whoisnian/rocom-capture/internal/gamedata"
	"github.com/whoisnian/rocom-capture/internal/pet"
)

// Event 是一条获得宠物事件(放生/赠送出等减少事件不入库)。
type Event struct {
	ID      int64    `json:"id"`
	Time    int64    `json:"time"`
	SubKind string   `json:"subKind"` // 捕捉/孵蛋/赠送 等(由 catch_way 推断)
	Gid     uint32   `json:"gid"`
	Pet     *pet.Pet `json:"pet"`
}

// Store 封装 SQLite 连接。跨账号操作(migrate/accounts 表)挂在此。
// gd 用于在写入时把身高/体重换算成形态内百分位并落列,支撑跨种族的百分位排序。
type Store struct {
	db *sql.DB
	gd *gamedata.DB
}

// Scoped 是绑定了某个 account 的 Store 视图:所有按账号隔离的读写都经它进行,
// account 由 For 注入,方法内部不再显式接收 account,避免漏传导致跨账号串数据。
type Scoped struct {
	db      *sql.DB
	gd      *gamedata.DB
	account string
}

// For 返回绑定指定 account 的视图。
func (s *Store) For(account string) *Scoped { return &Scoped{db: s.db, gd: s.gd, account: account} }

// New 打开(或创建)数据库并建表。gd 供写入时计算身高/体重百分位排序列。
func New(path string, gd *gamedata.DB) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite 写入串行化，避免 database is locked
	// 性能:默认 rollback 日志 + synchronous=FULL 会对每次自动提交 fsync,登录后全量宠物分页
	// 逐只 UpsertPet(数百次独立提交)时磁盘上每只≈16ms、每页(50 只)≈800ms,整轮拖到近 10s,
	// 处理速度赶不上抓包到达速度而积压。改 WAL + synchronous=NORMAL:提交不再逐次 fsync
	// (仅 checkpoint 时落盘),被动抓包丢库即便宕机最多丢尾部若干条、可经下次登录快照重建,
	// 该取舍安全。busy_timeout 兜底避免偶发 database is locked。
	for _, pragma := range []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA synchronous=NORMAL`,
		`PRAGMA busy_timeout=5000`,
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, err
		}
	}
	s := &Store{db: db, gd: gd}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS pets (
  account TEXT NOT NULL,
  gid INTEGER,
  conf_id INTEGER, species TEXT, name TEXT, level INTEGER,
  nature_id INTEGER, nature TEXT, gender TEXT, types TEXT,
  height REAL, weight REAL, voice INTEGER,
  talent_rank TEXT, medal TEXT, medal_id INTEGER, partner_mark TEXT,
  speciality TEXT, speciality_id INTEGER,
  catch_time INTEGER, shiny INTEGER, colorful INTEGER,
  hp INTEGER, attack INTEGER, defense INTEGER,
  sp_attack INTEGER, sp_defense INTEGER, speed INTEGER,
  form TEXT, egg_groups TEXT,
  data TEXT, updated_at INTEGER,
  PRIMARY KEY(account, gid)
);
CREATE INDEX IF NOT EXISTS idx_pets_species ON pets(species);
CREATE INDEX IF NOT EXISTS idx_pets_level ON pets(level);
CREATE INDEX IF NOT EXISTS idx_pets_form ON pets(form);
CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  account TEXT NOT NULL,
  time INTEGER, sub_kind TEXT, gid INTEGER,
  species TEXT, nature TEXT, medal TEXT, shiny INTEGER,
  data TEXT
);
CREATE INDEX IF NOT EXISTS idx_events_account_time ON events(account, time);
CREATE TABLE IF NOT EXISTS pet_box (
  account TEXT NOT NULL, gid INTEGER,
  box_id INTEGER, slot INTEGER, box_name TEXT, mark INTEGER,
  PRIMARY KEY(account, gid)
);
CREATE TABLE IF NOT EXISTS pet_boxes (
  account TEXT NOT NULL, box_id INTEGER,
  name TEXT, mark INTEGER, lock INTEGER,
  PRIMARY KEY(account, box_id)
);
CREATE TABLE IF NOT EXISTS pet_team (
  account TEXT NOT NULL, gid INTEGER,
  team_idx INTEGER, pos INTEGER,
  PRIMARY KEY(account, gid)
);
CREATE TABLE IF NOT EXISTS pet_medal (
  account TEXT NOT NULL, gid INTEGER, medal_id INTEGER,
  PRIMARY KEY(account, gid, medal_id)
);
CREATE TABLE IF NOT EXISTS accounts (
  account TEXT PRIMARY KEY, name TEXT, updated_at INTEGER
);
CREATE TABLE IF NOT EXISTS sessions (
  conn_id TEXT PRIMARY KEY, key BLOB, account TEXT, updated_at INTEGER
);
`)
	if err != nil {
		return err
	}
	// 为早于该列的旧库补列(CREATE TABLE IF NOT EXISTS 不会新增列);已存在则忽略错误。
	s.db.Exec(`ALTER TABLE sessions ADD COLUMN scene_res INTEGER`) // 实时地图:当前场景 res,供重启恢复
	s.db.Exec(`ALTER TABLE sessions ADD COLUMN home_room INTEGER`) // 家园室内房屋等级(选分层底图)
	s.db.Exec(`ALTER TABLE pets ADD COLUMN egg_groups TEXT`)
	// 身高/体重在当前形态取值范围内的百分位(0-100),写入时按 gamedata 计算并落列,
	// 供跨种族按「相对自身范围偏大/偏小」排序(见 buildOrder);范围缺失或旧库未回填时为
	// NULL(排序排末尾),清库重登后即补齐。
	s.db.Exec(`ALTER TABLE pets ADD COLUMN weight_pct REAL`)
	s.db.Exec(`ALTER TABLE pets ADD COLUMN height_pct REAL`)
	return nil
}

// SessionTTL 是持久化会话(密钥/账号归属)的有效期:超过此时长的连接不再复用,
// 兜底防止四元组被新连接复用时套用陈旧密钥(主校验仍是 gcp.ValidPlain 的明文自检)。
const SessionTTL = 24 * time.Hour

// SaveSessionKey 持久化某 GCP 连接(conn_id)的会话 AES 密钥,
// 供抓包服务异常重启后继续解密同一条仍存活的连接。account 列保持不变。
func (s *Store) SaveSessionKey(connID string, key []byte) error {
	_, err := s.db.Exec(`
INSERT INTO sessions(conn_id,key,updated_at) VALUES(?,?,?)
ON CONFLICT(conn_id) DO UPDATE SET key=excluded.key, updated_at=excluded.updated_at`,
		connID, key, time.Now().Unix())
	return err
}

// LoadKey 读取某连接近 SessionTTL 内更新过的会话密钥;无/过期/为空均返回 false。
// 实现 capture.KeyStore,供 Engine 在连接首次出现时预热密钥。
func (s *Store) LoadKey(connID string) ([]byte, bool) {
	var key []byte
	err := s.db.QueryRow(`SELECT key FROM sessions WHERE conn_id=? AND updated_at>=?`,
		connID, time.Now().Add(-SessionTTL).Unix()).Scan(&key)
	if err != nil || len(key) == 0 {
		return nil, false
	}
	return key, true
}

// SaveKey 实现 capture.KeyStore(SaveSessionKey 的忽略错误封装)。
func (s *Store) SaveKey(connID string, key []byte) { s.SaveSessionKey(connID, key) }

// SaveSessionAccount 持久化某连接的账号归属("UID:<user_id>"),
// 使重启后无需再次等到登录回包即可归属该连接解密出的消息。key 列保持不变。
func (s *Store) SaveSessionAccount(connID, account string) error {
	_, err := s.db.Exec(`
INSERT INTO sessions(conn_id,account,updated_at) VALUES(?,?,?)
ON CONFLICT(conn_id) DO UPDATE SET account=excluded.account, updated_at=excluded.updated_at`,
		connID, account, time.Now().Unix())
	return err
}

// SessionScene 是一个连接缓存的场景态:当前 scene_res 与家园房屋等级(非家园为 0)。
type SessionScene struct {
	Res  int32
	Room int32
}

// SaveSessionScene 持久化某连接当前所在的 scene_res_cfg_id 与家园房屋等级(实时地图页用)。
// 场景 res 只在进入/传送时下发,游戏中途不再重发,故须落盘以便抓包服务重启后恢复地图定位
// (移动包只带 scene_cfg_id,单靠它无法区分同 cfg 下的多个 res)。key/account 列保持不变。
func (s *Store) SaveSessionScene(connID string, res, room int32) error {
	_, err := s.db.Exec(`
INSERT INTO sessions(conn_id,scene_res,home_room,updated_at) VALUES(?,?,?,?)
ON CONFLICT(conn_id) DO UPDATE SET scene_res=excluded.scene_res, home_room=excluded.home_room, updated_at=excluded.updated_at`,
		connID, res, room, time.Now().Unix())
	return err
}

// LoadSessionScenes 读取近 SessionTTL 内的 conn_id→场景态映射(重启预热用)。
func (s *Store) LoadSessionScenes() (map[string]SessionScene, error) {
	rows, err := s.db.Query(`SELECT conn_id,scene_res,COALESCE(home_room,0) FROM sessions WHERE scene_res IS NOT NULL AND scene_res<>0 AND updated_at>=?`,
		time.Now().Add(-SessionTTL).Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]SessionScene{}
	for rows.Next() {
		var connID string
		var sc SessionScene
		if rows.Scan(&connID, &sc.Res, &sc.Room) == nil {
			out[connID] = sc
		}
	}
	return out, rows.Err()
}

// LoadSessionAccounts 读取近 SessionTTL 内的 conn_id→account 映射(重启预热用),
// 并顺带清理过期会话行,避免长期累积。仅返回 account 非空的连接。
func (s *Store) LoadSessionAccounts() (map[string]string, error) {
	cutoff := time.Now().Add(-SessionTTL).Unix()
	s.db.Exec(`DELETE FROM sessions WHERE updated_at<?`, cutoff)
	rows, err := s.db.Query(`SELECT conn_id,account FROM sessions WHERE account IS NOT NULL AND account<>''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var connID, account string
		if rows.Scan(&connID, &account) == nil {
			out[connID] = account
		}
	}
	return out, rows.Err()
}

// AccountInfo 是一个账号的概要(供前端账号下拉)。
type AccountInfo struct {
	Account  string `json:"account"`
	Name     string `json:"name"`
	PetCount int    `json:"petCount"`
}

// UpsertAccount 登记/更新一个账号的展示名与活跃时间。
func (s *Store) UpsertAccount(account, name string) error {
	_, err := s.db.Exec(`
INSERT INTO accounts(account,name,updated_at) VALUES(?,?,?)
ON CONFLICT(account) DO UPDATE SET name=excluded.name, updated_at=excluded.updated_at`,
		account, name, time.Now().Unix())
	return err
}

// ListAccounts 返回已知账号(按最近活跃倒序),petCount 含零宠物账号(LEFT JOIN)。
func (s *Store) ListAccounts() ([]AccountInfo, error) {
	rows, err := s.db.Query(`
SELECT a.account, a.name, COUNT(p.gid)
FROM accounts a LEFT JOIN pets p ON p.account = a.account
GROUP BY a.account, a.name
ORDER BY a.updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AccountInfo
	for rows.Next() {
		var a AccountInfo
		if err := rows.Scan(&a.Account, &a.Name, &a.PetCount); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ReplacePetMedals 用一份登录快照替换本账号所有宠物拥有的奖牌(gid↔medal 多对多)。
func (sc *Scoped) ReplacePetMedals(owns []pet.MedalOwn) error {
	tx, err := sc.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM pet_medal WHERE account=?`, sc.account); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO pet_medal(account,gid,medal_id) VALUES(?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, o := range owns {
		if _, err = stmt.Exec(sc.account, o.Gid, o.MedalID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// BoxLayout 是一个盒子的槽位布局(30 格,gid=0 表示空)。
type BoxLayout struct {
	ID    int32             `json:"id"`
	Name  string            `json:"name"`
	Slots []uint32          `json:"slots"`           // 长 30,下标=格位(0 起),值=宠物 gid(0 空)
	Heads map[string]string `json:"heads,omitempty"` // gid(字符串)→小头像路径,供示意图渲染头像
}

// petHeads 批量读取本账号一组 gid 的小头像路径(image.head);空集或无图忽略。
func (sc *Scoped) petHeads(gids []uint32) map[string]string {
	if len(gids) == 0 {
		return nil
	}
	ph := make([]string, len(gids))
	args := make([]any, 0, len(gids)+1)
	args = append(args, sc.account)
	for i, g := range gids {
		ph[i] = "?"
		args = append(args, g)
	}
	rows, err := sc.db.Query(`SELECT gid,data FROM pets WHERE account=? AND gid IN (`+strings.Join(ph, ",")+`)`, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var gid uint32
		var data string
		if rows.Scan(&gid, &data) != nil {
			continue
		}
		var p pet.Pet
		if json.Unmarshal([]byte(data), &p) == nil && p.Image.Head != "" {
			out[strconv.FormatUint(uint64(gid), 10)] = p.Image.Head
		}
	}
	return out
}

// BoxLayouts 返回本账号全部盒子的槽位布局(按 box_id/展示位置升序),供前端盒子示意图。
// 盒子全集与盒名取自 pet_boxes 元数据(含空盒),占用格从 pet_box 填入;两表都缺时返回空。
// 无 pet_boxes 元数据(旧库尚未刷新)时回退按 pet_box 里出现过的盒号构造(仅有宠物的盒)。
func (sc *Scoped) BoxLayouts() []BoxLayout {
	m := map[int32]*BoxLayout{}
	ensure := func(id int32) *BoxLayout {
		bl := m[id]
		if bl == nil {
			bl = &BoxLayout{ID: id, Slots: make([]uint32, 30)}
			m[id] = bl
		}
		return bl
	}
	// 盒子全集 + 盒名(含空盒)
	if rows, err := sc.db.Query(`SELECT box_id, name FROM pet_boxes WHERE account=?`, sc.account); err == nil {
		for rows.Next() {
			var id int32
			var name string
			if rows.Scan(&id, &name) == nil {
				ensure(id).Name = name
			}
		}
		rows.Close()
	}
	// 占用格(旧库无元数据时,盒名回退用 pet_box.box_name)
	if rows, err := sc.db.Query(`SELECT box_id, slot, gid, box_name FROM pet_box WHERE account=?`, sc.account); err == nil {
		for rows.Next() {
			var boxID, slot int32
			var gid uint32
			var name string
			if rows.Scan(&boxID, &slot, &gid, &name) != nil {
				continue
			}
			bl := ensure(boxID)
			if bl.Name == "" && name != "" {
				bl.Name = name
			}
			if slot >= 0 && slot < 30 {
				bl.Slots[slot] = gid
			}
		}
		rows.Close()
	}
	order := make([]int32, 0, len(m))
	for id := range m {
		order = append(order, id)
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	out := make([]BoxLayout, 0, len(order))
	for _, id := range order {
		bl := m[id]
		var gids []uint32
		for _, g := range bl.Slots {
			if g != 0 {
				gids = append(gids, g)
			}
		}
		bl.Heads = sc.petHeads(gids)
		out = append(out, *bl)
	}
	return out
}

// TeamLayout 是大世界三支队伍的位置布局(18 格 = 3 队 × 6 位,下标=team_idx*6+pos)。
type TeamLayout struct {
	Slots []uint32          `json:"slots"`
	Heads map[string]string `json:"heads,omitempty"` // gid(字符串)→小头像路径
}

// TeamLayouts 返回本账号大世界队伍的 18 格布局(gid=0 表示空位)。
func (sc *Scoped) TeamLayouts() TeamLayout {
	tl := TeamLayout{Slots: make([]uint32, 18)}
	rows, err := sc.db.Query(`SELECT team_idx, pos, gid FROM pet_team WHERE account=?`, sc.account)
	if err != nil {
		return tl
	}
	defer rows.Close()
	for rows.Next() {
		var ti, pos int32
		var gid uint32
		if rows.Scan(&ti, &pos, &gid) == nil {
			if idx := ti*6 + pos; idx >= 0 && idx < 18 {
				tl.Slots[idx] = gid
			}
		}
	}
	var gids []uint32
	for _, g := range tl.Slots {
		if g != 0 {
			gids = append(gids, g)
		}
	}
	tl.Heads = sc.petHeads(gids)
	return tl
}

// medalsFor 读取本账号单只宠物拥有的奖牌 id 列表(升序),供 GetPet 注入。
func (sc *Scoped) medalsFor(gid uint32) []uint32 {
	rows, err := sc.db.Query(`SELECT medal_id FROM pet_medal WHERE account=? AND gid=? ORDER BY medal_id`, sc.account, gid)
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

// ReplacePetBoxes 用一份完整背包快照替换本账号所有宠物盒子位置(整体 DELETE + 批量插入)。
func (sc *Scoped) ReplacePetBoxes(entries []pet.BoxEntry) error {
	tx, err := sc.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM pet_box WHERE account=?`, sc.account); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO pet_box(account,gid,box_id,slot,box_name,mark) VALUES(?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, e := range entries {
		if _, err = stmt.Exec(sc.account, e.Gid, e.BoxID, e.Slot, e.BoxName, e.Mark); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ReplacePetBoxMetas 用一份完整盒子元数据快照替换本账号所有盒子(整体 DELETE + 批量插入)。
// 元数据含空盒,是盒名/数量/位置(box_id)的权威来源;登录/整理回包携带全量盒列表时刷新。
func (sc *Scoped) ReplacePetBoxMetas(metas []pet.BoxMeta) error {
	tx, err := sc.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM pet_boxes WHERE account=?`, sc.account); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO pet_boxes(account,box_id,name,mark,lock) VALUES(?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, mt := range metas {
		if _, err = stmt.Exec(sc.account, mt.BoxID, mt.Name, mt.Mark, mt.Lock); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpsertPetBoxMeta 增量更新单个盒子的元数据(解锁新盒 / 设标记 / 改名),不动其他盒子。
func (sc *Scoped) UpsertPetBoxMeta(meta pet.BoxMeta) error {
	_, err := sc.db.Exec(`INSERT OR REPLACE INTO pet_boxes(account,box_id,name,mark,lock) VALUES(?,?,?,?,?)`,
		sc.account, meta.BoxID, meta.Name, meta.Mark, meta.Lock)
	return err
}

// ApplyBoxMoves 增量应用盒位变更(box_pet_change):把每只宠物移到新(盒,格);
// 盒名/标记随盒不随宠,取自 pet_boxes 元数据(增量包不携带,移入空盒也能拿到盒名),
// 元数据缺失(旧库)才回退取该盒任一既有宠物行;宠物入盒即不在队伍,清除其队伍位置。
func (sc *Scoped) ApplyBoxMoves(entries []pet.BoxEntry) error {
	tx, err := sc.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	up, err := tx.Prepare(`INSERT OR REPLACE INTO pet_box(account,gid,box_id,slot,box_name,mark) VALUES(?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer up.Close()
	for _, e := range entries {
		var name string
		var mark int32
		if tx.QueryRow(`SELECT name,mark FROM pet_boxes WHERE account=? AND box_id=?`, sc.account, e.BoxID).Scan(&name, &mark) != nil {
			tx.QueryRow(`SELECT box_name,mark FROM pet_box WHERE account=? AND box_id=? AND gid<>? LIMIT 1`, sc.account, e.BoxID, e.Gid).Scan(&name, &mark)
		}
		if _, err = up.Exec(sc.account, e.Gid, e.BoxID, e.Slot, name, mark); err != nil {
			return err
		}
		if _, err = tx.Exec(`DELETE FROM pet_team WHERE account=? AND gid=?`, sc.account, e.Gid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// boxLocFor 读取本账号单只宠物的盒子位置(无则 nil),供 GetPet 注入。
// 盒号/格位取自 pet_box;盒名/标记优先取 pet_boxes 权威元数据(移入空盒也准),缺失才回退 pet_box。
func (sc *Scoped) boxLocFor(gid uint32) *pet.PetBoxLoc {
	var boxID, slot, mark int32
	var name string
	err := sc.db.QueryRow(`SELECT box_id,slot,box_name,mark FROM pet_box WHERE account=? AND gid=?`, sc.account, gid).Scan(&boxID, &slot, &name, &mark)
	if err != nil {
		return nil
	}
	var mName string
	var mMark int32
	if sc.db.QueryRow(`SELECT name,mark FROM pet_boxes WHERE account=? AND box_id=?`, sc.account, boxID).Scan(&mName, &mMark) == nil {
		name, mark = mName, mMark
	}
	return &pet.PetBoxLoc{BoxID: boxID, Slot: slot, BoxName: name, Mark: pet.MarkName(mark)}
}

// ReplacePetTeams 用一份大世界队伍快照替换本账号所有宠物队伍位置。
func (sc *Scoped) ReplacePetTeams(entries []pet.TeamEntry) error {
	tx, err := sc.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM pet_team WHERE account=?`, sc.account); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO pet_team(account,gid,team_idx,pos) VALUES(?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, e := range entries {
		if _, err = stmt.Exec(sc.account, e.Gid, e.TeamIdx, e.Pos); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// teamLocFor 读取本账号单只宠物的队伍位置(无则 nil),供 GetPet 注入。
func (sc *Scoped) teamLocFor(gid uint32) *pet.PetTeamLoc {
	var teamIdx, pos int32
	if sc.db.QueryRow(`SELECT team_idx,pos FROM pet_team WHERE account=? AND gid=?`, sc.account, gid).Scan(&teamIdx, &pos) != nil {
		return nil
	}
	return &pet.PetTeamLoc{TeamIdx: teamIdx, Pos: pos}
}

// UpsertPet 插入或更新本账号一只宠物，返回是否为新增(该账号此前无该 gid)。
func (sc *Scoped) UpsertPet(p *pet.Pet) (isNew bool, err error) {
	var one int
	err = sc.db.QueryRow(`SELECT 1 FROM pets WHERE account=? AND gid=?`, sc.account, p.Gid).Scan(&one)
	isNew = err == sql.ErrNoRows
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}

	// 计算身高/体重在当前形态范围内的百分位并落列(排序键);同时写入 data JSON(读取时会再刷新,无害)。
	pet.FillSizePercentile(sc.gd, p)
	weightPct, heightPct := nullPct(p.WeightPct), nullPct(p.HeightPct)

	data, _ := json.Marshal(p)
	types, _ := json.Marshal(p.Types)
	// 蛋组存组名 JSON 数组(与 types 同法),供 egg_groups LIKE '%"名"%' 过滤。
	eggNames := make([]string, 0, len(p.EggGroups))
	for _, g := range p.EggGroups {
		eggNames = append(eggNames, g.Name)
	}
	eggGroups, _ := json.Marshal(eggNames)
	_, err = sc.db.Exec(`
INSERT INTO pets(account,gid,conf_id,species,name,level,nature_id,nature,gender,types,
  height,weight,voice,talent_rank,medal,medal_id,partner_mark,speciality,speciality_id,
  catch_time,shiny,colorful,hp,attack,defense,sp_attack,sp_defense,speed,form,egg_groups,
  weight_pct,height_pct,data,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(account,gid) DO UPDATE SET
  conf_id=excluded.conf_id,species=excluded.species,name=excluded.name,level=excluded.level,
  nature_id=excluded.nature_id,nature=excluded.nature,gender=excluded.gender,types=excluded.types,
  height=excluded.height,weight=excluded.weight,voice=excluded.voice,talent_rank=excluded.talent_rank,
  medal=excluded.medal,medal_id=excluded.medal_id,partner_mark=excluded.partner_mark,
  speciality=excluded.speciality,speciality_id=excluded.speciality_id,catch_time=excluded.catch_time,
  shiny=excluded.shiny,colorful=excluded.colorful,hp=excluded.hp,attack=excluded.attack,defense=excluded.defense,
  sp_attack=excluded.sp_attack,sp_defense=excluded.sp_defense,speed=excluded.speed,form=excluded.form,
  egg_groups=excluded.egg_groups,weight_pct=excluded.weight_pct,height_pct=excluded.height_pct,
  data=excluded.data,updated_at=excluded.updated_at`,
		sc.account, p.Gid, p.ConfID, p.Species, p.Name, p.Level, p.NatureID, p.Nature, p.Gender, string(types),
		p.HeightM, p.WeightKg, p.Voice, p.TalentRank, p.Medal, p.WearMedalConfID, p.PartnerMark,
		p.Speciality, p.SpecialityID, p.CatchTime, b2i(p.Shiny), b2i(p.Colorful),
		p.HP.Value, p.Attack.Value, p.Defense.Value, p.SpAttack.Value, p.SpDefense.Value, p.Speed.Value,
		p.Form, string(eggGroups), weightPct, heightPct, string(data), time.Now().Unix())
	return isNew, err
}

// nullPct 把可空百分位指针转为可绑定的 sql.NullFloat64(nil→NULL)。
func nullPct(v *float64) sql.NullFloat64 {
	if v == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *v, Valid: true}
}

// RemovePet 删除本账号宠物，返回被删除的快照(若存在)。
func (sc *Scoped) RemovePet(gid uint32) (*pet.Pet, error) {
	p, err := sc.GetPet(gid)
	if err != nil || p == nil {
		return nil, err
	}
	_, err = sc.db.Exec(`DELETE FROM pets WHERE account=? AND gid=?`, sc.account, gid)
	// 一并清掉盒位/队位/奖牌关联,否则示意图仍把该格当作占用(灰底可点、却无头像)。
	sc.db.Exec(`DELETE FROM pet_box WHERE account=? AND gid=?`, sc.account, gid)
	sc.db.Exec(`DELETE FROM pet_team WHERE account=? AND gid=?`, sc.account, gid)
	sc.db.Exec(`DELETE FROM pet_medal WHERE account=? AND gid=?`, sc.account, gid)
	return p, err
}

// PruneMissingPets 删除本账号中 gid 不在 keep 集合内的宠物(及其盒位/队位/奖牌关联),
// 返回被删除的 gid。用于分页宠物列表全量下发后对账:玩家在别处放生/赠送的宠物不会出现在
// 快照里,若不清除则会以"位置待同步"残留在列表(登录快照只做增改、从不删)。
// before 为本轮对账开始时刻:仅清除此前就已存在(updated_at < before)的宠物,从而放过对账
// 期间刚捕获/更新(updated_at≥before)却未落入快照的宠物,避免误删刚入库的新宠。
func (sc *Scoped) PruneMissingPets(keep map[uint32]bool, before int64) ([]uint32, error) {
	// 先收齐待删 gid 再执行删除:SetMaxOpenConns(1) 下遍历结果集时嵌套写会死锁。
	rows, err := sc.db.Query(`SELECT gid FROM pets WHERE account=? AND (updated_at IS NULL OR updated_at < ?)`, sc.account, before)
	if err != nil {
		return nil, err
	}
	var stale []uint32
	for rows.Next() {
		var g uint32
		if rows.Scan(&g) == nil && !keep[g] {
			stale = append(stale, g)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, g := range stale {
		sc.db.Exec(`DELETE FROM pets WHERE account=? AND gid=?`, sc.account, g)
		sc.db.Exec(`DELETE FROM pet_box WHERE account=? AND gid=?`, sc.account, g)
		sc.db.Exec(`DELETE FROM pet_team WHERE account=? AND gid=?`, sc.account, g)
		sc.db.Exec(`DELETE FROM pet_medal WHERE account=? AND gid=?`, sc.account, g)
	}
	return stale, nil
}

// GetPet 按 gid 返回本账号宠物。
func (sc *Scoped) GetPet(gid uint32) (*pet.Pet, error) {
	var data string
	err := sc.db.QueryRow(`SELECT data FROM pets WHERE account=? AND gid=?`, sc.account, gid).Scan(&data)
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
	p.Box = sc.boxLocFor(gid)
	p.Team = sc.teamLocFor(gid)
	if ms := sc.medalsFor(gid); ms != nil {
		p.MedalIDs = ms
	}
	return &p, nil
}

// AddEvent 写入本账号一条事件。
func (sc *Scoped) AddEvent(e *Event) error {
	// 注入盒位/队位(捕捉回包常同时携带落位,此时已 ApplyBoxMoves),使实时广播的事件即带位置。
	if e.Pet != nil {
		e.Pet.Box = sc.boxLocFor(e.Pet.Gid)
		e.Pet.Team = sc.teamLocFor(e.Pet.Gid)
	}
	data, _ := json.Marshal(e.Pet)
	res, err := sc.db.Exec(`INSERT INTO events(account,time,sub_kind,gid,species,nature,medal,shiny,data)
VALUES(?,?,?,?,?,?,?,?,?)`,
		sc.account, e.Time, e.SubKind, e.Gid,
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

// ClearEvents 清空本账号事件历史。
func (sc *Scoped) ClearEvents() error {
	_, err := sc.db.Exec(`DELETE FROM events WHERE account=?`, sc.account)
	return err
}

// CountEvents 返回本账号事件总数(即自上次清空以来获得的宠物数,失去事件不入库)。
func (sc *Scoped) CountEvents() (int, error) {
	var n int
	err := sc.db.QueryRow(`SELECT COUNT(*) FROM events WHERE account=?`, sc.account).Scan(&n)
	return n, err
}

// ListEvents 返回本账号最近事件(按时间倒序)。
func (sc *Scoped) ListEvents(limit, beforeID int) ([]*Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id,time,sub_kind,gid,data FROM events WHERE account=?`
	args := []any{sc.account}
	if beforeID > 0 {
		q += ` AND id < ?`
		args = append(args, beforeID)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := sc.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	var out []*Event
	for rows.Next() {
		var e Event
		var data string
		if err := rows.Scan(&e.ID, &e.Time, &e.SubKind, &e.Gid, &data); err != nil {
			rows.Close()
			return nil, err
		}
		var p pet.Pet
		if json.Unmarshal([]byte(data), &p) == nil {
			e.Pet = &p
		}
		out = append(out, &e)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close() // 先关闭结果集再发后续查询:SetMaxOpenConns(1) 下迭代中嵌套查询会死锁
	// 注入当前盒位/队位(与宠物列表一致,反映该宠物现在所处位置;已放生则为空)
	for _, e := range out {
		if e.Pet != nil {
			e.Pet.Box = sc.boxLocFor(e.Gid)
			e.Pet.Team = sc.teamLocFor(e.Gid)
		}
	}
	return out, nil
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
