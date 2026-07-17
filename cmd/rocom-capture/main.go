package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"math"
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
	"github.com/whoisnian/rocom-capture/internal/scene"
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

// petSweep 累积一轮分页宠物列表全量下发。客户端登录/打开仓库时会连续请求 page 1..TotalPage,
// 在末页据完整快照对账:库中存在却不在快照里的 gid,即玩家在别处放生/赠送的宠物,予以清除。
// 仅当 1..TotalPage 连续到达才对账(nextPage 校验),乱序或单独请求某页不触发,避免误删。
type petSweep struct {
	gids     map[uint32]bool // 本轮各页出现过的 gid
	nextPage uint32          // 期望的下一页号(保证连续)
	valid    bool            // 自 page 1 起连续累积至今(否则不对账)
	start    time.Time       // 本轮起始(page 1 到达):对账时据此放过其后刚入库的新宠;并计全量请求耗时
	proc     time.Duration   // 累计实际解析+入库(+末页对账)耗时,排除等待客户端下一页的空档,用于暴露处理瓶颈
}

// layerDebounce 是分层地图切换的去抖时长。区域触发体之间有接缝,玩家在洞内正常走动会短暂「擦出」
// 所有区域(实测空窗 0/0/94ms),贴着楼梯口走也会短暂擦进上层(实测 107ms);若照单全收,叠加图就会
// 一闪一闪,看着像层图与底图不同步。而真正的进出层空窗是秒级的(实测 3.8/5.1/15.7s),两者差一个
// 数量级,故只采纳「稳定超过本时长」的变化。代价是进出洞的切换晚 0.3s 可见,无关痛痒。
const layerDebounce = 300 * time.Millisecond

// layerState 是某连接的分层地图去抖状态:cur 为正在显示的层,pend 为待确认的新值。
type layerState struct {
	cur, pend     gamedata.LayerInfo
	curOK, pendOK bool
	since         time.Time // pend 首次出现的时刻
	// fresh:换场景/传送后到**首个移动包**之间的「落地窗口」。此间的区域事件是落地时的权威状态,
	// 不可能是走动擦出接缝的噪声,故直接采纳、不等去抖——否则传送进洞后若站着不动,进入事件会一直
	// 卡在去抖里(没有下一个移动包来推进它),洞穴层图迟迟不出现。
	fresh bool
}

// settle 收一个「按区域算出的层」,返回去抖后应显示的层。
func (ls *layerState) settle(l gamedata.LayerInfo, ok bool, now time.Time) (gamedata.LayerInfo, bool) {
	same := func(a gamedata.LayerInfo, aok bool, b gamedata.LayerInfo, bok bool) bool {
		return aok == bok && (!aok || a.ID == b.ID)
	}
	switch {
	case ls.fresh: // 落地窗口内(换场景后、玩家尚未移动):直接采纳,不等去抖
		ls.cur, ls.curOK = l, ok
		ls.pend, ls.pendOK, ls.since = l, ok, time.Time{}
	case same(l, ok, ls.cur, ls.curOK): // 与在显示的一致:清掉待定
		ls.pend, ls.pendOK, ls.since = l, ok, time.Time{}
	case !same(l, ok, ls.pend, ls.pendOK) || ls.since.IsZero(): // 新的候选:开始计时
		ls.pend, ls.pendOK, ls.since = l, ok, now
	case now.Sub(ls.since) >= layerDebounce: // 候选稳定够久:采纳
		ls.cur, ls.curOK = l, ok
		ls.since = time.Time{}
	}
	return ls.cur, ls.curOK
}

