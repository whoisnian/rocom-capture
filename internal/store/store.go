// Package store 用 SQLite(纯 Go 驱动)持久化宠物当前状态与事件历史,并支持筛选查询。
// 文件按领域划分:session(连接会话缓存)/ account / pet / location(盒子·队伍·奖牌)/
// event / star(眠枭之星)/ query(筛选查询)。
package store

import (
	"database/sql"
	"net/url"
	"runtime"

	_ "modernc.org/sqlite"

	"github.com/whoisnian/rocom-capture/internal/gamedata"
)

// Store 封装 SQLite 连接。跨账号操作(建表/accounts/sessions/star 表)挂在此。
// gd 用于在写入时把身高/体重换算成形态内百分位并落列,支撑跨种族的百分位排序。
//
// 读写分两个连接池(见 New):db 只写(单连接串行),rdb 只读(多连接并发)。
// 约定:Exec/Begin 走 db,Query/QueryRow 走 rdb。
type Store struct {
	db  *sql.DB
	rdb *sql.DB
	gd  *gamedata.DB
}

// Scoped 是绑定了某个 account 的 Store 视图:所有按账号隔离的读写都经它进行,
// account 由 For 注入,方法内部不再显式接收 account,避免漏传导致跨账号串数据。
type Scoped struct {
	db      *sql.DB
	rdb     *sql.DB
	gd      *gamedata.DB
	account string
}

// For 返回绑定指定 account 的视图。
func (s *Store) For(account string) *Scoped {
	return &Scoped{db: s.db, rdb: s.rdb, gd: s.gd, account: account}
}

// maxReadConns 是只读池的连接数上限:读多是 CPU 密集(扫行 + JSON 编解码),开到超过核数
// 只会徒增争用。网关一类弱机核少,按核数自适应即可。
func maxReadConns() int {
	return min(max(runtime.NumCPU(), 2), 8)
}

// dsn 把库路径拼成带 pragma 的 file: URI。pragma 必须走 DSN 而不能事后 db.Exec:
// database/sql 的 Exec 只落在池里某一条连接上,多连接的池里其余连接拿不到设置。
func dsn(path string, pragmas ...string) string {
	q := url.Values{}
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}
	// "file:rocom.db" / "file:/var/lib/rocom.db",相对与绝对路径都成立。
	return "file:" + (&url.URL{Path: path}).EscapedPath() + "?" + q.Encode()
}

// New 打开(或创建)数据库并建表。gd 供写入时计算身高/体重百分位排序列。
func New(path string, gd *gamedata.DB) (*Store, error) {
	// 性能:默认 rollback 日志 + synchronous=FULL 会对每次自动提交 fsync,登录后全量宠物分页
	// 逐只 UpsertPet(数百次独立提交)时整轮拖到近 10s,处理速度赶不上抓包到达速度而积压。
	// 改 WAL + synchronous=NORMAL:提交不再逐次 fsync(仅 checkpoint 时落盘),被动抓包库
	// 即便宕机最多丢尾部若干条、可经下次登录快照重建,该取舍安全。busy_timeout 兜底。
	pragmas := []string{"busy_timeout(5000)", "journal_mode(WAL)", "synchronous(NORMAL)"}
	db, err := sql.Open("sqlite", dsn(path, pragmas...))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite 写入串行化,避免 database is locked

	// 读另开一个池:此前读写共用那一条连接,Web 端同时发来的几个 API 只能排队,墙钟时间成了
	// 各自耗时之和(实测宠物列表页四个请求 13+50+62+230ms 串成 330ms)。WAL 下读者之间、
	// 读者与写者之间都不互斥,故读放开并发;写仍只有一条连接,原先的写串行保证不变。
	// 只读连接始终处于 autocommit,每条语句各取一次 WAL 快照,读得到写者已提交的最新数据。
	rdb, err := sql.Open("sqlite", dsn(path, pragmas...))
	if err != nil {
		db.Close()
		return nil, err
	}
	rdb.SetMaxOpenConns(maxReadConns())

	s := &Store{db: db, rdb: rdb, gd: gd}
	if err := s.initSchema(); err != nil {
		db.Close()
		rdb.Close()
		return nil, err
	}
	return s, nil
}

