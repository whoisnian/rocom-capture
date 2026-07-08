package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
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
	ignoreIPs := flag.String("ignore-ip", "", "额外忽略的 IP(逗号分隔;两端命中即丢包)。实时抓包已自动忽略网卡自身 IP,此项用于离线回放或多网关等场景")
	port := flag.Int("port", 8195, "游戏服务器端口")
	addr := flag.String("addr", ":4939", "Web 服务监听地址")
	dbPath := flag.String("db", "rocom.db", "SQLite 数据库路径")
	useTLS := flag.Bool("tls", false, "启用 HTTPS(自签证书;手机经局域网访问以满足屏幕常亮等需 secure context 的 API)")
	certPath := flag.String("cert", "rocom-cert.pem", "TLS 证书路径(-tls 时不存在则自动生成自签证书)")
	keyPath := flag.String("key", "rocom-key.pem", "TLS 私钥路径(-tls 时不存在则自动生成)")
	flag.Parse()

	db, err := gamedata.Load()
	if err != nil {
		log.Fatalf("加载名称库失败: %v", err)
	}
	st, err := store.New(*dbPath, db)
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	hub := server.NewHub()
	srv := server.New(st, hub, db)
	eng := capture.NewEngine(*port)
	eng.Keys = st // 会话密钥持久化:抓包服务重启后继续解密仍存活的连接
	for s := range strings.SplitSeq(*ignoreIPs, ",") {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		ip, err := netip.ParseAddr(s)
		if err != nil {
			log.Fatalf("-ignore-ip 无效地址 %q: %v", s, err)
		}
		eng.AddSkipIP(ip)
	}

	go consume(eng, st, db, srv)

	go func() {
		if *useTLS {
			cert, err := loadOrCreateCert(*certPath, *keyPath)
			if err != nil {
				log.Fatalf("准备 TLS 证书失败: %v", err)
			}
			hs := &http.Server{
				Addr:      *addr,
				Handler:   srv.Handler(),
				TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}},
			}
			log.Printf("Web 界面: https://localhost%s (自签证书,浏览器首次访问需手动信任)", *addr)
			if err := hs.ListenAndServeTLS("", ""); err != nil {
				log.Fatalf("HTTPS 服务失败: %v", err)
			}
			return
		}
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
		if d := eng.BadKeyDropped(); d > 0 {
			log.Printf("提示: %d 个数据包因密钥错误(明文校验失败)被丢弃(缓存密钥失效时会出现)", d)
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

	// connAccount: GCP 连接(connID)→账号("UID:"+user_id)。同一客户端 IP 可能同时跑多个
	// 账号(不同设备经 NAT 同 IP、或不同游戏服),故按 user_id 而非 IP 归属:抓到某连接的
	// LOGIN_RSP 时解析 user_id 建映射。登录回包自身也带背包/队伍/奖牌快照,须先登记再归属。
	// 从库中预热已知映射:配合会话密钥缓存,抓包服务重启后无需再等登录回包即可归属消息。
	connAccount := map[string]string{}
	if saved, err := st.LoadSessionAccounts(); err == nil {
		connAccount = saved
	}

	for m := range eng.Out {
		// 登录回包:解析 user_id → 账号并登记 connID 映射(必须在下面 resolve acc 之前)。
		if m.Direction == gcp.S2C && m.Opcode == pet.OpLoginRsp {
			if id, name, ok := pet.ParseLoginAccount(m.AppBody); ok {
				acc := "UID:" + strconv.FormatUint(id, 10)
				nick := name
				if nick == "" {
					nick = "?"
				}
				if connAccount[m.Session] != acc { // 同一登录会重复下发,仅首次记日志并落盘映射
					log.Printf("用户 %s (UID:%d) 登录成功 [%s]", nick, id, m.Session)
					st.SaveSessionAccount(m.Session, acc)
				}
				connAccount[m.Session] = acc
				if name == "" {
					name = acc
				}
				st.UpsertAccount(acc, name)
			}
		}
		acc := connAccount[m.Session]

		// debug 页面：广播所有应用层消息,按来源账号归属(登录前无法归属的连接消息 acc="" 作全局)。
		// 订阅端据此只推当前账号的调试流;账号也放进 data 供页面列展示。
		srv.Hub().Broadcast("debug", acc, map[string]any{
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
			// 全量背包快照:整体替换盒位(占用)+ 盒子元数据(名称/数量/位置,含空盒)。
			// 登录/整理走 PetBackpackInfo;整理排列(改名/换位)的 SETTING_UP 回包是裸的
			// repeated PetBox(非 PetBackpackInfo),前者解不出时按后者再试。
			if pet.CarriesBackpack(m.Opcode) {
				entries, metas := pet.ParseBackpack(m.AppBody)
				if len(metas) == 0 && m.Opcode == pet.OpPetBoxSettingUpRsp {
					entries, metas = pet.ParseBoxSettingUp(m.AppBody)
				}
				if len(metas) > 0 {
					updated = sc.ReplacePetBoxMetas(metas) == nil || updated
				}
				if len(entries) > 0 {
					updated = sc.ReplacePetBoxes(entries) == nil || updated
				}
				// 单盒元数据增量:解锁(新增空盒→盒数+1)/设标记·改名(更新单盒名称/标记)
				var meta *pet.BoxMeta
				switch m.Opcode {
				case pet.OpPetBoxUnlockRsp:
					meta = pet.ParseBoxUnlock(m.AppBody)
				case pet.OpPetBoxSetMarkTypeRsp:
					meta = pet.ParseBoxSetMark(m.AppBody)
				}
				if meta != nil {
					updated = sc.UpsertPetBoxMeta(*meta) == nil || updated
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
		// 等级/属性/技能刷新;伙伴标记增删改:partner_mark 已变),就地更新宠物(同一 gid)但不产生获得事件。
		if m.Direction == gcp.S2C && (m.Opcode == pet.OpPetMedalCommonRsp || m.Opcode == pet.OpPetEvoluteRsp ||
			m.Opcode == pet.OpUpdatePetCollectTagRsp) {
			if pd := pet.FindNewPet(m.AppBody); pd != nil {
				p := pet.ToPet(pd, db)
				sc.UpsertPet(p)
				srv.Hub().Broadcast("pet", acc, p)
			}
			continue
		}

		// 获得新宠物：孵蛋、战斗外捕捉、普通战斗内捕捉(经奖励通知)、花种战斗内捕捉(经玩家同步)、
		// 传说精灵战后捕捉(catch_way=5,仅经战斗结束通知下发)都把新宠物嵌在子消息里。同一宠物可能
		// 经多个 opcode 下发(普通捕捉的 BATTLE_FINISH 与 GOODS_REWARD 重复),用 isNew 去重;获得方式由 catch_way 区分。
		if m.Direction == gcp.S2C &&
			(m.Opcode == pet.OpCrackEggRsp || m.Opcode == pet.OpPetCatchRsp ||
				m.Opcode == pet.OpGoodsRewardNotify || m.Opcode == pet.OpPlayerSyncNotify ||
				m.Opcode == pet.OpBattleFinishNotify) {
			if pd := pet.FindNewPet(m.AppBody); pd != nil {
				// PLAYER_SYNC_NOTIFY/BATTLE_FINISH_NOTIFY 是通用通知通道(理论上可能携带对手/旧快照),
				// 额外用 add_time 时近性(相对本包时间)守卫，仅认刚捕获的宠物。
				if (m.Opcode == pet.OpPlayerSyncNotify || m.Opcode == pet.OpBattleFinishNotify) &&
					int64(pd.GetAddTime()) < m.Time.Unix()-grace {
					continue
				}
				p := pet.ToPet(pd, db)
				isNew, _ := sc.UpsertPet(p)
				// 获得新宠物的回包(战斗外捕捉/孵蛋等)常同时携带该宠物的盒位放置(box_pet_change);
				// 据此落库盒位,否则新宠物在盒子示意图上缺位(仅列表末尾可见,位置标「待同步」)。
				// 严格按本次新宠 gid 过滤:回包体内只有该宠物一条落位,借此排除 PetData 子结构被误解析。
				var placed []pet.BoxEntry
				for _, mv := range pet.ParseBoxMoves(m.AppBody) {
					if mv.Gid == p.Gid {
						placed = append(placed, mv)
					}
				}
				if len(placed) > 0 {
					sc.ApplyBoxMoves(placed)
				}
				srv.Hub().Broadcast("pet", acc, p)
				if isNew {
					ev := &store.Event{Time: m.Time.Unix(), SubKind: catchWayName(pd, acc), Gid: p.Gid, Pet: p}
					if sc.AddEvent(ev) == nil {
						logEvent(acc, ev)
						srv.Hub().Broadcast("event", acc, ev)
					}
				}
			}
			continue
		}

		// 放生：服务器下行确认被放生的 gid 列表。宠物减少不计入事件,仅从库中移除并刷新前端。
		if m.Direction == gcp.S2C && m.Opcode == pet.OpPetFreeRsp {
			freed := false
			for _, gid := range pet.ParseFreeRsp(m.AppBody) {
				sc.RemovePet(gid)
				freed = true
			}
			// 通知前端刷新列表与盒子/队伍示意图(放生已清掉盒位/队位)
			if freed {
				srv.Hub().Broadcast("pet", acc, map[string]any{"locUpdate": true})
			}
			continue
		}

		// 赠送:玩家开盒子手动把共同捕捉的宠物赠送给好友。宠物减少不计入事件,
		// 仅据执行回包携带的 gid 从自己库中移除并刷新前端。
		if m.Direction == gcp.S2C && m.Opcode == pet.OpTogetherCatchGiftRsp {
			if gid := pet.ParseTogetherCatchGiftRsp(m.AppBody); gid != 0 {
				sc.RemovePet(gid)
				// 刷新列表与盒子/队伍示意图(赠送已清掉盒位/队位)
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
					SubKind: catchWayName(pd, acc),
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

// logEvent 打印一条获得宠物事件日志。
func logEvent(acc string, ev *store.Event) {
	sp := "?"
	if ev.Pet != nil && ev.Pet.Species != "" {
		sp = ev.Pet.Species
	}
	log.Printf("用户 %s 获得宠物 %s(gid=%d) [%s]", acc, sp, ev.Gid, ev.SubKind)
}

// catchWayName 由 catch_way 推断获得方式(实测：1=捕捉、3=孵蛋;其余未知归“获得”)。
// 例外:共同捕捉转赠的宠物 catch_way 仍是 1,但对接收方应记「赠送获得」而非「捕捉」——
// 据 together_catch_info 区分(related_uin=接收方、catched_uin=捕捉方):本账号是接收方且非捕捉方即为受赠。
func catchWayName(pd *pb.PetData, acc string) string {
	if tci := pd.GetTogetherCatchInfo(); tci != nil {
		if uid, ok := uidFromAcc(acc); ok &&
			tci.GetRelatedUin() == uid && tci.GetCatchedUin() != 0 && tci.GetCatchedUin() != uid {
			return "赠送获得"
		}
	}
	switch pd.GetCatchWay() {
	case 1, 4, 5:
		return "捕捉" // 1=普通/战斗外捕捉, 4=花种(稀兽)战斗内捕捉, 5=传说精灵战后(耗体力)捕捉
	case 3:
		return "孵蛋"
	default:
		return "获得"
	}
}

// uidFromAcc 从账号标识("UID:<user_id>")取回 user_id。
func uidFromAcc(acc string) (uint32, bool) {
	s, ok := strings.CutPrefix(acc, "UID:")
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, false
	}
	return uint32(v), true
}
