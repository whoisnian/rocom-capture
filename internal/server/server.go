// Package server 提供 REST API、SSE 实时推送，并 embed 前端静态资源。
package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/whoisnian/rocom-capture/internal/gamedata"
	"github.com/whoisnian/rocom-capture/internal/pet"
	"github.com/whoisnian/rocom-capture/internal/store"
)

//go:embed all:web
var webFS embed.FS

// Server 聚合存储、广播中心与路由。
type Server struct {
	store       *store.Store
	hub         *Hub
	mux         *http.ServeMux
	db          *gamedata.DB
	opcodeNames map[uint16]string
	medals      []gamedata.MedalEntry
	medalIDs    map[string][]uint32 // 奖牌名 -> id 列表(同名多枚时全含),用于把筛选名解析为 id
	icons       iconMeta

	posMu   sync.Mutex                // 保护 lastPos
	lastPos map[string]map[string]any // 账号 -> 最近一次位置(实时地图页加载时即时回显,不必等下一次移动)
}

// iconMeta 是全局固定图标(每只宠物都一样,不随宠物下发):六维属性小图 + 异色/炫彩/污染标记图。
// 前端一次性拉取(GET /api/icons),供六维栏与标记徽标渲染。
type iconMeta struct {
	Stat          map[string]string `json:"stat"` // hp/attack/spAttack/defense/spDefense/speed -> 相对路径
	Type          map[string]string `json:"type"` // 系别中文名 -> 图标路径(筛选按钮用)
	Shiny         string            `json:"shiny,omitempty"`
	Colorful      string            `json:"colorful,omitempty"`
	ShinyColorful string            `json:"shinyColorful,omitempty"`
	Pollution     string            `json:"pollution,omitempty"`
	PartnerFrame  string            `json:"partnerFrame,omitempty"` // 搭档标记徽章橙色外框底(img_collect)
}

// New 创建 HTTP 服务。
func New(st *store.Store, hub *Hub, db *gamedata.DB) *Server {
	s := &Server{store: st, hub: hub, mux: http.NewServeMux(), db: db, opcodeNames: db.OpcodeNames(), medals: db.AllMedals()}
	s.lastPos = map[string]map[string]any{}
	s.medalIDs = map[string][]uint32{}
	for _, m := range s.medals {
		s.medalIDs[m.Name] = append(s.medalIDs[m.Name], m.ID)
	}
	// 六维编号 1-6:1生命 2物攻 3魔攻 4物防 5魔防 6速度(与 pet.ToPet 六维顺序一致)。
	s.icons = iconMeta{
		Stat: map[string]string{
			"hp":        db.AttributeTypeIcon(1),
			"attack":    db.AttributeTypeIcon(2),
			"spAttack":  db.AttributeTypeIcon(3),
			"defense":   db.AttributeTypeIcon(4),
			"spDefense": db.AttributeTypeIcon(5),
			"speed":     db.AttributeTypeIcon(6),
		},
		Type:          db.SkillDamTypeIcons(),
		Shiny:         db.StaticIcon("shiny"),
		Colorful:      db.StaticIcon("colorful"),
		ShinyColorful: db.StaticIcon("shiny_colorful"),
		Pollution:     db.StaticIcon("pollution"),
		PartnerFrame:  db.StaticIcon("partner_frame"),
	}
	s.routes()
	return s
}

// Hub 返回广播中心。
func (s *Server) Hub() *Hub { return s.hub }

// OpcodeName 返回 opcode 的可读名称。
func (s *Server) OpcodeName(op uint16) string {
	if n, ok := s.opcodeNames[op]; ok {
		return n
	}
	return fmt.Sprintf("UNKNOWN_0x%04X", op)
}

// Handler 返回 http.Handler。
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/pets", s.handlePets)
	s.mux.HandleFunc("GET /api/pets/{gid}", s.handlePet)
	s.mux.HandleFunc("GET /api/events", s.handleEvents)
	s.mux.HandleFunc("GET /api/events/count", s.handleEventCount)
	s.mux.HandleFunc("DELETE /api/events", s.handleClearEvents)
	s.mux.HandleFunc("GET /api/filter-options", s.handleFilterOptions)
	s.mux.HandleFunc("GET /api/stats", s.handleStats)
	s.mux.HandleFunc("GET /api/medals", s.handleMedals)
	s.mux.HandleFunc("GET /api/name-options", s.handleNameOptions)
	s.mux.HandleFunc("GET /api/icons", s.handleIcons)
	s.mux.HandleFunc("GET /api/boxes", s.handleBoxes)
	s.mux.HandleFunc("GET /api/teams", s.handleTeams)
	s.mux.HandleFunc("GET /api/evolution", s.handleEvolution)
	s.mux.HandleFunc("GET /api/pet-page", s.handlePetPage)
	s.mux.HandleFunc("GET /api/accounts", s.handleAccounts)
	s.mux.HandleFunc("GET /api/position", s.handlePosition)
	s.mux.HandleFunc("GET /api/stream", s.handleStream)
	// 宠物图片(embed 的 webp,路径如 /img/HeadIcon/3001.webp);长缓存,内容随版本变更。
	imgFS := http.FileServerFS(gamedata.ImageFS())
	s.mux.Handle("GET /img/", http.StripPrefix("/img/", cacheControl(imgFS, "public, max-age=86400")))
	s.mux.HandleFunc("/", s.handleStatic)
}

