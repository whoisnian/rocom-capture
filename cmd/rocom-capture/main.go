package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/whoisnian/rocom-capture/internal/capture"
	"github.com/whoisnian/rocom-capture/internal/gamedata"
	"github.com/whoisnian/rocom-capture/internal/gcp"
	"github.com/whoisnian/rocom-capture/internal/pb"
	"github.com/whoisnian/rocom-capture/internal/pet"
	"github.com/whoisnian/rocom-capture/internal/server"
	"github.com/whoisnian/rocom-capture/internal/store"
)

func main() {
	pcapPath := flag.String("pcap", "", "离线 pcap 文件路径(回放模式)")
	iface := flag.String("iface", "", "实时抓包网卡名")
	port := flag.Int("port", 8195, "游戏服务器端口")
	addr := flag.String("addr", ":4939", "Web 服务监听地址")
	dbPath := flag.String("db", "rocom.db", "SQLite 数据库路径")
	flag.Parse()

	db, err := gamedata.Load()
	if err != nil {
		log.Fatalf("加载名称库失败: %v", err)
	}
	st, err := store.New(*dbPath)
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	hub := server.NewHub()
	srv := server.New(st, hub, db)
	eng := capture.NewEngine(*port)

	go consume(eng, st, db, srv)

	go func() {
		log.Printf("Web 界面: http://localhost%s", *addr)
		if err := http.ListenAndServe(*addr, srv.Handler()); err != nil {
			log.Fatalf("HTTP 服务失败: %v", err)
		}
	}()

	if *pcapPath != "" {
		log.Printf("离线回放: %s", *pcapPath)
		if err := eng.RunOffline(*pcapPath); err != nil {
			log.Fatalf("回放失败: %v", err)
		}
		accs, _ := st.ListAccounts()
		n := 0
		for _, a := range accs {
			n += a.PetCount
		}
		log.Printf("回放完成，%d 个账号共宠物 %d 只。Web 服务保持运行(Ctrl-C 退出)", len(accs), n)
		if d := eng.NoKeyDropped(); d > 0 {
			log.Printf("提示: %d 个数据包因尚无会话密钥被丢弃(抓包晚于密钥协商时属正常)", d)
		}
		select {}
	} else if *iface != "" {
		log.Printf("实时抓包: 网卡=%s 端口=%d", *iface, *port)
		if err := eng.RunLive(*iface); err != nil {
			log.Fatalf("抓包失败(需 root): %v", err)
		}
	} else {
		log.Println("用法: -pcap <文件> 或 -iface <网卡>")
	}
}

