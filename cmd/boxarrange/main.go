// boxarrange 算出「把宠物盒排成想要的顺序」需要哪些换位,构造与游戏客户端逐字节同构的
// 请求包,并按人工拖拽的节奏模拟发出。游戏里只能一只一只拖、或整盒按单个规则整理,
// 这里可以按多个键排(如 种类 → 等级降序),再拆成最少次数的两两交换。
//
// 默认只模拟不发送:全程不打开任何 socket,-hex 能看到每一步真正会发出去的字节。
// 加 -send 才会接管连接真发(见 internal/relay)。抓包侧始终是纯被动的。
//
// 建议按这个顺序试,每一步都零风险地排掉一类问题:
//
//	# 1. 自检:拿 pcap 里的真包回来,验证组出来的字节与客户端一致
//	go run ./cmd/boxarrange -selftest -pcap pcap/rocom-20260822-003405.pcap00
//	go run ./cmd/boxarrange -selftest -pcap pcap/rocom-20260822-15*.pcap00   # 轮转的多份连读
//	sudo go run ./cmd/boxarrange -selftest -iface wlo1                       # 边玩边核,不落盘
//
//	# 2. 模拟:看看计划是什么,要多少步、多久
//	go run ./cmd/boxarrange -db rocom.db -box 3,4 -sort species,level:desc -no-sleep
//
//	# 3. 只中转不注入:单独验「接管连接本身不会把游戏搞坏」,正常玩几分钟
//	sudo go run ./cmd/boxarrange -send -relay-only
//
//	# 4. 打一枪:只走第一步,回包核对通过再放开
//	sudo go run ./cmd/boxarrange -send -db rocom.db -box 3 -sort species -limit 1
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/whoisnian/rocom-capture/internal/petbox"
)

func main() {
	selftest := flag.Bool("selftest", false, "自检模式:从 pcap 取真实 0x1887 请求,与本工具的组包逐字节比对")
	pcapPath := flag.String("pcap", "", "自检用的 pcap 文件路径(可再跟多个,轮转出来的多份会当成一条流连读)")
	ifaceName := flag.String("iface", "", "自检改成在这块网卡上被动嗅探,边玩边核(需 root;不发包、不改规则)")
	// -iface 自检也用 -db:只读借 rocom-capture 落盘的会话密钥,好中途接上已建立的连接
	statEvery := flag.Duration("stat-every", time.Minute, "-iface 自检时多久报一次进度")
	dbPath := flag.String("db", "rocom.db", "SQLite 数据库路径(读当前盒位布局)")
	account := flag.String("account", "", "账号(库里只有一个账号时可省略)")
	boxFilter := flag.String("box", "", "只排这些盒号(逗号分隔;默认全部有宠物的盒)")
	sortKeys := flag.String("sort", "species,gid", "排序键,逗号分隔,可加 :desc(见 -sort-keys)")
	listKeys := flag.Bool("sort-keys", false, "列出可用的排序键后退出")
	interval := flag.Duration("interval", petbox.DefaultPacer.Interval, "每步基准间隔")
	jitterRatio := flag.Float64("jitter", petbox.DefaultPacer.Jitter, "间隔抖动比例 0~1")
	restEvery := flag.Int("rest-every", petbox.DefaultPacer.RestEvery, "每多少步歇一次(0=不歇)")
	rest := flag.Duration("rest", petbox.DefaultPacer.Rest, "每次歇多久")
	limit := flag.Int("limit", 0, "只走前 N 步(0=全部)")
	showHex := flag.Bool("hex", false, "打印每一步的完整上线字节(仅模拟模式)")
	noSleep := flag.Bool("no-sleep", false, "不真的等待,只打印时间线(仅模拟模式)")
	send := flag.Bool("send", false, "真发:接管游戏连接,按节奏把换位请求发给服务器")
	listen := flag.String("listen", ":4940", "-send 时中转监听的本地地址")
	upstream := flag.String("upstream", "", "-send 时强制上游地址(默认取 SO_ORIGINAL_DST)")
	yes := flag.Bool("yes", false, "-send 时跳过开始前的回车确认")
	relayOnly := flag.Bool("relay-only", false, "-send 时只接管转发、一条都不注入(单独验中转本身不会把游戏搞坏)")
	flag.Parse()

	opts := simOpts{
		db: *dbPath, account: *account, boxes: *boxFilter, sort: *sortKeys,
		pacer: petbox.Pacer{Interval: *interval, Jitter: *jitterRatio, RestEvery: *restEvery, Rest: *rest},
		limit: *limit, hex: *showHex, noSleep: *noSleep,
	}
	switch {
	case *listKeys:
		printSortKeys()
	case *selftest && *ifaceName != "":
		runSelftestLive(*ifaceName, *dbPath, *statEvery)
	case *selftest:
		if *pcapPath == "" {
			fail("自检需要 -pcap(离线)或 -iface(在线嗅探)")
		}
		// 通配展开的其余文件走位置参数:-pcap pcap/rocom-2026*.pcap00 会把第一份给 -pcap、
		// 剩下的落在这里。按文件名排序即时间顺序(抓包脚本用时间戳命名)。
		paths := append([]string{*pcapPath}, flag.Args()...)
		sort.Strings(paths)
		runSelftest(paths)
	case *send:
		runSend(opts, sendOpts{listen: *listen, upstream: *upstream, yes: *yes, relayOnly: *relayOnly})
	default:
		runSimulate(opts)
	}
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}