// cacheControl 给静态资源加 Cache-Control 头。
func cacheControl(h http.Handler, v string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", v)
		h.ServeHTTP(w, r)
	})
}

// acct 返回请求指向的账号:优先 ?account=,缺省回退最近活跃账号(库空则空串)。
func (s *Server) acct(r *http.Request) string {
	if a := r.URL.Query().Get("account"); a != "" {
		return a
	}
	if accs, err := s.store.ListAccounts(); err == nil && len(accs) > 0 {
		return accs[0].Account // ListAccounts 按 updated_at 倒序,取最近
	}
	return ""
}

// SetLastPosition 缓存某账号最近一次实时位置(由抓包消费循环在广播 position 时调用),
// 供实时地图页加载时经 GET /api/position 即时回显,不必等玩家下一次移动。
func (s *Server) SetLastPosition(account string, pos map[string]any) {
	if account == "" {
		return
	}
	s.posMu.Lock()
	s.lastPos[account] = pos
	s.posMu.Unlock()
}

// handlePosition 返回当前账号最近一次位置(实时地图页初始定位);无记录返回 null。
// 缓存位置可能已是很久以前的(玩家早已停下/离线),前端据 vu/vv 外推会一路飘走,
// 故过期(超过 posFresh)就抹掉速度:页面加载先静态回显,下一个移动包到达后自然接管。
func (s *Server) handlePosition(w http.ResponseWriter, r *http.Request) {
	acc := s.acct(r)
	s.posMu.Lock()
	pos := s.lastPos[acc]
	s.posMu.Unlock()
	if ts, ok := pos["tsMs"].(int64); ok && time.Since(time.UnixMilli(ts)) > posFresh {
		stale := make(map[string]any, len(pos))
		maps.Copy(stale, pos)
		delete(stale, "vu")
		delete(stale, "vv")
		delete(stale, "path") // 陈旧轨迹不该再回放一遍
		pos = stale
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(pos) // pos 为 nil 时输出 null
}

// posFresh 是缓存位置仍可用于外推的时限(客户端移动中最长 3s 一个心跳包,留些余量)。
const posFresh = 4 * time.Second

// handleAccounts 返回已知账号列表(account/name/petCount),供前端账号切换下拉。
func (s *Server) handleAccounts(w http.ResponseWriter, r *http.Request) {
	accs, err := s.store.ListAccounts()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if accs == nil {
		accs = []store.AccountInfo{}
	}
	writeJSON(w, accs)
}

// handleMedals 返回全部奖牌(id/name/desc/icon),供宠物详情奖牌墙展示。
func (s *Server) handleMedals(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.medals)
}

// handleNameOptions 返回全量特长名(gamedata 全表,非按账号),供事件页高亮规则点选。
func (s *Server) handleNameOptions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string][]string{"speciality": s.db.AllSpecialities()})
}

// handleIcons 返回全局固定图标(六维属性小图 + 异色/炫彩/污染标记图),供前端一次性缓存。
func (s *Server) handleIcons(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.icons)
}

// handleBoxes 返回各盒子的槽位布局,供宠物列表左侧盒子示意图。
func (s *Server) handleBoxes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.store.For(s.acct(r)).BoxLayouts())
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

// parseFilter 从查询参数构造 store.Filter(handlePets/handlePetPage 共用)。
// 奖牌按名筛选,这里将奖牌名解析为 id 列表(pet_medal 存 id),同名多枚时全含。
func (s *Server) parseFilter(q url.Values) store.Filter {
	atoi := func(k string) int { n, _ := strconv.Atoi(q.Get(k)); return n }
	atoi64 := func(k string) int64 { n, _ := strconv.ParseInt(q.Get(k), 10, 64); return n }
	f := store.Filter{
		Search:      q.Get("search"),
		Nature:      q.Get("nature"),
		Gender:      q.Get("gender"),
		TalentRank:  q.Get("talentRank"),
		MedalIDs:    s.medalIDs[q.Get("medal")],
		Speciality:  q.Get("speciality"),
		EggGroup:    q.Get("eggGroup"),
		PartnerMark: q.Get("partnerMark"),
		Shiny:       q.Get("shiny"),
		Colorful:    q.Get("colorful"),
		Form:        q.Get("form"),
		Box:         q.Get("box"),
		CatchAfter:  atoi64("catchAfter"),
		LevelMin:    atoi("levelMin"),
		LevelMax:    atoi("levelMax"),
		Sort:        q.Get("sort"),
		Order:       q.Get("order"),
		Page:        atoi("page"),
		PageSize:    atoi("pageSize"),
	}
	if t := q.Get("types"); t != "" {
		f.Types = strings.Split(t, ",")
	}
	if ne := q.Get("natureExclude"); ne != "" {
		f.NatureExclude = strings.Split(ne, ",")
	}
	return f
}