// consume 消费解密后的消息流：更新宠物库、产生事件、广播实时消息。
func consume(eng *capture.Engine, st *store.Store, db *gamedata.DB, srv *server.Server) {
	// add_time 早于此刻的宠物视为初始仓库快照，不产生“获得”事件；
	// 服务运行期间新捕捉的宠物 add_time≈当前，才推事件。
	const grace = 120
	startTS := time.Now().Unix() - grace

	// connAccount: GCP 连接(connID)→账号("role:"+user_id)。同一客户端 IP 可能同时跑多个
	// 账号(不同设备经 NAT 同 IP、或不同游戏服),故按 user_id 而非 IP 归属:抓到某连接的
	// LOGIN_RSP 时解析 user_id 建映射。登录回包自身也带背包/队伍/奖牌快照,须先登记再归属。
	connAccount := map[string]string{}

	for m := range eng.Out {
		// 登录回包:解析 user_id → 账号并登记 connID 映射(必须在下面 resolve acc 之前)。
		if m.Direction == gcp.S2C && m.Opcode == pet.OpLoginRsp {
			if id, name, ok := pet.ParseLoginAccount(m.AppBody); ok {
				acc := "role:" + strconv.FormatUint(id, 10)
				nick := name
				if nick == "" {
					nick = "?"
				}
				if connAccount[m.Session] != acc { // 同一登录会重复下发,仅首次记日志
					log.Printf("用户 %d(%s) 登录成功 [%s]", id, nick, m.Session)
				}
				connAccount[m.Session] = acc
				if name == "" {
					name = acc
				}
				st.UpsertAccount(acc, name)
			}
		}
		acc := connAccount[m.Session]

		// debug 页面：广播所有应用层消息(带来源账号,便于多账号排查;account 传 "" 不参与前端过滤)
		srv.Hub().Broadcast("debug", "", map[string]any{
			"time":    m.Time.Unix(),
			"dir":     m.Direction.String(),
			"opcode":  fmt.Sprintf("0x%04x", m.Opcode),
			"name":    srv.OpcodeName(m.Opcode),
			"account": acc,
		})

		if acc == "" {
			continue // 尚未见到该连接的登录(无法归属 user_id),丢弃
		}
		sc := st.For(acc)

		// 盒子布局：登录数据(0x0102)或盒子操作回包携带完整背包 PetBackpackInfo，
		// 解出 gid->(盒子,格位) 全量快照存入 pet_box,读取宠物时 JOIN 注入位置。
		if m.Direction == gcp.S2C && pet.CarriesTeam(m.Opcode) {
			updated := false
			var focusGid uint32 // 客户端刚调整位置的宠物,推给前端自动切页选中
			var focusBox int32  // 该宠物移动后所在盒子,供前端切换盒子示意图
			// 全量背包快照(登录/整理/设置回包):整体替换盒位
			if pet.CarriesBackpack(m.Opcode) {
				if entries := pet.ParseBackpack(m.AppBody); len(entries) > 0 {
					updated = sc.ReplacePetBoxes(entries) == nil || updated
				}
			}
			// 大世界队伍快照(登录/队伍变更/盒子操作回包常一并刷新):整体替换队位
			if teams := pet.ParseTeams(m.AppBody); len(teams) > 0 {
				updated = sc.ReplacePetTeams(teams) == nil || updated
			}
			// 盒位移动增量(box_pet_change,仅 CHANGE_PET 回包携带,其余 opcode 易误报)
			if m.Opcode == pet.OpPetBoxChangePetRsp {
				if moves := pet.ParseBoxMoves(m.AppBody); len(moves) > 0 {
					if sc.ApplyBoxMoves(moves) == nil {
						updated = true
						// 末项为被拖动(开始选中)的宠物:交换时回包按「先被挤走者、
						// 后拖动者落到目标位」排列,移到空位时也仅末项是被移动的宠物。
						last := moves[len(moves)-1]
						focusGid, focusBox = last.Gid, last.BoxID
					}
				}
			}
			// 宠物拥有的奖牌(仅登录数据携带 pet_medal_info),过滤掉非真实奖牌 id
			if m.Opcode == pet.OpLoginRsp {
				owns := pet.ParsePetMedals(m.AppBody)
				valid := owns[:0]
				for _, o := range owns {
					if _, ok := db.Medal(o.MedalID); ok {
						valid = append(valid, o)
					}
				}
				if len(valid) > 0 {
					updated = sc.ReplacePetMedals(valid) == nil || updated
				}
			}
			if updated {
				payload := map[string]any{"locUpdate": true}
				if focusGid != 0 {
					payload["focusGid"] = focusGid
					payload["focusBox"] = focusBox
				}
				srv.Hub().Broadcast("pet", acc, payload)
			}
		}

		// 携带更新后完整 PetData 的回包(换牌:佩戴奖牌已变;进化:base_conf_id 换形态、
		// 等级/属性/技能刷新),就地更新宠物(同一 gid)但不产生获得事件。
		if m.Direction == gcp.S2C && (m.Opcode == pet.OpPetMedalCommonRsp || m.Opcode == pet.OpPetEvoluteRsp) {
			if pd := pet.FindNewPet(m.AppBody); pd != nil {
				p := pet.ToPet(pd, db)
				sc.UpsertPet(p)
				srv.Hub().Broadcast("pet", acc, p)
			}
			continue
		}

		// 获得新宠物：孵蛋、战斗外捕捉、普通战斗内捕捉(经奖励通知)、花种战斗内捕捉(经玩家同步)
		// 都把新宠物嵌在子消息里。同一宠物可能经多个 opcode 下发，用 isNew 去重;获得方式由 catch_way 区分。
		if m.Direction == gcp.S2C &&
			(m.Opcode == pet.OpCrackEggRsp || m.Opcode == pet.OpPetCatchRsp ||
				m.Opcode == pet.OpGoodsRewardNotify || m.Opcode == pet.OpPlayerSyncNotify) {
			if pd := pet.FindNewPet(m.AppBody); pd != nil {
				// PLAYER_SYNC_NOTIFY 是通用同步通道(理论上可能携带 PvP 对手/旧快照),
				// 额外用 add_time 时近性(相对本包时间)守卫，仅认刚捕获的宠物。
				if m.Opcode == pet.OpPlayerSyncNotify && int64(pd.GetAddTime()) < m.Time.Unix()-grace {
					continue
				}
				p := pet.ToPet(pd, db)
				isNew, _ := sc.UpsertPet(p)
				srv.Hub().Broadcast("pet", acc, p)
				if isNew {
					ev := &store.Event{Time: m.Time.Unix(), Kind: store.EventObtain, SubKind: catchWayName(pd), Gid: p.Gid, Pet: p}
					if sc.AddEvent(ev) == nil {
						logEvent(acc, ev)
						srv.Hub().Broadcast("event", acc, ev)
					}
				}
			}
			continue
		}

		// 放生：服务器下行确认被放生的 gid 列表(库中无快照时仍记录 gid)
		if m.Direction == gcp.S2C && m.Opcode == pet.OpPetFreeRsp {
			freed := false
			for _, gid := range pet.ParseFreeRsp(m.AppBody) {
				old, _ := sc.RemovePet(gid)
				freed = true
				ev := &store.Event{Time: m.Time.Unix(), Kind: store.EventLose, SubKind: "放生", Gid: gid, Pet: old}
				if sc.AddEvent(ev) == nil {
					logEvent(acc, ev)
					srv.Hub().Broadcast("event", acc, ev)
				}
			}
			// 通知前端刷新列表与盒子/队伍示意图(放生已清掉盒位/队位)
			if freed {
				srv.Hub().Broadcast("pet", acc, map[string]any{"locUpdate": true})
			}
			continue
		}

		if m.Direction != gcp.S2C || m.Opcode != pet.OpGetPetInfoByPageRsp {
			continue
		}
		res := pet.ParsePetListRsp(m.AppBody)
		for _, pd := range res.Pets {
			p := pet.ToPet(pd, db)
			isNew, err := sc.UpsertPet(p)
			if err != nil {
				continue
			}
			srv.Hub().Broadcast("pet", acc, p)
			if isNew && int64(pd.GetAddTime()) >= startTS {
				ev := &store.Event{
					Time:    int64(pd.GetAddTime()),
					Kind:    store.EventObtain,
					SubKind: catchWayName(pd),
					Gid:     p.Gid,
					Pet:     p,
				}
				if sc.AddEvent(ev) == nil {
					logEvent(acc, ev)
					srv.Hub().Broadcast("event", acc, ev)
				}
			}
		}
	}
}

// logEvent 打印一条宠物增减事件日志(获得/失去)。
func logEvent(acc string, ev *store.Event) {
	verb := "获得"
	if ev.Kind == store.EventLose {
		verb = "失去"
	}
	sp := "?"
	if ev.Pet != nil && ev.Pet.Species != "" {
		sp = ev.Pet.Species
	}
	log.Printf("用户 %s %s宠物 %s(gid=%d) [%s]", acc, verb, sp, ev.Gid, ev.SubKind)
}

// catchWayName 由 catch_way 推断获得方式(实测：1=捕捉、3=孵蛋;其余未知归“获得”)。
func catchWayName(pd *pb.PetData) string {
	switch pd.GetCatchWay() {
	case 1, 4:
		return "捕捉" // 1=普通/战斗外捕捉, 4=花种(稀兽)战斗内捕捉
	case 3:
		return "孵蛋"
	default:
		return "获得"
	}
}