// initSchema 建表建索引(幂等,每次启动都跑一遍)。
// 库结构只有这一处定义:早先散在下面的一串 ALTER 已并回各自的 CREATE TABLE,不再做旧库升级
// ——版本对不上就删掉 rocom.db 重启,数据在下次登录的全量快照里重建。
func (s *Store) initSchema() error {
	_, err := s.db.Exec(`
-- 宠物当前状态。列大多是 data(整只宠物的 JSON)里挑出来供筛选/排序用的冗余投影。
-- base_conf_id 是当前形态的 petbase(进化后随之变化):与 conf_id、shiny 一起够经 gamedata
-- 内存查表得出头像,盒子示意图取几百只头像时便无需逐条解 data(见 petHeads)。
-- *_pct 是身高/体重在当前形态取值范围内的百分位(0-100),写入时按 gamedata 算,供跨种族按
-- 「相对自身范围偏大/偏小」排序(见 buildOrder);gamedata 缺该形态范围时为 NULL,排序排末尾。
CREATE TABLE IF NOT EXISTS pets (
  account TEXT NOT NULL,
  gid INTEGER,
  conf_id INTEGER, base_conf_id INTEGER, species TEXT, name TEXT, level INTEGER,
  nature_id INTEGER, nature TEXT, gender TEXT, types TEXT,
  height REAL, weight REAL, height_pct REAL, weight_pct REAL, voice INTEGER,
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

-- 获得宠物的事件历史(失去不入库)。
CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  account TEXT NOT NULL,
  time INTEGER, sub_kind TEXT, gid INTEGER,
  species TEXT, nature TEXT, medal TEXT, shiny INTEGER,
  data TEXT
);
CREATE INDEX IF NOT EXISTS idx_events_account_time ON events(account, time);

-- 宠物所在位置:pet_box 是「哪只宠物在哪个盒的哪一格」,pet_boxes 是盒子本身的元数据
-- (含空盒,是盒名/数量的权威来源);在队伍里的宠物不在盒子里,二者互斥。
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
-- 宠物拥有过的奖牌(多对多;佩戴中的那枚另存在 pets.medal_id)。
CREATE TABLE IF NOT EXISTS pet_medal (
  account TEXT NOT NULL, gid INTEGER, medal_id INTEGER,
  PRIMARY KEY(account, gid, medal_id)
);

CREATE TABLE IF NOT EXISTS accounts (
  account TEXT PRIMARY KEY, name TEXT, updated_at INTEGER
);
-- 连接会话:key 是会话密钥(供重启后对存活连接续解,见 docs/architecture.md 3),
-- 其余几列是实时地图重启回显所需的现场(当前场景 / 家园房屋等级 / 所在区域)。
CREATE TABLE IF NOT EXISTS sessions (
  conn_id TEXT PRIMARY KEY, key BLOB, account TEXT, updated_at INTEGER,
  scene_res INTEGER, home_room INTEGER, areas TEXT
);

-- 精灵蛋(背包物品 gid 为键)。表里只有背包现状,破壳/送人的行直接删。
-- parents 是收蛋那一刻的双亲快照 JSON:亲本可能被放生/赠送,蛋上的双亲信息不应随之消失,
-- 故存快照而非引用 pets(见 docs/eggs.md)。seq 是背包里的原始次序(服务器下发顺序)。
CREATE TABLE IF NOT EXISTS eggs (
  account TEXT NOT NULL, gid INTEGER,
  item_id INTEGER, conf_id INTEGER, name TEXT, species TEXT,
  height REAL, weight REAL, height_pct REAL, weight_pct REAL,
  src INTEGER, hatching INTEGER, obtained_at INTEGER,
  seq INTEGER, parents TEXT, first_seen INTEGER, updated_at INTEGER, data TEXT,
  PRIMARY KEY(account, gid)
);
-- 孵蛋器槽位:权威顺序来自 PetBackpackInfo.egg_gid / 0x0312 的 egg_gid[],数组下标即槽位(1 起)。
-- 客户端 GetPlayerBackpackEggInfo 保持原序、UMG_PetHatching 直接按下标填九宫格,蛋自己的
-- start_hatch_time 排不出这个顺序(同秒入孵的几颗尤其,见 docs/eggs.md)。每份权威快照整表替换。
CREATE TABLE IF NOT EXISTS egg_hatch_slot (
  account TEXT NOT NULL, gid INTEGER, slot INTEGER,
  PRIMARY KEY(account, gid)
);

-- 眠枭之星的收集状态(按账号、按刷新点)。1=未收集(收到过该点的 NPC 实体),2=已收集(走近了却
-- 没有实体——已收集的星星服务器不刷,见 docs/map.md 4)。没有行 = 尚未确认(前端照常显示)。
CREATE TABLE IF NOT EXISTS star_state (
  account TEXT NOT NULL, refresh_id INTEGER,
  state INTEGER, updated_at INTEGER,
  PRIMARY KEY(account, refresh_id)
);
-- 服务器口径的按区域收集进度(进场景包下发);区域收满即可整片隐藏,无需逐点走到。
CREATE TABLE IF NOT EXISTS star_zone (
  account TEXT NOT NULL, camp INTEGER, npc_id INTEGER,
  got INTEGER, total INTEGER, updated_at INTEGER,
  PRIMARY KEY(account, camp, npc_id)
);

-- 稀兽花种(实时地图页的花种图层,见 docs/map.md 8)。一行 = 大世界上当前开着的一朵花:
-- 描述列(cfg_id/star/petbase/content_id/end_ts…)来自花种列表(0x0375),检测列(state/glass)
-- 来自逐朵查询(0x0338),两边各写各的。obj_id 是花实体的对象 id,**超出 int64 范围**故存 TEXT;
-- 花刷新后 obj_id 变,旧行在下一次列表里被删掉,新行的 state 归 0(未检测)。
-- 里面那只精灵的等级不存列:它由 (star, spec_id) 查表算出(gamedata.FlowerLevel),不是下发字段。
CREATE TABLE IF NOT EXISTS flower_seed (
  account TEXT NOT NULL, obj_id TEXT,
  cfg_id INTEGER, star INTEGER, petbase INTEGER, content_id INTEGER, spec_id INTEGER,
  end_ts INTEGER, state INTEGER, glass TEXT, updated_at INTEGER,
  PRIMARY KEY(account, obj_id)
);

-- 涂地(实时地图页的覆盖图层,见 docs/map.md 7):「玩家 ↔ 已下发的野生宠」之间那条走廊
-- 扫过的格子各记一位,cells 是 w*h 的位图(每字节 8 格,低位在前),按账号 + 场景 + 分层各存一张。
CREATE TABLE IF NOT EXISTS paint (
  account TEXT NOT NULL, res INTEGER, layer INTEGER,
  w INTEGER, h INTEGER, cells BLOB, updated_at INTEGER,
  PRIMARY KEY(account, res, layer)
);
`)
	return err
}

// execBatch 在一个事务里对每组参数执行同一条语句(upsert 批量写入用)。
func execBatch(db *sql.DB, query string, rows [][]any) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(query)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, r := range rows {
		if _, err := stmt.Exec(r...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// replaceAll 在一个事务里清空本账号在 table 的所有行,再按 rows 批量插入(全量快照替换用)。
func (sc *Scoped) replaceAll(table, insertSQL string, rows [][]any) error {
	tx, err := sc.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM `+table+` WHERE account=?`, sc.account); err != nil {
		return err
	}
	stmt, err := tx.Prepare(insertSQL)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, r := range rows {
		if _, err = stmt.Exec(r...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
