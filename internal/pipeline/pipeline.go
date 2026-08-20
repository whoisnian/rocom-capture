// Package pipeline 消费抓包引擎解密出的应用层消息流:归属账号、更新宠物库、产生获得事件,
// 维护实时地图与眠枭之星状态,并经 server 广播给前端。
// 文件划分:pets(宠物入库/事件)/ position(实时地图/分层)/ stars(眠枭之星判定)。
package pipeline

import (
	"fmt"
	"log"
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

// grace 是「初始快照」的判定余量(秒):add_time 早于服务启动前 grace 的宠物视为存量仓库,
// 不产生获得事件;服务运行期间新捕捉的宠物 add_time≈当前,才推事件。
const grace = 120

// Pipeline 持有消费循环的全部依赖与状态。非并发安全:所有状态只在 Run 的单 goroutine 内读写。
type Pipeline struct {
	st      *store.Store
	db      *gamedata.DB
	srv     *server.Server
	startTS int64

	// connAccount: GCP 连接(connID)→账号("UID:"+user_id)。同一客户端 IP 可能同时跑多个
	// 账号(不同设备经 NAT 同 IP、或不同游戏服),故按 user_id 而非 IP 归属:抓到某连接的
	// LOGIN_RSP 时解析 user_id 建映射。登录回包自身也带背包/队伍/奖牌快照,须先登记再归属。
	// 从库中预热已知映射:配合会话密钥缓存,抓包服务重启后无需再等登录回包即可归属消息。
	connAccount map[string]string
	conns       map[string]*connState
	accts       map[string]*acctState
}

// connState 是单条 GCP 连接的实时地图状态。场景 res 与区域只在切场景/跨触发体时下发、
// 游戏中途不重发,故经 store 的 sessions 表落盘,重启后预热恢复(否则虽能解密移动包,
// 却因不知当前 res 而无法定位底图——移动包只带 scene_cfg_id)。
type connState struct {
	res  int32 // 当前 scene_res_cfg_id(s2c 进入/传送更新;0=未知)
	room int32 // 家园房屋等级(家园室内选分层底图,非家园为 0)
	// areas: area_func_id → 该 func 下已进入的 area_id 集合。由服务器的区域进/出事件维护
	// (见 scene.ParseAreaActs),是「玩家当前在洞穴/几楼」的权威依据。一个 func 可含多个
	// area(如信仰者村落一层同时进入 541030265/541030499),故按 func 存 area 集合:
	// 离开其中一个仍在该层,集合空了才算离开。
	areas      map[uint32]map[uint32]bool
	layer      *layerState               // 分层地图去抖状态(见 layerDebounce)
	stars      *starTracker              // 眠枭之星观测态(换场景/传送即重置)
	wilds      *wildTracker              // 当前场景的野生宠物图层观测态(见 wildpets.go)
	wildByRes  map[int32]*wildTracker    // 按 scene_res_id 保留的观测态,返回场景时恢复
	pos        scene.Position            // 最近一次移动包/传送落点的玩家世界坐标(涂地要从这儿画到宠物那儿)
	wildSeen   map[uint64]scene.Position // 当前 AOI 里**全部**野生宠实体的位置(涂地用,不只稀有那几只)
	pendantRid int32                     // 最近一次挂件交互(0x0272)的刷新行 id,等回包(0x0273)确认
	home       *homeState                // 家园小窝图层状态(仅在家园场景内非空,见 home.go)
	crackEgg   uint32                    // 最近一次破壳请求(0x030b)的 egg_gid,回包确认后把这颗蛋删掉
}

// acctState 是单个账号的消费状态。
type acctState struct {
	sweep *petSweep // 正在累积的分页宠物列表快照(末页对账,见 petSweep)
	// zoneGot: camp → 该区已收集数合计(服务器口径,各形态相加);nil=尚未从库预热。
	// 用作星星判定的守卫(见 sweepStars)。
	zoneGot map[int32]int32
	// starKnown: 已确认的星星状态(库内快照,只写增量);nil=尚未从库预热。
	starKnown map[int32]int
	eggSweep  *eggSweep      // 正在累积的分页背包快照(末页对账,见 eggs.go)
	lastPos   map[string]any // 最近推送的位置载荷(layerOnly 更新时合并回缓存)
}

// New 创建消费管线并从库中预热连接归属/场景状态(抓包服务重启后无缝续接)。
func New(st *store.Store, db *gamedata.DB, srv *server.Server) *Pipeline {
	p := &Pipeline{
		st: st, db: db, srv: srv,
		startTS:     time.Now().Unix() - grace,
		connAccount: map[string]string{},
		conns:       map[string]*connState{},
		accts:       map[string]*acctState{},
	}
	if saved, err := st.LoadSessionAccounts(); err == nil {
		p.connAccount = saved
	}
	if saved, err := st.LoadSessionScenes(); err == nil {
		for id, s := range saved {
			cs := p.conn(id)
			cs.res, cs.room = s.Res, s.Room
			for fn, ids := range s.Areas {
				set := map[uint32]bool{}
				for _, a := range ids {
					set[a] = true
				}
				if len(set) > 0 {
					if cs.areas == nil {
						cs.areas = map[uint32]map[uint32]bool{}
					}
					cs.areas[fn] = set
				}
			}
		}
	}
	return p
}

// conn 返回(必要时创建)某连接的状态。
func (p *Pipeline) conn(id string) *connState {
	cs := p.conns[id]
	if cs == nil {
		cs = &connState{}
		p.conns[id] = cs
	}
	return cs
}

// acct 返回(必要时创建)某账号的状态。
func (p *Pipeline) acct(acc string) *acctState {
	as := p.accts[acc]
	if as == nil {
		as = &acctState{}
		p.accts[acc] = as
	}
	return as
}

// Run 消费 eng.Out 直到通道关闭(离线回放结束时)。
func (p *Pipeline) Run(eng *capture.Engine) {
	for m := range eng.Out {
		p.handle(m)
	}
}

func (p *Pipeline) handle(m capture.Message) {
	// 登录回包:解析 user_id → 账号并登记 connID 映射(必须在下面归属 acc 之前)。
	if m.Direction == gcp.S2C && m.Opcode == pet.OpLoginRsp {
		p.registerLogin(m)
	}
	acc := p.connAccount[m.Session]

	// debug 页面:广播所有应用层消息,按来源账号归属(登录前无法归属的连接消息 acc="" 作全局)。
	// 订阅端据此只推当前账号的调试流;账号也放进 data 供页面列展示。
	p.srv.Hub().Broadcast("debug", acc, map[string]any{
		"time":    m.Time.Unix(),
		"dir":     m.Direction.String(),
		"opcode":  fmt.Sprintf("0x%04x", m.Opcode),
		"name":    p.srv.OpcodeName(m.Opcode),
		"account": acc,
	})
	if acc == "" {
		return // 尚未见到该连接的登录(无法归属 user_id),丢弃
	}

	// 去抖中的层变化需要「过一会儿再看一眼」才能采纳,而玩家可能站着不动、迟迟没有下一个移动包。
	// 故借该连接的任意一条消息(心跳等,实测约 1.6s 一条)把去抖推进到底。
	if cs := p.conns[m.Session]; cs != nil && cs.layer != nil && !cs.layer.since.IsZero() {
		p.settleLayer(m.Session, acc, m.Time)
	}

	// 精灵蛋与宠物、场景都相关(破壳回包同时带新宠物,收蛋通知也走奖励通道),
	// 故不参与「消费即返回」的分发,单独过一遍。
	p.handleEgg(m, acc)

	if p.handleScene(m, acc) {
		return
	}
	p.handlePet(m, acc)
}

// registerLogin 从登录回包解析 user_id/昵称,登记连接归属并落盘。
func (p *Pipeline) registerLogin(m capture.Message) {
	id, name, ok := pet.ParseLoginAccount(m.AppBody)
	if !ok {
		return
	}
	acc := "UID:" + strconv.FormatUint(id, 10)
	nick := name
	if nick == "" {
		nick = "?"
	}
	if p.connAccount[m.Session] != acc { // 同一登录会重复下发,仅首次记日志并落盘映射
		log.Printf("用户 %s (%s) 登录成功 [%s]", acc, nick, m.Session)
		p.st.SaveSessionAccount(m.Session, acc)
	}
	p.connAccount[m.Session] = acc
	if name == "" {
		name = acc
	}
	p.st.UpsertAccount(acc, name)
}

// PetTotal 返回全部账号的宠物数合计(离线回放结束时的汇总日志用)。
func (p *Pipeline) PetTotal() int {
	accs, _ := p.st.ListAccounts()
	n := 0
	for _, a := range accs {
		n += a.PetCount
	}
	return n
}

// AccountCount 返回已知账号数。
func (p *Pipeline) AccountCount() int {
	accs, _ := p.st.ListAccounts()
	return len(accs)
}

// catchWayName 由 catch_way 推断获得方式(实测:1=捕捉、3=孵蛋;其余未知归"获得")。
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
