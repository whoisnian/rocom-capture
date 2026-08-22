package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/whoisnian/rocom-capture/internal/petbox"
)

type simOpts struct {
	db      string
	account string
	boxes   string
	sort    string
	pacer   petbox.Pacer
	limit   int
	hex     bool
	noSleep bool
}

// petInfo 只为打印好看:名字取昵称,没起过名就用种类名。
type petInfo struct {
	species string
	name    string
	level   int
}

func (p petInfo) label(gid uint32) string {
	n := p.name
	if n == "" {
		n = p.species
	}
	if n == "" {
		n = "?"
	}
	return fmt.Sprintf("%s Lv%d #%d", n, p.level, gid)
}

// plan 是一次排布的全部输入与结果。模拟(runSimulate)与实发(runSend)共用。
type plan struct {
	account string
	slots   []petbox.Slot
	swaps   []petbox.Swap
	info    map[uint32]petInfo
	pretty  []string
}

func buildPlan(o simOpts) (*plan, error) {
	order, pretty, err := orderBy(o.sort)
	if err != nil {
		return nil, err
	}
	db, err := openReadOnly(o.db)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	defer db.Close()

	account := o.account
	if account == "" {
		if account, err = soleAccount(db); err != nil {
			return nil, err
		}
	}
	boxes, err := parseBoxes(o.boxes)
	if err != nil {
		return nil, err
	}
	slots, err := currentSlots(db, account, boxes)
	if err != nil {
		return nil, fmt.Errorf("读当前盒位失败: %w", err)
	}
	if len(slots) == 0 {
		return nil, noSlotsErr(db, account, boxes)
	}
	want, err := targetOrder(db, account, boxes, order)
	if err != nil {
		return nil, fmt.Errorf("算目标顺序失败: %w", err)
	}
	info, err := petLabels(db, account)
	if err != nil {
		return nil, fmt.Errorf("读宠物名称失败: %w", err)
	}
	swaps, err := petbox.Plan(slots, want)
	if err != nil {
		return nil, fmt.Errorf("排布计划失败: %w", err)
	}
	return &plan{account: account, slots: slots, swaps: swaps, info: info, pretty: pretty}, nil
}

// header 打印账号/盒子/排序/交换次数几行,两种模式一致。
func (p *plan) header(pacer petbox.Pacer) {
	fmt.Printf("账号 %s · 盒 %s · %d 只宠物\n", p.account, joinInts(usedBoxes(p.slots)), len(p.slots))
	fmt.Printf("排序 %s\n", strings.Join(p.pretty, " → "))
	if len(p.swaps) == 0 {
		fmt.Println("已经是目标顺序,不需要任何操作。")
		return
	}
	fmt.Printf("需要 %d 次交换(该目标下的最少次数),按当前节奏预计 %s\n\n",
		len(p.swaps), round(pacer.Estimate(len(p.swaps))))
}

// step 打印一步的描述(不含换行后面的等待/结果)。
func (p *plan) step(i, total, width int, sw petbox.Swap, tail string) {
	fmt.Printf("[%*d/%d] 盒%-2d 格%-2d %s ↔ 盒%-2d 格%-2d %s  %s\n",
		width, i+1, total,
		sw.From.Box, sw.From.Pos, padCells(p.info[sw.From.Gid].label(sw.From.Gid), 20),
		sw.To.Box, sw.To.Pos, padCells(p.info[sw.To.Gid].label(sw.To.Gid), 20), tail)
}

func runSimulate(o simOpts) {
	p, err := buildPlan(o)
	if err != nil {
		fail("%v", err)
	}
	p.header(o.pacer)
	if len(p.swaps) == 0 {
		return
	}

	// 会话密钥只影响密文内容:模拟不发包,用随机值就够,组包结构与真包一致。
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		fail("生成密钥失败: %v", err)
	}
	fmt.Println("注:只模拟不发送 —— 会话密钥用的随机值,包序号从 1 起,请求外壳用的是默认模板。")
	fmt.Println("    要真发请加 -send(见 -h),那边会接管连接并接着真实序号往下走。")
	fmt.Println()

	steps := len(p.swaps)
	if o.limit > 0 && o.limit < steps {
		steps = o.limit
	}
	width := len(strconv.Itoa(steps))
	start := time.Now()
	for i, sw := range p.swaps[:steps] {
		wire, plain, err := petbox.Build(key, petbox.DefaultTemplate, uint32(i+1), uint32(i+1), sw)
		if err != nil {
			fail("第 %d 步组包失败: %v", i+1, err)
		}
		p.step(i, len(p.swaps), width, sw, fmt.Sprintf("明文 %dB / 上线 %dB", len(plain), len(wire)))
		if o.hex {
			fmt.Printf("         %s\n", hex.EncodeToString(wire))
		}
		if i+1 == steps {
			break
		}
		d := o.pacer.Next(i + 1)
		fmt.Printf("         等 %s\n", round(d))
		if !o.noSleep {
			time.Sleep(d)
		}
	}
	fmt.Printf("\n模拟完成:%d/%d 步", steps, len(p.swaps))
	if o.noSleep {
		fmt.Println("(-no-sleep,没有真的等待)")
	} else {
		fmt.Printf(",实际耗时 %s\n", round(time.Since(start)))
	}
}

// openReadOnly 以只读方式打开库:本工具只看不改,顺手也避免与正在跑的抓包服务抢写。
func openReadOnly(path string) (*sql.DB, error) {
	return sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
}