// sortCols 把外部可写的键名映射到 pets 表的列。只认白名单里的名字,拼进 ORDER BY 才安全。
var sortCols = map[string]string{
	"species": "p.species", "name": "p.name", "level": "p.level",
	"conf_id": "p.conf_id", "base_conf_id": "p.base_conf_id", "form": "p.form",
	"nature": "p.nature_id", "gender": "p.gender", "types": "p.types",
	"height": "p.height", "weight": "p.weight",
	"height_pct": "p.height_pct", "weight_pct": "p.weight_pct",
	"voice": "p.voice", "talent_rank": "p.talent_rank",
	"medal": "p.medal_id", "speciality": "p.speciality_id",
	"catch_time": "p.catch_time", "shiny": "p.shiny", "colorful": "p.colorful",
	"hp": "p.hp", "attack": "p.attack", "defense": "p.defense",
	"sp_attack": "p.sp_attack", "sp_defense": "p.sp_defense", "speed": "p.speed",
	"gid": "b.gid",
}

func printSortKeys() {
	keys := make([]string, 0, len(sortCols))
	for k := range sortCols {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Println("可用排序键(加 :desc 倒序;逗号分隔多个键,先写的优先):")
	for i, k := range keys {
		fmt.Printf("  %-14s", k)
		if i%4 == 3 {
			fmt.Println()
		}
	}
	fmt.Println()
}

// orderBy 把 "species,level:desc" 编成 ORDER BY 子句。
// 末尾恒加 b.gid 兜底,保证顺序确定(否则等值行的次序由 SQLite 自行决定,每次跑都可能不同)。
func orderBy(spec string) (clause string, pretty []string, err error) {
	var parts []string
	for _, item := range strings.Split(spec, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key, dir, _ := strings.Cut(item, ":")
		col, ok := sortCols[key]
		if !ok {
			return "", nil, fmt.Errorf("未知排序键 %q(-sort-keys 看全部)", key)
		}
		switch strings.ToLower(dir) {
		case "", "asc":
			parts, pretty = append(parts, col+" ASC"), append(pretty, key)
		case "desc":
			parts, pretty = append(parts, col+" DESC"), append(pretty, key+":desc")
		default:
			return "", nil, fmt.Errorf("排序键 %q 的方向只能是 asc/desc", item)
		}
	}
	if len(parts) == 0 {
		return "", nil, fmt.Errorf("-sort 不能为空")
	}
	// (p.gid IS NULL) 放最前:pets 表里没有的宠物(库还没抓全)统一排到末尾,不参与键比较。
	return "(p.gid IS NULL), " + strings.Join(parts, ", ") + ", b.gid", pretty, nil
}
