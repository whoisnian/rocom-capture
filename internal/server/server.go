// Package server 提供 REST API、SSE 实时推送,并 embed 前端静态资源。
// 文件划分:api_pets(宠物/事件/筛选)/ api_map(实时地图)/ stream(SSE)/ hub(广播中心)。
package server

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
	"sync"

	"github.com/whoisnian/rocom-capture/internal/gamedata"
	"github.com/whoisnian/rocom-capture/internal/store"
)

//go:embed all:web
var webFS embed.FS

// Server 聚合存储、广播中心与路由。
type Server struct {
	store    *store.Store
	hub      *Hub
	mux      *http.ServeMux
	db       *gamedata.DB
	medals   []gamedata.MedalEntry
	medalIDs map[string][]uint32 // 奖牌名 -> id 列表(同名多枚时全含),用于把筛选名解析为 id
	icons    iconMeta

	posMu    sync.Mutex                // 保护 lastPos / lastWild / lastHome / visitFlowers
	lastPos  map[string]map[string]any // 账号 -> 最近一次位置(实时地图页加载时即时回显,不必等下一次移动)
	lastWild map[string]any            // 账号 -> 最近一次野生宠物标记(同上,免得进页面要等下一条 AOI 通知)
	lastHome map[string]any            // 账号 -> 最近一次家园小窝图层(同上;不在家园时为空列表)
	// 账号 -> 正在参观的那个世界的花种(见 api_flowers.go)。只在内存里:参观是临时状态,
	// 离开即弃,绝不能混进自己那套(库里的 flower_seed)。
	visitFlowers map[string]visitFlowerSet

	paint paintState // 涂地覆盖位图(自带锁,见 paint.go)

	opts Options
}

// Options 是经启动参数交给前端的页面配置(GET /api/config)。
type Options struct {
	// PetsURL 是本机桌宠 rocom-pets 的本地监听(如 http://127.0.0.1:47831)。由**看页面的那台浏览器**
	// 去连,127.0.0.1 指的是浏览器所在的机器而不是网关。空 = 前端不探测,炫彩色卡只跳 rkpet。
	PetsURL string `json:"petsURL,omitempty"`
	// PetsToken 是 rocom-pets 本地接口的鉴权令牌,浏览器调它时放进 `Authorization: Bearer`。
	// 只能下发给页面(调用方是浏览器),所以能打开本页的人都看得到它;桌宠监听在 127.0.0.1 时,
	// 令牌只对那台电脑自己的浏览器有用。
	PetsToken string `json:"petsToken,omitempty"`
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
func New(st *store.Store, hub *Hub, db *gamedata.DB, opts Options) *Server {
	s := &Server{store: st, hub: hub, mux: http.NewServeMux(), db: db, medals: db.AllMedals(), opts: opts}
	s.lastPos = map[string]map[string]any{}
	s.lastWild = map[string]any{}
	s.lastHome = map[string]any{}
	s.visitFlowers = map[string]visitFlowerSet{}
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
	s.mux.HandleFunc("GET /api/config", s.handleConfig)
	s.mux.HandleFunc("GET /api/boxes", s.handleBoxes)
	s.mux.HandleFunc("GET /api/teams", s.handleTeams)
	s.mux.HandleFunc("GET /api/evolution", s.handleEvolution)
	s.mux.HandleFunc("GET /api/pet-page", s.handlePetPage)
	s.mux.HandleFunc("GET /api/accounts", s.handleAccounts)
	s.mux.HandleFunc("GET /api/position", s.handlePosition)
	s.mux.HandleFunc("GET /api/pois", s.handlePois)
	s.mux.HandleFunc("GET /api/wildpets", s.handleWildPets)
	s.mux.HandleFunc("GET /api/flowers", s.handleFlowers)
	s.mux.HandleFunc("GET /api/paint", s.handlePaint)
	s.mux.HandleFunc("DELETE /api/paint", s.handlePaintReset)
	s.mux.HandleFunc("GET /api/home", s.handleHome)
	s.mux.HandleFunc("GET /api/eggs", s.handleEggs)
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

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
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
