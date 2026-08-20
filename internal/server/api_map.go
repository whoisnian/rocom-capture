package server

import (
	"maps"
	"net/http"
	"sort"
	"strconv"
	"time"
)

// posFresh 是缓存位置仍可用于外推的时限(客户端移动中最长 3s 一个心跳包,留些余量)。
const posFresh = 4 * time.Second

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
	writeJSON(w, pos) // pos 为 nil 时输出 null
}

// SetLastWildPets 缓存某账号最近一次野生宠物标记(由消费管线在广播 wildpets 时调用),
// 供实时地图页加载时经 GET /api/wildpets 即时回显。
func (s *Server) SetLastWildPets(account string, payload any) {
	if account == "" {
		return
	}
	s.posMu.Lock()
	s.lastWild[account] = payload
	s.posMu.Unlock()
}

// handleWildPets 返回当前账号最近一次野生宠物标记
// (异色/炫彩、污染、体重/嗓音奖牌窗口及 MAX);无记录返回 null。
// 与位置不同,这里不做过期抹除:标记本身已带 stale 标志(实体离开 AOI 后由管线置位并限时保留)。
func (s *Server) handleWildPets(w http.ResponseWriter, r *http.Request) {
	s.posMu.Lock()
	v := s.lastWild[s.acct(r)]
	s.posMu.Unlock()
	writeJSON(w, v)
}

// SetLastHome 缓存某账号最近一次家园小窝图层(由消费管线在广播 home 时调用),
// 供实时地图页加载时经 GET /api/home 即时回显。玩家不在家园时缓存的是空列表。
func (s *Server) SetLastHome(account string, payload any) {
	if account == "" {
		return
	}
	s.posMu.Lock()
	s.lastHome[account] = payload
	s.posMu.Unlock()
}

// handleHome 返回当前账号最近一次家园小窝图层(空窝也在内);无记录返回 null。
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	s.posMu.Lock()
	v := s.lastHome[s.acct(r)]
	s.posMu.Unlock()
	writeJSON(w, v)
}

// poiKind 是一个 POI 图层(前端的一个开关):图层键、中文名、图标路径、是否默认开启。
type poiKind struct {
	K       string `json:"k"`
	N       string `json:"n"`
	Icon    string `json:"icon"`    // /img/<此路径>
	On      bool   `json:"on"`      // 默认开启(魔力之源、炼金釜)
	Num     int    `json:"num"`     // 本场景该图层的点数(前端显示,0 则该层置灰)
	Collect bool   `json:"collect"` // 可收集图层(眠枭之星/不咕钟零件):前端「收集模式」覆盖这些层
}

// poiPoint 是一个 POI 标记:底图归一化坐标(与玩家位置同一投影)+ 名称。
// 眠枭之星另带刷新点 id、候选区域与收集状态(见 docs/data.md 3.4)。
type poiPoint struct {
	K    string  `json:"k"`
	U    float64 `json:"u"`
	V    float64 `json:"v"`
	N    string  `json:"n"`
	R    int32   `json:"r,omitempty"`    // 刷新点 id(星星:前端据此接收状态增量)
	Zone []int32 `json:"zone,omitempty"` // 候选区域营地 id 列表;全部收满才可隐藏(重叠带语义见 docs/data.md 3.4)
	St   int     `json:"st,omitempty"`   // 收集状态:0 未确认 / 1 未收集 / 2 已收集
}

// zoneProgress 是某区域的眠枭之星收集进度(服务器口径,合并同区域的独立星/光点/石像)。
type zoneProgress struct {
	Camp int32  `json:"camp"` // 区域键(营地 id),与 poiPoint.Zone 的元素对应
	Name string `json:"name"` // 区域名(商店街周边…)
	Got  int32  `json:"got"`
	Tot  int32  `json:"tot"`
}

// handlePois 返回某场景(?res=scene_res_cfg_id)的大地图 POI:图层清单 + 已投影到底图归一化坐标
// 的标记点。投影复用 db.Project(与玩家位置同一套),故前端不需要知道 ox/oy/side。
// 无底图的场景没有 POI(投影无从谈起),返回空列表。数据来源见 docs/data.md 3.3。
func (s *Server) handlePois(w http.ResponseWriter, r *http.Request) {
	res64, err := strconv.ParseUint(r.URL.Query().Get("res"), 10, 32)
	if err != nil {
		http.Error(w, "bad res", http.StatusBadRequest)
		return
	}
	res := uint32(res64)
	acc := s.acct(r)
	states := s.store.StarStates(acc) // 刷新点 -> 收集状态(逐点确认的结果)
	pts := []poiPoint{}
	num := map[string]int{}
	for _, p := range s.db.POIs(res) {
		u, v, ok := s.db.Project(res, p.X, p.Y)
		if !ok {
			continue
		}
		pt := poiPoint{K: p.K, U: u, V: v, N: p.N}
		if s.db.CollectibleKind(p.K) {
			pt.R, pt.Zone, pt.St = p.R, p.Zone, states[p.R]
		}
		pts = append(pts, pt)
		num[p.K]++
	}
	kinds := []poiKind{}
	for _, k := range s.db.POIKinds() {
		kinds = append(kinds, poiKind{K: k.K, N: k.N, Icon: s.db.POIIcon(k), On: k.On, Num: num[k.K], Collect: k.Collect})
	}
	// 按区域的收集进度(服务器口径):点的候选区域全部收满 ⇒ 可隐藏(见 poiPoint.Z)。
	agg := map[int32]*zoneProgress{}
	for _, z := range s.store.StarZones(acc) {
		e := agg[z.Camp]
		if e == nil {
			e = &zoneProgress{Camp: z.Camp, Name: s.db.ZoneName(z.Camp)}
			agg[z.Camp] = e
		}
		e.Got += z.Got
		e.Tot += z.Total
	}
	zones := []zoneProgress{}
	for _, z := range agg {
		zones = append(zones, *z)
	}
	sort.Slice(zones, func(i, j int) bool { return zones[i].Camp < zones[j].Camp })
	writeJSON(w, map[string]any{"kinds": kinds, "pois": pts, "zones": zones})
}
