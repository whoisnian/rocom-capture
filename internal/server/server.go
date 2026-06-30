// Package server 提供 REST API、SSE 实时推送，并 embed 前端静态资源。
package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"

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
	opcodeNames map[uint16]string
	medals      []gamedata.MedalEntry
}

// New 创建 HTTP 服务。
func New(st *store.Store, hub *Hub, opcodeNames map[uint16]string, medals []gamedata.MedalEntry) *Server {
	s := &Server{store: st, hub: hub, mux: http.NewServeMux(), opcodeNames: opcodeNames, medals: medals}
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
	s.mux.HandleFunc("DELETE /api/events", s.handleClearEvents)
	s.mux.HandleFunc("GET /api/filter-options", s.handleFilterOptions)
	s.mux.HandleFunc("GET /api/stats", s.handleStats)
	s.mux.HandleFunc("GET /api/medals", s.handleMedals)
	s.mux.HandleFunc("GET /api/boxes", s.handleBoxes)
	s.mux.HandleFunc("GET /api/teams", s.handleTeams)
	s.mux.HandleFunc("GET /api/pet-page", s.handlePetPage)
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

// handleMedals 返回全部奖牌(id/name/desc),供宠物详情奖牌墙展示。
func (s *Server) handleMedals(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.medals)
}

// handleBoxes 返回各盒子的槽位布局,供宠物列表左侧盒子示意图。
func (s *Server) handleBoxes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.store.BoxLayouts())
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

// parseFilter 从查询参数构造 store.Filter(handlePets/handlePetPage 共用)。
func parseFilter(q url.Values) store.Filter {
	atoi := func(k string) int { n, _ := strconv.Atoi(q.Get(k)); return n }
	f := store.Filter{
		Search:      q.Get("search"),
		Nature:      q.Get("nature"),
		Gender:      q.Get("gender"),
		TalentRank:  q.Get("talentRank"),
		Medal:       q.Get("medal"),
		Speciality:  q.Get("speciality"),
		PartnerMark: q.Get("partnerMark"),
		Shiny:       q.Get("shiny"),
		Colorful:    q.Get("colorful"),
		Box:         q.Get("box"),
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
	writeJSON(w, s.store.TeamLayouts())
}

// handlePetPage 返回某宠物在当前筛选+排序下所处的页码,供盒子示意图点击跳页。
func (s *Server) handlePetPage(w http.ResponseWriter, r *http.Request) {
	gid, _ := strconv.ParseUint(r.URL.Query().Get("gid"), 10, 32)
	page := s.store.PetPage(uint32(gid), parseFilter(r.URL.Query()))
	writeJSON(w, map[string]int{"page": page})
}

func (s *Server) handlePets(w http.ResponseWriter, r *http.Request) {
	f := parseFilter(r.URL.Query())
	pets, total, err := s.store.ListPets(f)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if pets == nil {
		pets = []*pet.Pet{}
	}
	writeJSON(w, map[string]any{"total": total, "pets": pets})
}

func (s *Server) handlePet(w http.ResponseWriter, r *http.Request) {
	gid, _ := strconv.ParseUint(r.PathValue("gid"), 10, 32)
	p, err := s.store.GetPet(uint32(gid))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if p == nil {
		http.Error(w, "not found", 404)
		return
	}
	writeJSON(w, p)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	beforeID, _ := strconv.Atoi(q.Get("beforeId"))
	events, err := s.store.ListEvents(limit, beforeID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if events == nil {
		events = []*store.Event{}
	}
	writeJSON(w, events)
}

// handleClearEvents 清空事件历史。
func (s *Server) handleClearEvents(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ClearEvents(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleFilterOptions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.store.FilterOptions())
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	count, _ := s.store.CountPets()
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
			fmt.Fprintf(w, "data: %s\n\n", msg)
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
