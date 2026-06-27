package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
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
	srv := server.New(st, hub, db.OpcodeNames())
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
		n, _ := st.CountPets()
		log.Printf("回放完成，当前宠物 %d 只。Web 服务保持运行(Ctrl-C 退出)", n)
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

	for m := range eng.Out {
		// debug 页面：广播所有应用层消息
		srv.Hub().Broadcast("debug", map[string]any{
			"time":   m.Time.Unix(),
			"dir":    m.Direction.String(),
			"opcode": fmt.Sprintf("0x%04x", m.Opcode),
			"name":   srv.OpcodeName(m.Opcode),
		})

		if m.Direction != gcp.S2C || m.Opcode != pet.OpGetPetInfoByPageRsp {
			continue
		}
		res := pet.ParsePetListRsp(m.AppBody)
		for _, pd := range res.Pets {
			p := pet.ToPet(pd, db)
			isNew, err := st.UpsertPet(p)
			if err != nil {
				continue
			}
			srv.Hub().Broadcast("pet", p)
			if isNew && int64(pd.GetAddTime()) >= startTS {
				ev := &store.Event{
					Time:    int64(pd.GetAddTime()),
					Kind:    store.EventObtain,
					SubKind: catchWayName(pd),
					Gid:     p.Gid,
					Pet:     p,
				}
				if st.AddEvent(ev) == nil {
					srv.Hub().Broadcast("event", ev)
				}
			}
		}
	}
}

// catchWayName 由 catch_way 推断获得方式(细分映射后续可补)。
func catchWayName(pd *pb.PetData) string {
	switch pd.GetCatchWay() {
	case 1:
		return "捕捉"
	case 2:
		return "孵蛋"
	case 3:
		return "赠送"
	default:
		return "获得"
	}
}