// handleTeams 返回大世界三队的 18 格布局,供盒子示意图。
func (s *Server) handleTeams(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.store.For(s.acct(r)).TeamLayouts())
}

// handleEvolution 返回某 petbase(base_conf_id)所属进化链(按阶段升序),供详情页展示。
func (s *Server) handleEvolution(w http.ResponseWriter, r *http.Request) {
	base, _ := strconv.ParseUint(r.URL.Query().Get("base"), 10, 32)
	chain := s.db.EvolutionChain(uint32(base))
	if chain == nil {
		chain = []gamedata.ChainStep{}
	}
	writeJSON(w, chain)
}

// handlePetPage 返回某宠物在当前筛选+排序下所处的页码,供盒子示意图点击跳页。
func (s *Server) handlePetPage(w http.ResponseWriter, r *http.Request) {
	gid, _ := strconv.ParseUint(r.URL.Query().Get("gid"), 10, 32)
	page, found := s.store.For(s.acct(r)).PetPage(uint32(gid), s.parseFilter(r.URL.Query()))
	writeJSON(w, map[string]any{"page": page, "found": found})
}

func (s *Server) handlePets(w http.ResponseWriter, r *http.Request) {
	f := s.parseFilter(r.URL.Query())
	pets, total, err := s.store.For(s.acct(r)).ListPets(f)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if pets == nil {
		pets = []*pet.Pet{}
	}
	pet.FillSizePercentile(s.db, pets...) // 读取时注入身高/体重范围与百分位(静态参考,不入库)
	writeJSON(w, map[string]any{"total": total, "pets": pets})
}

func (s *Server) handlePet(w http.ResponseWriter, r *http.Request) {
	gid, _ := strconv.ParseUint(r.PathValue("gid"), 10, 32)
	p, err := s.store.For(s.acct(r)).GetPet(uint32(gid))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if p == nil {
		http.Error(w, "not found", 404)
		return
	}
	pet.FillSizePercentile(s.db, p)
	writeJSON(w, p)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	beforeID, _ := strconv.Atoi(q.Get("beforeId"))
	events, err := s.store.For(s.acct(r)).ListEvents(limit, beforeID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if events == nil {
		events = []*store.Event{}
	}
	// 补注体重/身高百分位(供事件页体重/声音高亮规则按百分位判定;历史事件也据当前 gamedata 刷新)。
	for _, ev := range events {
		if ev.Pet != nil {
			pet.FillSizePercentile(s.db, ev.Pet)
		}
	}
	writeJSON(w, events)
}

// handleEventCount 返回事件总数,供前端展示「累计获得宠物数」(失去事件不入库)。
func (s *Server) handleEventCount(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.For(s.acct(r)).CountEvents()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{"count": n})
}

// handleClearEvents 清空事件历史。
func (s *Server) handleClearEvents(w http.ResponseWriter, r *http.Request) {
	if err := s.store.For(s.acct(r)).ClearEvents(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleFilterOptions(w http.ResponseWriter, r *http.Request) {
	sc := s.store.For(s.acct(r))
	opts := sc.FilterOptions()
	// 奖牌下拉:按「拥有」筛选,列出本账号宠物拥有过的奖牌名(id→名,去重,保持 id 升序)。
	var names []string
	seen := map[string]bool{}
	for _, id := range sc.OwnedMedalIDs() {
		if m, ok := s.db.Medal(id); ok && m.Name != "" && !seen[m.Name] {
			seen[m.Name] = true
			names = append(names, m.Name)
		}
	}
	opts["medal"] = names
	writeJSON(w, opts)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	count, _ := s.store.For(s.acct(r)).CountPets()
	writeJSON(w, map[string]any{"petCount": count})
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// 该连接只接收当前账号(?account=,缺省回退最近活跃账号)的消息;account 为空的全局消息始终放行。
	account := s.acct(r)
	// 高频的 debug(逐条 opcode)流量默认不推送,仅调试页显式 ?debug=1 时才发,避免其它页面白拉。
	wantDebug := r.URL.Query().Get("debug") == "1"

	ch := s.hub.subscribe()
	defer s.hub.unsubscribe(ch)
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if msg.account != "" && msg.account != account {
				continue // 非当前账号
			}
			if msg.typ == "debug" && !wantDebug {
				continue // 未订阅调试流
			}
			fmt.Fprintf(w, "data: %s\n\n", msg.data)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	sub, _ := fs.Sub(webFS, "web")
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if f, err := sub.Open(path); err == nil {
		f.Close()
		http.ServeFileFS(w, r, sub, path)
		return
	}
	// SPA fallback
	http.ServeFileFS(w, r, sub, "index.html")
}