// ---- 眠枭之星收集判定(见 docs/data.md 3.4)----
//
// 星/光点:已收集的服务器**根本不刷**,只有未收集的才作为 NPC 实体下发(实体带刷新点 id)。故:
//
//	收到某点的实体          ⇒ 未收集
//	走到某点附近却没有实体  ⇒ 已收集
//
// 石像**不同**:本体收集后不消失、实体一直下发,「出现/消失」不携带收集信息;它的星是实体上的
// 挂件,状态就在实体里(scene.NpcActor.Pendant),触碰收集时客户端发挂件交互(0x0272,带刷新行
// id)。故石像看挂件定状态、只在挂件交互成功时判「刚收走」,不参与「实体离开=被收走」的判定;
// 而 seen 的语义(true ⇒ 未收集)对石像同样成立(挂件已收的石像不置 seen),扫描逻辑无需分叉。
//
// AOI 是**按格子**下发的(配置里有「跨aoi拆分」的区域),不是圆形半径:实测多次出现「更远的实体
// 下发了、更近的没下发」。故不能拿单一半径当 AOI 边界,只能取一个**保守判定半径**——4 份 pcap 里
// 凡距玩家轨迹 ≤100m 的固定 POI(必定存在那些)全部下发,无一例外,故 80m 留足余量。
//
// 但「进圈时刻」不能立即结账:实体按跨格触发下发,可以晚于进圈 4-31s、晚到时玩家已近至 21-59m
// (12 份 pcap 共 5 例),圈边缘徘徊时延迟无上界——进圈即判会闪烁(先判已收集隐藏图标,实体
// 随后到达又翻回)。空间邻近也推不出「该格已下发」(实测有星点 20m 内他者实体早到、星实体晚
// 31s,格边界贴着点过)。故只在两种**实体必已下发**的时机结账:贴脸(≤starCommitNear,实测
// 最早晚到距离 21m 的一半),或已过最近点回撤(≥minD+starCommitBack,即接近段结束——实体
// 要来早来了)。回撤结账在圈外也生效(擦圈边而过的点,回撤 15m 时往往已出圈)。代价只是
// 结账推迟到走过之后几秒,12 份 pcap 复演:零闪烁、无误判、无漏判(仅 pcap 截断处未及结账)。
const (
	starSweepRadius   = 8000  // 判定半径(厘米):玩家进到此距离内仍无实体 ⇒ 该点已收集(结账另看时机)
	starCommitNear    = 1000  // 贴脸结账距离:实体最早在 21m 处必已下发,10m 留足余量
	starCommitBack    = 1500  // 回撤结账迟滞:距离回升超过最近点这么多 ⇒ 接近段结束
	starCollectRadius = 3000  // 实体离开 AOI 时,玩家在此距离内 ⇒ 是被收走了(而非走远出 AOI)
	starSettle        = 1500 * time.Millisecond // 进场景后等快照到齐再判定,免得把还没下发的当成已收集
)

// starTracker 是一个连接在**当前场景会话**内的星星观测态(换场景/传送即重置)。
type starTracker struct {
	seen   map[int32]bool      // 本场景收到过实体的刷新点 id ⇒ 未收集
	actor  map[uint64]int32    // 实体 actor_id -> 刷新点 id(实体离开时只给 actor_id)
	minD   map[int32]float64   // 刷新点 id -> 本场景内玩家距它的最近距离(只记进过判定圈的,结账时机用)
	snapAt time.Time           // 周边实体快照(0x014a)到达时刻;零值 = 还没到,不做已收集判定
	res    int32               // 当前场景 res(星点按场景取)
}

// posXY 从位置推送里取玩家世界坐标。
func posXY(pos map[string]any) (int32, int32, bool) {
	x, ok1 := pos["x"].(int32)
	y, ok2 := pos["y"].(int32)
	return x, y, ok1 && ok2
}

// starPos 查某刷新点的世界坐标(星点来自 gamedata 的 POI 表)。
func starPos(db *gamedata.DB, res int32, refreshID int32) [2]int32 {
	for _, p := range db.POIs(uint32(res)) {
		if p.R == refreshID {
			return [2]int32{p.X, p.Y}
		}
	}
	return [2]int32{}
}

// near 报告 (x,y) 是否在点 p 的 r 厘米内(平面距离;星星有同 xy 叠放的,z 不参与)。
func near(x, y int32, p [2]int32, r int32) bool {
	if p == ([2]int32{}) {
		return false
	}
	dx, dy := float64(x-p[0]), float64(y-p[1])
	return math.Hypot(dx, dy) <= float64(r)
}