// noSlotsErr 在查不到盒位时给出**能直接照做**的提示。
// 账号 id 在库里带 UID: 前缀,漏了前缀是最容易踩的坑,而「库是空的?」这种提示只会把人带偏。
func noSlotsErr(db *sql.DB, account string, boxes []int32) error {
	all, err := accountsWithBoxes(db)
	if err != nil || len(all) == 0 {
		return fmt.Errorf("库里没有任何盒位数据 —— 先让 rocom-capture 抓一次登录并打开宠物仓库")
	}
	known := false
	for _, a := range all {
		if a == account {
			known = true
		}
	}
	if !known {
		return fmt.Errorf("库里没有账号 %q。现有账号:%s(注意 id 带 UID: 前缀)",
			account, strings.Join(all, "、"))
	}
	if len(boxes) > 0 {
		return fmt.Errorf("账号 %s 的盒 %s 里没有宠物;换个盒号,或去掉 -box 排全部",
			account, joinInts(boxes))
	}
	return fmt.Errorf("账号 %s 名下一只宠物都没有 —— 先让 rocom-capture 抓一次打开宠物仓库的全量下发", account)
}

func accountsWithBoxes(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT DISTINCT account FROM pet_box ORDER BY account`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if rows.Scan(&a) == nil {
			out = append(out, a)
		}
	}
	return out, rows.Err()
}

func soleAccount(db *sql.DB) (string, error) {
	all, err := accountsWithBoxes(db)
	if err != nil {
		return "", err
	}
	switch len(all) {
	case 0:
		return "", fmt.Errorf("库里没有任何盒位数据")
	case 1:
		return all[0], nil
	default:
		return "", fmt.Errorf("库里有多个账号(%s),用 -account 指定", strings.Join(all, ", "))
	}
}

func parseBoxes(spec string) ([]int32, error) {
	var out []int32
	for _, s := range strings.Split(spec, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("盒号 %q 不合法", s)
		}
		out = append(out, int32(n))
	}
	return out, nil
}

// boxWhere 拼出盒号过滤条件与参数(为空表示不过滤)。
func boxWhere(account string, boxes []int32) (string, []any) {
	args := []any{account}
	if len(boxes) == 0 {
		return "", args
	}
	ph := make([]string, len(boxes))
	for i, b := range boxes {
		ph[i], args = "?", append(args, b)
	}
	return " AND b.box_id IN (" + strings.Join(ph, ",") + ")", args
}

// currentSlots 读当前已占用的格位,按盒号/格号升序。pet_box.slot 是 0 起,协议里的 pos 是 1 起。
func currentSlots(db *sql.DB, account string, boxes []int32) ([]petbox.Slot, error) {
	where, args := boxWhere(account, boxes)
	rows, err := db.Query(`SELECT b.box_id, b.slot, b.gid FROM pet_box b
		WHERE b.account=?`+where+` AND b.gid<>0 ORDER BY b.box_id, b.slot`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []petbox.Slot
	for rows.Next() {
		var box, slot int32
		var gid uint32
		if err := rows.Scan(&box, &slot, &gid); err != nil {
			return nil, err
		}
		out = append(out, petbox.Slot{Gid: gid, Box: box, Pos: slot + 1})
	}
	return out, rows.Err()
}

// targetOrder 按排序键给出这批宠物应有的先后顺序(与 currentSlots 是同一批 gid)。
func targetOrder(db *sql.DB, account string, boxes []int32, order string) ([]uint32, error) {
	where, args := boxWhere(account, boxes)
	rows, err := db.Query(`SELECT b.gid FROM pet_box b
		LEFT JOIN pets p ON p.account=b.account AND p.gid=b.gid
		WHERE b.account=?`+where+` AND b.gid<>0 ORDER BY `+order, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uint32
	for rows.Next() {
		var gid uint32
		if err := rows.Scan(&gid); err != nil {
			return nil, err
		}
		out = append(out, gid)
	}
	return out, rows.Err()
}

func petLabels(db *sql.DB, account string) (map[uint32]petInfo, error) {
	rows, err := db.Query(`SELECT gid, COALESCE(species,''), COALESCE(name,''), COALESCE(level,0)
		FROM pets WHERE account=?`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uint32]petInfo{}
	for rows.Next() {
		var gid uint32
		var p petInfo
		if err := rows.Scan(&gid, &p.species, &p.name, &p.level); err != nil {
			return nil, err
		}
		out[gid] = p
	}
	return out, rows.Err()
}

func usedBoxes(slots []petbox.Slot) []int32 {
	seen := map[int32]bool{}
	var out []int32
	for _, s := range slots {
		if !seen[s.Box] {
			seen[s.Box], out = true, append(out, s.Box)
		}
	}
	return out
}

func joinInts(v []int32) string {
	parts := make([]string, len(v))
	for i, n := range v {
		parts[i] = strconv.Itoa(int(n))
	}
	return strings.Join(parts, ",")
}

// round 把时长收到 0.1 秒,免得打印出一串纳秒。
func round(d time.Duration) time.Duration { return d.Round(100 * time.Millisecond) }

// padCells 按终端显示宽度左对齐补空格。fmt 的 %-Ns 按字节数补,一个汉字算三格,列会歪。
func padCells(s string, w int) string {
	n := 0
	for _, r := range s {
		if r >= 0x1100 && (r <= 0x115f || // 韩文字母
			(r >= 0x2e80 && r <= 0xa4cf) || // CJK 部首 ~ 彝文
			(r >= 0xac00 && r <= 0xd7a3) || // 韩文音节
			(r >= 0xf900 && r <= 0xfaff) || // CJK 兼容
			(r >= 0xfe30 && r <= 0xfe6f) || // 竖排/小型标点
			(r >= 0xff00 && r <= 0xff60) || // 全角
			(r >= 0xffe0 && r <= 0xffe6)) {
			n += 2
		} else {
			n++
		}
	}
	if n >= w {
		return s
	}
	return s + strings.Repeat(" ", w-n)
}