// buildPos 组装一条位置推送(不含分层)。移动包与**传送落点**共用:传送时用一个只带 Pos/Yaw/StopMove
// 的合成 MoveReq(无速度、无轨迹),这样传送一下发就能把地图切到目的地,不必干等第一个移动包。
func buildPos(db *gamedata.DB, acc string, res, room int32, mr scene.MoveReq, t time.Time) map[string]any {
	// 地表底图始终作背景;玩家点用底图投影。坐标系统一为底图。
	pos := map[string]any{
		"account":    acc,
		"sceneResId": res,
		"sceneCfgId": mr.SceneCfgID,
		"sceneName":  sceneDisplayName(db, res, mr.SceneCfgID),
		"img":        db.MapImage(uint32(res), room), // 底图文件名(家园按等级 <res>_<lv>);无底图为空
		"x":          mr.Pos.X,
		"y":          mr.Pos.Y,
		"z":          mr.Pos.Z,
		"heading":    float64(mr.Yaw) / 10, // 朝向角(度),UE Yaw:0=世界+X(地图东/右),顺时针增
		"stop":       mr.StopMove,
		"ts":         t.Unix(),
		"tsMs":       t.UnixMilli(), // 前端判断缓存位置是否过期(过期则不外推)
	}
	u, v, ok := db.Project(uint32(res), mr.Pos.X, mr.Pos.Y)
	if !ok {
		return pos // 该场景无底图:只回坐标
	}
	pos["u"], pos["v"] = u, v
	mi, ok := db.MapInfo(uint32(res))
	if !ok || mi.Side == 0 {
		return pos
	}
	// 速度向量(UE 厘米/秒)按同一投影(纯缩放:u=(x-ox)/side)换算为「归一化底图坐标/秒」,
	// 供前端在两包之间逐帧外推(航位推算),即客户端给其他玩家做平滑的同一套办法。
	// 实测(pcap 回放)上一包 pos+speed*Δt 预测下一包 pos:中位误差 3cm、直线段 3s 也仅几米。
	if !mr.StopMove {
		pos["vu"] = float64(mr.Speed.X) / float64(mi.Side)
		pos["vv"] = float64(mr.Speed.Y) / float64(mi.Side)
	}
	// 客户端沉默一段(直线巡航/推住摇杆盘旋时退化成 2.5-3s 心跳)后补报的真实轨迹:
	// 那几秒里它没报过位置,前端只能外推;等这段轨迹到了就沿它把箭头滑回真实路线上(转弯尤其明显)。
	// 持续操作时上报本就 0.1s 一包(轨迹点为空/极短),不必也不能回放——那会让 0.45s 的滑行跨过好几个
	// 新包,箭头反而落后。故以轨迹跨度为准。
	if mr.SegSpan() >= minSegSpan {
		path := make([]map[string]any, 0, len(mr.Segs)+1)
		for _, sg := range mr.Segs {
			if su, sv, ok := db.Project(uint32(res), sg.Pos.X, sg.Pos.Y); ok {
				path = append(path, map[string]any{"u": su, "v": sv})
			}
		}
		// 末段采样略早于包时刻(实测差 0.2–0.6 个采样步长),to_pos 才是最新位置:补作轨迹终点,
		// 前端滑完轨迹正好落在上报位置,与其后的外推无缝衔接。
		if len(path) > 0 {
			if last := path[len(path)-1]; last["u"] != u || last["v"] != v {
				path = append(path, map[string]any{"u": u, "v": v})
			}
		}
		if len(path) >= 2 {
			pos["path"] = path
		}
	}
	return pos
}

// layerPayload 把某分层地图投影成底图上的归一化矩形(u0,v0)-(u1,v1),前端据此定位切片图
// (透明处透出底图);玩家点仍用底图投影,自然落在矩形内。该场景无底图时返回 nil。
func layerPayload(db *gamedata.DB, res int32, l gamedata.LayerInfo) map[string]any {
	mi, ok := db.MapInfo(uint32(res))
	if !ok || mi.Side == 0 {
		return nil
	}
	return map[string]any{
		"img": "layer/" + l.Img,
		"u0":  float64(l.OX-mi.OX) / float64(mi.Side),
		"v0":  float64(l.OY-mi.OY) / float64(mi.Side),
		"u1":  float64(l.OX+l.Side-mi.OX) / float64(mi.Side),
		"v1":  float64(l.OY+l.Side-mi.OY) / float64(mi.Side),
	}
}

// activeFuncs 把「func → area 集合」压成 func 集合(玩家当前所在的 area_func_id),供选层。
func activeFuncs(funcs map[uint32]map[uint32]bool) map[uint32]bool {
	out := make(map[uint32]bool, len(funcs))
	for fn, set := range funcs {
		if len(set) > 0 {
			out[fn] = true
		}
	}
	return out
}

// minSegSpan 是「值得回放的真实轨迹」的最短跨度(秒)。移动包按操作事件上报:持续改方向/变速时
// 约 0.1s 一包(轨迹点为空或只有一两个,回放毫无意义且会拖慢箭头);推住摇杆盘旋或直线巡航时输入
// 不变,退化成 2.5-3s 一次心跳,那几秒实际走的路(含大转弯)只在 move_seg_list 里。取 0.6s 为界。
const minSegSpan = 0.6

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

	// sweeps: 账号 → 正在累积的分页宠物列表快照(见 petSweep)。末页到达即对账清除别处放生/赠送的残留。
	sweeps := map[string]*petSweep{}

	// 实时地图(仅自己):按连接维护当前 scene_res_cfg_id(s2c 进入/传送更新),
	// c2s 移动包结合当前场景投影成地图坐标推给前端(逐包推送,不节流,峰值约 8 条/秒)。
	// 从库预热:进入/传送通知只在切场景时下发,游戏中途不重发,故须像会话密钥一样从缓存恢复,
	// 否则重启后虽能解密移动包,却因不知当前 res 而无法定位底图(移动包只带 scene_cfg_id)。
	sceneByConn := map[string]int32{} // connID -> 当前 scene_res_cfg_id
	roomByConn := map[string]int32{}  // connID -> 家园房屋等级(家园室内选分层底图,非家园为 0)
	// areasByConn: connID -> area_func_id -> 该 func 下已进入的 area_id 集合。由服务器的区域进/出
	// 事件维护(见 scene.ParseAreaActs),是「玩家当前在洞穴/几楼」的权威依据。一个 func 可含多个
	// area(如信仰者村落一层同时进入 541030265/541030499),故按 func 存 area 集合:离开其中一个
	// 仍在该层,集合空了才算离开。同样只在跨越触发体时下发,故与场景 res 一样落盘供重启恢复。
	areasByConn := map[string]map[uint32]map[uint32]bool{}
	layerByConn := map[string]*layerState{} // connID -> 分层地图去抖状态(见 layerDebounce)
	starByConn := map[string]*starTracker{} // connID -> 眠枭之星收集判定(见 starTracker)
	// acc -> camp -> 该区已收集数合计(服务器口径,各形态相加)。用作 starSweep 的守卫:
	// 某区 got=0 ⇒ 该区**任何点都不可能已收集**,「走近无实体」只能是没刷出(2026-07-17 实测:
	// 新区紫星配置/计数就位但实体未开放刷出,无此守卫会把玩家路过的点全误判成已收集)。
	// key 的**存在性**也有语义:map 里有 camp = 服务器给过该区计数行;根本没行的区(月兔暗港)
	// 不注册任何星,不参与守卫(见 starSweep)。
	zoneGotByAcc := map[string]map[int32]int32{}
	zoneGot := func(acc string) map[int32]int32 {
		if g, ok := zoneGotByAcc[acc]; ok {
			return g
		}
		g := map[int32]int32{} // 抓包服务重启后从库预热
		for _, r := range st.StarZones(acc) {
			g[r.Camp] += r.Got
		}
		zoneGotByAcc[acc] = g
		return g
	}
	starKnown := map[string]map[int32]int{} // account -> 已确认的星星状态(库内快照,只写增量)
	pendantByConn := map[string]int32{}     // connID -> 最近一次挂件交互(0x0272)的刷新行 id,等回包确认
	if saved, err := st.LoadSessionScenes(); err == nil {
		for id, s := range saved {
			sceneByConn[id] = s.Res
			roomByConn[id] = s.Room
			for fn, ids := range s.Areas {
				set := map[uint32]bool{}
				for _, a := range ids {
					set[a] = true
				}
				if len(set) > 0 {
					if areasByConn[id] == nil {
						areasByConn[id] = map[uint32]map[uint32]bool{}
					}
					areasByConn[id][fn] = set
				}
			}
		}
	}
	// saveAreas 把某连接的区域集合落盘(map[func][]area 形式)。
	saveAreas := func(conn string) {
		out := map[uint32][]uint32{}
		for fn, set := range areasByConn[conn] {
			for a := range set {
				out[fn] = append(out[fn], a)
			}
		}
		st.SaveSessionAreas(conn, out)
	}
	// resetAreas 清空某连接的区域与去抖状态(换场景/传送时)。
	resetAreas := func(conn string) {
		delete(areasByConn, conn)
		delete(layerByConn, conn)
		saveAreas(conn)
	}
	// starSave 落盘并广播星星状态增量(只写与库内快照不同的)。
	starSave := func(acc string, states map[int32]int) {
		known := starKnown[acc]
		if known == nil {
			known = st.StarStates(acc)
			starKnown[acc] = known
		}
		diff := map[int32]int{}
		for rid, s := range states {
			if known[rid] != s {
				known[rid], diff[rid] = s, s
			}
		}
		if len(diff) == 0 {
			return
		}
		st.SetStarStates(acc, diff)
		srv.Hub().Broadcast("stars", acc, diff)
	}
	// starSee 收录一个星星系实体:星/光点按「出现 ⇒ 未收集」;石像按挂件状态定收集与否
	// (本体常驻,出现不代表未收集),且不进 ts.actor——「实体离开 = 被收走」对石像不成立。
	starSee := func(ts *starTracker, a scene.NpcActor, states map[int32]int) {
		if a.IsStatue() {
			if a.Pendant == scene.PendantCollected {
				delete(ts.seen, a.RefreshID)
				states[a.RefreshID] = store.StarCollected
			} else { // 挂件未收集;个别实体缺挂件字段时也保守视作未收集(宁可多显示)
				ts.seen[a.RefreshID] = true
				states[a.RefreshID] = store.StarUncollected
			}
			return
		}
		ts.seen[a.RefreshID] = true
		ts.actor[a.ActorID] = a.RefreshID
		states[a.RefreshID] = store.StarUncollected
	}
	// starObserve 收下一个 AOI 通知里的星星实体进/离(0x0413/0x0414)。
	// 实体进入 ⇒ 见 starSee;实体离开且玩家就在旁边 ⇒ 刚被收走(走远出 AOI 的离开不算,故看距离)。
	starObserve := func(conn, acc string, body []byte, pos map[string]any) {
		ts := starByConn[conn]
		if ts == nil {
			return
		}
		states := map[int32]int{}
		for _, a := range scene.ParseActorEnter(body) {
			if a.IsStar() && a.RefreshID != 0 {
				starSee(ts, a, states)
			}
		}
		for _, id := range scene.ParseActorLeave(body) {
			rid, ok := ts.actor[id]
			if !ok {
				continue
			}
			delete(ts.actor, id)
			// 玩家不可能隔着几十米收集:只有他就在旁边时,实体消失才是「被收走」。
			if px, py, ok := posXY(pos); ok && near(px, py, starPos(db, ts.res, rid), starCollectRadius) {
				delete(ts.seen, rid)
				states[rid] = store.StarCollected
			}
		}
		starSave(acc, states)
	}
	// starSweep 按玩家当前位置判定周围的星星:走到判定半径内却没收到实体 ⇒ 已收集。
	starSweep := func(conn, acc string, res int32, x, y int32, now time.Time) {
		ts := starByConn[conn]
		// 快照没到齐就判,会把「还没下发」当成「已收集」。
		if ts == nil || ts.res != res || ts.snapAt.IsZero() || now.Sub(ts.snapAt) < starSettle {
			return
		}
		states := map[int32]int{}
		got := zoneGot(acc)
		// 一条区域计数都没有(本会话与库里都没见过进场景包)⇒ 守卫无从工作,全部不判。
		if len(got) == 0 {
			return
		}
		for _, p := range db.POIs(uint32(res)) {
			if !strings.HasPrefix(p.K, "star") {
				continue
			}
			d := math.Hypot(float64(x-p.X), float64(y-p.Y))
			md, entered := ts.minD[p.R]
			// 没进过判定圈的点不关心;进过的出圈后仍要评估(回撤结账常发生在圈外)。
			if d > starSweepRadius && !entered {
				continue
			}
			if ts.seen[p.R] {
				if d <= starSweepRadius {
					states[p.R] = store.StarUncollected
				}
				continue
			}
			if d <= starSweepRadius && (!entered || d < md) {
				ts.minD[p.R], md, entered = d, d, true
			}
			// 守卫:候选区域(见 POI.Z)只要有一个**有计数行且 got=0**,「已收集」就不可能成立
			//(真实归属区必在候选之中),多半是该点还没开放刷出——不判,保持显示。
			// 服务器**根本没给计数行**的候选(如月兔暗港,该区不注册任何星)不可能是真归属区,
			// 跳过不挡——否则重叠带上的点会被永远卡住(2026-07-17 pcap 实测:望风半岛 3 点
			// 已收集却因候选含月兔暗港而永不隐藏)。
			ok := true
			for _, c := range p.Z {
				if g, tracked := got[c]; tracked && g == 0 {
					ok = false
					break
				}
			}
			// 结账时机:贴脸,或已过最近点回撤(实体若在早就该到了,见 starCommitNear/Back 注释)。
			if ok && (d <= starCommitNear || (entered && d >= md+starCommitBack)) {
				states[p.R] = store.StarCollected
			}
		}
		starSave(acc, states)
	}
	// layerOf 按当前区域集合定层(经去抖)。fromMove=true 表示由移动包触发:玩家开始动了,
	// 落地窗口就此关闭,其后的层变化一律走去抖(滤掉走动擦出/擦进触发体接缝的抖动)。
	layerOf := func(conn string, res int32, t time.Time, fromMove bool) (gamedata.LayerInfo, bool) {
		ls := layerByConn[conn]
		if ls == nil {
			ls = &layerState{fresh: true} // 换场景/传送后的落地窗口(见 layerState.fresh)
			layerByConn[conn] = ls
		}
		raw, rawOK := db.LayerIn(res, activeFuncs(areasByConn[conn]))
		l, ok := ls.settle(raw, rawOK, t)
		if fromMove {
			ls.fresh = false
		}
		return l, ok
	}
	// lastPos 为各账号最近推送的位置载荷(供 layerOnly 更新时合并回缓存)。
	lastPos := map[string]map[string]any{}
	// settleLayer 在区域事件后重新定层;层变了就推一条只更新分层的消息(不动位置锚点)。
	settleLayer := func(conn, acc string, t time.Time) {
		res := sceneByConn[conn]
		if res == 0 {
			return
		}
		ls := layerByConn[conn]
		var prev gamedata.LayerInfo
		var prevOK bool
		if ls != nil {
			prev, prevOK = ls.cur, ls.curOK
		}
		l, ok := layerOf(conn, res, t, false)
		if ok == prevOK && (!ok || l.ID == prev.ID) {
			return // 层没变
		}
		upd := map[string]any{
			"account":   acc,
			"layerOnly": true, // 前端只叠加/撤下切片图,不动位置锚点
			"ts":        t.Unix(),
			"tsMs":      t.UnixMilli(), // 仅供观测(调试页/回放核对);前端合并时不取
		}
		if ok {
			upd["layer"], upd["sceneName"] = layerPayload(db, res, l), l.Name
		} else {
			cfg := int32(0)
			if p := lastPos[acc]; p != nil {
				cfg, _ = p["sceneCfgId"].(int32)
			}
			upd["layer"], upd["sceneName"] = nil, sceneDisplayName(db, res, cfg)
		}
		if p := lastPos[acc]; p != nil { // 同步进缓存,页面加载(GET /api/position)也能带上分层
			p["layer"], p["sceneName"] = upd["layer"], upd["sceneName"]
			srv.SetLastPosition(acc, p)
		}
		srv.Hub().Broadcast("position", acc, upd)
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
					log.Printf("用户 %s (%s) 登录成功 [%s]", acc, nick, m.Session)
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

		// 去抖中的层变化需要「过一会儿再看一眼」才能采纳,而玩家可能站着不动、迟迟没有下一个移动包。
		// 故借该连接的任意一条消息(心跳等,实测约 1.6s 一条)把去抖推进到底。
		if ls := layerByConn[m.Session]; ls != nil && !ls.since.IsZero() {
			settleLayer(m.Session, acc, m.Time)
		}

		// 实时地图(仅自己):s2c 进入/传送更新当前场景 res 与落点、区域进出更新所在层;c2s 移动包投影后推送。
		switch {
		case m.Direction == gcp.S2C && m.Opcode == scene.OpEnterSceneRsp:
			if _, res, room, ok := scene.ParseEnterScene(m.AppBody); ok {
				sceneByConn[m.Session], roomByConn[m.Session] = res, room
				st.SaveSessionScene(m.Session, res, room) // 落盘供重启恢复
				// 换场景/传送后旧区域一律作废:服务器不为它们补发离开事件,只在落地后重发进入事件
				// (客户端同样在传送时清空区域,见 AreaAndZoneModule:OnTeleportClearAreaInfo)。
				resetAreas(m.Session)
				// 星星观测态按场景重置:上个场景的实体不算数。周边实体快照(0x014a)随后才到。
				starByConn[m.Session] = &starTracker{
					seen: map[int32]bool{}, actor: map[uint64]int32{}, minD: map[int32]float64{}, res: res,
				}
			}
			// 按区域的收集进度(服务器口径):前端按候选区域整片隐藏,starSweep 按 got=0 挡误判。
			if zp := scene.ParseZoneProgress(m.AppBody); len(zp) > 0 {
				rows := make([]store.ZoneProgressRow, 0, len(zp))
				g := map[int32]int32{}
				for _, p := range zp {
					rows = append(rows, store.ZoneProgressRow{Camp: p.Camp, NpcID: p.NpcID, Got: p.Got, Total: p.Total})
					g[p.Camp] += p.Got
				}
				zoneGotByAcc[acc] = g
				st.SetStarZones(acc, rows)
				srv.Hub().Broadcast("starzones", acc, rows)
			}
			continue
		case m.Direction == gcp.S2C && m.Opcode == scene.OpEnterSceneFinishAck:
			// 周边实体快照:进场景/传送后一次性给出 AOI 内的实体。星/光点实体 ⇒ 那些点未收集;
			// 石像实体按挂件状态直接定收集与否(见 starSee)。
			ts := starByConn[m.Session]
			if ts == nil {
				continue
			}
			states := map[int32]int{}
			for _, a := range scene.ParseSceneActors(m.AppBody) {
				if a.IsStar() && a.RefreshID != 0 {
					starSee(ts, a, states)
				}
			}
			ts.snapAt = m.Time
			starSave(acc, states)
			continue
		case m.Direction == gcp.S2C && m.Opcode == scene.OpTeleportNotify:
			tp, ok := scene.ParseTeleport(m.AppBody)
			if !ok {
				continue
			}
			sceneByConn[m.Session], roomByConn[m.Session] = tp.ResID, tp.Room
			st.SaveSessionScene(m.Session, tp.ResID, tp.Room)
			resetAreas(m.Session)
			// 传送落点(to_pt)此刻已知,而客户端要过几秒(加载)才落地并开始发移动包:立刻按落点推一条
			// 位置,否则地图会停在原地干等,玩家落地后不动更是一直不更新(分层也跟着不出现)。
			// 落点是静止的一点:无速度、无轨迹;分层留待落地后的区域进入事件补上(见下)。
			pos := buildPos(db, acc, tp.ResID, tp.Room, scene.MoveReq{
				Pos: tp.Pos, Yaw: tp.Yaw, StopMove: true, SceneCfgID: tp.CfgID,
			}, m.Time)
			lastPos[acc] = pos
			srv.SetLastPosition(acc, pos)
			srv.Hub().Broadcast("position", acc, pos)
			continue
		case m.Direction == gcp.S2C && m.Opcode == scene.OpPlayActsBatchNotify:
			starObserve(m.Session, acc, m.AppBody, lastPos[acc])
			continue
		case m.Direction == gcp.C2S && m.Opcode == scene.OpNpcPendantInteractReq:
			// 触碰石像上浮现的星:请求直接带石像刷新行 id,等回包(0x0273)确认后判已收集。
			if rid, ok := scene.ParsePendantInteract(m.AppBody); ok {
				pendantByConn[m.Session] = rid
			}
			continue
		case m.Direction == gcp.S2C && m.Opcode == scene.OpNpcPendantInteractRsp:
			rid := pendantByConn[m.Session]
			delete(pendantByConn, m.Session)
			ts := starByConn[m.Session]
			if rid == 0 || ts == nil || !scene.ParsePendantInteractRsp(m.AppBody) {
				continue
			}
			// 只认当前场景确有该刷新点的 POI(其它 NPC 的挂件交互对不上星点,自然被滤掉)。
			if starPos(db, ts.res, rid) == ([2]int32{}) {
				continue
			}
			delete(ts.seen, rid)
			starSave(acc, map[int32]int{rid: store.StarCollected})
			continue
		case m.Direction == gcp.S2C && m.Opcode == scene.OpPlayActsNotify:
			// 同一个通知里既有区域进/出(选层),也有 AOI 实体进/离(星星收集判定)。
			starObserve(m.Session, acc, m.AppBody, lastPos[acc])
			// 区域进/出:玩家真正踩进/离开区域触发体(3D 体积)时服务器才下发,是选层的权威依据。
			acts := scene.ParseAreaActs(m.AppBody)
			if len(acts) == 0 {
				continue
			}
			for _, a := range acts {
				funcs := areasByConn[m.Session]
				if a.Enter {
					if funcs == nil {
						funcs = map[uint32]map[uint32]bool{}
						areasByConn[m.Session] = funcs
					}
					if funcs[a.FuncID] == nil {
						funcs[a.FuncID] = map[uint32]bool{}
					}
					funcs[a.FuncID][a.AreaID] = true
					continue
				}
				if funcs[a.FuncID] != nil {
					delete(funcs[a.FuncID], a.AreaID)
					if len(funcs[a.FuncID]) == 0 { // 该 func 下的区域都离开了,才算离开这一层
						delete(funcs, a.FuncID)
					}
				}
			}
			saveAreas(m.Session)
			// 层可能就此变了(尤其传送落地时的进入事件):此刻玩家可能站着不动、下一个移动包遥遥无期,
			// 故当场推一条**只更新分层**的消息(layerOnly),前端据此叠上/撤下切片图而不动位置锚点。
			settleLayer(m.Session, acc, m.Time)
			continue
		case m.Direction == gcp.C2S && m.Opcode == scene.OpSceneMoveReq:
			mr, ok := scene.ParseMoveReq(m.AppBody)
			if !ok {
				continue
			}
			// 逐包推送,不节流:客户端本就只在操作变化时上报(约 0.1s 一包,输入不变时退化成 2.5-3s
			// 心跳),峰值仅约 8 条/秒。丢包会丢掉转向事件,前端外推便会偏出去(见 buildPos 的 vu/vv)。
			res := sceneByConn[m.Session]
			if res == 0 { // 未知 res(中途开抓/无缓存):用移动包的 scene_cfg_id 兜底默认 res
				res = db.DefaultSceneRes(mr.SceneCfgID)
			}
			pos := buildPos(db, acc, res, roomByConn[m.Session], mr, m.Time)
			// 分层地图:玩家当前所在区域(服务器区域进/出事件维护)命中某层的 area_func_id 即在该层,
			// 经 layerDebounce 去抖(滤掉走动中擦出/擦进触发体接缝的百毫秒级抖动)。见 docs/data.md 3.2。
			if l, ok := layerOf(m.Session, res, m.Time, true); ok {
				if lp := layerPayload(db, res, l); lp != nil {
					pos["sceneName"] = l.Name
					pos["layer"] = lp
				}
			}
			lastPos[acc] = pos
			srv.SetLastPosition(acc, pos) // 缓存供地图页加载即时回显
			srv.Hub().Broadcast("position", acc, pos)
			// 玩家走到哪,就把周围的星星判一遍(走近了却没实体 ⇒ 已收集)。
			starSweep(m.Session, acc, res, mr.Pos.X, mr.Pos.Y, m.Time)
			continue
		}

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
		pageT0 := time.Now() // 本页处理起点(解析+入库),累计入 sw.proc 以衡量实际处理耗时
		res := pet.ParsePetListRsp(m.AppBody)
		// 本页页号取 req_page(响应回显所请求页,登录时依次为 1..TotalPage);
		// page_num 实为每页容量(实测恒为 50),不是页序,不能用作累积接续判据。
		page := res.ReqPage
		sw := sweeps[acc]
		if sw == nil || !sw.valid || page != sw.nextPage { // 无法接续上一页则从本页重开(仅 page 1 起算有效)
			sw = &petSweep{gids: map[uint32]bool{}, valid: page <= 1, start: pageT0}
			sweeps[acc] = sw
		}
		for _, pd := range res.Pets {
			p := pet.ToPet(pd, db)
			sw.gids[p.Gid] = true // 无论 upsert 成败都视为"仍拥有",避免对账误删
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
		sw.nextPage = page + 1
		sw.proc += time.Since(pageT0) // 累计本页实际处理耗时(不含等待客户端下一页的空档)
		// 末页:先据完整快照清除库中已不存在(玩家在别处放生/赠送)的宠物(否则残留为"位置待同步"),
		// 再汇总本轮:请求耗时=首页到末页的墙钟跨度(含客户端分页节奏),解析耗时=纯处理累计。
		// 二者背离即暴露问题:处理耗时接近/超过请求跨度,说明处理速度赶不上抓包到达而积压。
		if sw.valid && res.TotalPage > 0 && page >= res.TotalPage {
			pruneT0 := time.Now()
			if stale, err := sc.PruneMissingPets(sw.gids, sw.start.Unix()); err == nil && len(stale) > 0 {
				log.Printf("用户 %s 对账清除 %d 只已不在仓库的宠物", acc, len(stale))
				srv.Hub().Broadcast("pet", acc, map[string]any{"locUpdate": true})
			}
			sw.proc += time.Since(pruneT0)
			log.Printf("用户 %s 宠物同步完成: %d 只 %d 页, 请求耗时 %v, 解析耗时 %v",
				acc, len(sw.gids), res.TotalPage, time.Since(sw.start), sw.proc)
			delete(sweeps, acc) // 本轮结束,防止后续单独请求某页复用旧累积
		}
	}
}

// logEvent 打印一条获得宠物事件日志。
// sceneDisplayName 取当前场景显示名:优先 scene_res(区分同一 cfg 下的子场景,如卡洛西亚
// 大陆 vs 魔法学院),缺失时(未见进入/传送通知)回退移动包自带的 scene_cfg_id。
func sceneDisplayName(db *gamedata.DB, resID, cfgID int32) string {
	if resID != 0 {
		if n := db.SceneResName(resID); n != "" {
			return n
		}
	}
	return db.SceneName(cfgID)
}

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
