// Package gamedata 提供从游戏解包数据(nrc/bin)提取的 id->中文名 查找表(编译期 embed)。
package gamedata

import (
	"embed"
	"encoding/json"
	"io/fs"
	"sort"
	"strconv"
)

//go:embed data/names.json
var namesJSON []byte

// 宠物图片(webp,由 scripts/gen_images.py 从 FModel PNG 转出);未生成时仅含占位 .gitkeep。
//
//go:embed all:data/img
var imageFS embed.FS

// ImageFS 返回 embed 的宠物图片文件系统,路径形如 HeadIcon/3001.webp(见 PetImage)。
func ImageFS() fs.FS {
	sub, err := fs.Sub(imageFS, "data/img")
	if err != nil {
		return imageFS
	}
	return sub
}

// Medal 是奖牌的名称与描述。
type Medal struct {
	Name string `json:"name"`
	Desc string `json:"desc"`
}

// imageEntry 是 petbase 形态的图片文件名(头像为数字,全身图去掉 JL_ 前缀)。
type imageEntry struct {
	H   string `json:"h"`   // 小头像文件名
	B   string `json:"b"`   // 大头像文件名
	P   string `json:"p"`   // 全身图拼音键(实际文件名为 JL_<p>)
	PS  string `json:"ps"`  // 全身缩略拼音键
	SH  string `json:"sh"`  // 异色小头像(形如 3010_1;仅有专属异色图者)
	SB  string `json:"sb"`  // 异色大头像
	SPS string `json:"sps"` // 异色全身缩略拼音键(形如 emoding_yise)
}

// PetImage 是宠物各尺寸图片的相对路径(相对图片根,空串表示缺图)。
type PetImage struct {
	Head          string `json:"head"`          // 小头像 HeadIcon/<n>.webp
	BigHead       string `json:"bigHead"`       // 大头像 BigHeadIcon256/<n>.webp
	Portrait      string `json:"portrait"`      // 全身图 Pet1024/JL_<x>.webp
	PortraitSmall string `json:"portraitSmall"` // 全身缩略 Pet256/JL_<x>.webp
}

// PetBaseInfo 是 petbase 形态的元数据(名称/图鉴号/形态名/进化阶段/进化链分组/身高体重范围)。
type PetBaseInfo struct {
	Name  string // 当前形态名(火神/音速犬/岚鸟…)
	Book  uint32 // 图鉴编号(pictorial_book_id)
	Form  string // 地区/季节形态名(春天的样子…),普通宠物为空
	Stage uint32 // 进化阶段(1 起)
	Evo   uint32 // 进化链分组 id(同链共享),用于重建进化链
	// 身高/体重取值范围(原始整数,与 PetData.height/weight 同单位:height÷100=米,weight÷1000=千克)。
	HeightLow  uint32
	HeightHigh uint32
	WeightLow  uint32
	WeightHigh uint32
	EggGroups  []uint32 // 蛋组(繁殖组)编号,1~2 个,对应 EggGroup.ID
}

// EggGroup 是蛋组(繁殖组)信息:社区流行名 + 官方描述(源自 PET_LIKE_ELEMENT_CONF)。
type EggGroup struct {
	ID   uint32 `json:"id"`
	Name string `json:"name"`
	Desc string `json:"desc"`
}

// DB 是只读名称查找库。
type DB struct {
	species      map[string]string
	nature       map[string]string
	skillDamType map[string]string
	talentRate   map[string]string
	partnerMark  map[string]string
	speciality   map[string]string
	medal        map[string]Medal
	opcodes      map[uint16]string
	natureEffect map[string]NatureEffect
	images       map[string]imageEntry  // petbase_id -> 文件名
	imageBase    map[string]string      // conf_id -> petbase_id(base==自身者不入表)
	petbase      map[uint32]PetBaseInfo // petbase_id -> 形态元数据
	eggGroup     map[uint32]EggGroup    // 蛋组id(1-15) -> 社区名/描述
	evoIndex     map[uint32][]uint32    // 进化链分组 -> 该链各 petbase_id
	imgFiles     map[string]bool        // 实际 embed 的图片相对路径(异色图缺失时回退普通)
	// UI 图标索引: 语义键 -> 图标原始文件名(webp 保持原名),Go 侧拼 <组>/<原名>.webp。
	filterIcons map[string]map[string]string // 组名 -> {枚举整数值: 原名}(filter/)
	bloodIcons  map[string]string            // 血脉id -> 原名(blood/)
	bloodNames  map[string]string            // 血脉id -> 中文短名(普通/草/火…)
	medalIcons  map[string]string            // 奖牌id -> 原名(medal/)
	staticIcons map[string]string            // 语义键 -> 原名(static/:异色/炫彩/污染等)
	// 场景与大地图(实时地图页):见 docs/data.md 3.1、3.2。
	scenes      map[string]string   // scene_cfg_id -> 场景名(SCENE_CONF)
	sceneDefRes map[string]int32    // scene_cfg_id -> 默认 scene_res_id(res 未知时兜底定位)
	sceneRes    map[string]sceneRes // scene_res_cfg_id -> {名称, 所属 scene_cfg_id}
	maps        map[uint32]MapInfo  // 有大地图底图的 scene_res_cfg_id -> 投影参数
	layers      []LayerInfo         // 分层地图(洞穴/地下层),按 cave_name 前缀/位置定位
	poiKinds    []POIKind           // 大地图 POI 图层清单(有序,前端开关)
	pois        map[uint32][]POI    // scene_res_cfg_id -> 该场景的 POI(世界坐标)
	zones       map[string]string   // 区域(营地 id) -> 区域名;眠枭之星收集进度按此键统计
}

// POIKind 是一类大地图 POI(实时地图页的一个可开关图层),取自 names.json 的 poi_kinds。
type POIKind struct {
	K    string `json:"k"`    // 图层键(alchemy/mana/…),与 POI.K 对应
	N    string `json:"n"`    // 中文名(炼金釜/魔力之源…)
	Icon string `json:"icon"` // 图标原始文件名;Go 侧拼 worldmap/<原名>.webp
	On   bool   `json:"on"`   // 默认开启(魔力之源、炼金釜)
}

// POI 是一个大地图标记点(世界坐标,厘米)。名称取自 WORLD_MAP_CONF.element_text_name
// (如「月牙湖岸的魔力之源」),无名时退到图层名。坐标来源与提取见 docs/data.md 3.3。
type POI struct {
	K string `json:"k"` // 所属图层键
	R int32  `json:"r"` // 刷新点 id(NPC_REFRESH_CONTENT_CONF.id);服务器下发的 NPC 实体带同一个 id
	X int32  `json:"x"` // 世界坐标 X(厘米)
	Y int32  `json:"y"` // 世界坐标 Y
	N string `json:"n"` // 名称(悬停显示)
	Z int32  `json:"z"` // 所属区域(营地 id;仅眠枭之星有,0=不属任何区域)
}

// sceneRes 是一个场景资源(scene_res_cfg_id)的名称与所属场景(scene_cfg_id)。
type sceneRes struct {
	N string `json:"n"`
	S int32  `json:"s"`
}

// MapInfo 是一张大地图底图的投影参数(SCENE_RES 世界坐标 → 底图归一化坐标)。
// 底图 webp 路径为 bigmap/<scene_res_cfg_id>.webp(家园室内 30001 为 30001_<房屋等级>)。
type MapInfo struct {
	Name  string `json:"name"`  // 地图名(卡洛西亚大陆…)
	OX    int32  `json:"ox"`    // 底图左上角世界坐标 X(= 地块中心X - 边长/2)
	OY    int32  `json:"oy"`    // 底图左上角世界坐标 Y
	Side  int32  `json:"side"`  // 地块世界边长(厘米);u=(x-ox)/side, v=(y-oy)/side
	World bool   `json:"world"` // 大世界(底图 4096²)否则家园场景(2048²)
	Rooms int    `json:"rooms"` // >0 表示按房屋等级分层(家园室内 30001,底图 30001_<lv>)
}

// LayerInfo 是一个分层地图(洞穴/地下层/家园楼层)的切片图与投影(见 docs/data.md 3.2)。
// 与所属场景同坐标系,进入该层时把切片叠加到底图对应位置。切片 webp 路径为 bigmap/layer/<Img>.webp。
//
// 「当前在哪一层」由服务器的区域进/出事件决定(scene.ParseAreaActs):玩家所在区域的
// area_func_id 命中本层的 AreaFunc 即在本层。不能用位置点对区域多边形做 2D 判定——那样在洞穴
// 正上方的地表也会命中(多边形只有 x/y,分不清人在洞里还是在洞顶)。
type LayerInfo struct {
	ID       uint32 // 层 id(LAYERED_WORLD_MAP_CONF.id)
	Name     string // 层名(信仰者村落一层…)
	Group    int32  // 层组;同组共享地表底图,组内多个楼层
	Res      int32  // 所属 scene_res_cfg_id(家园层为 0)
	Img      string // 切片图文件名(bigmap/layer/<Img>.webp)
	OX       int32  // 层投影左上角世界坐标 X(= camera_center.x - Ortho_width/2)
	OY       int32  // 层投影左上角世界坐标 Y
	Side     int32  // 层投影世界边长(= Ortho_width);切片在底图上的矩形据此算
	AreaFunc uint32 // 本层对应的 area_func_id(LAYERED_WORLD_MAP_CONF.area_func_id)
}

// NatureEffect 是性格对六维的增减维度(六维编号 1-6:1生命2物攻3魔攻4物防5魔防6速度)。
type NatureEffect struct {
	Pos int32 `json:"pos"` // +10% 维度
	Neg int32 `json:"neg"` // -10% 维度
}

// Load 加载 embed 的名称表。
func Load() (*DB, error) {
	var raw struct {
		Species      map[string]string            `json:"species"`
		Nature       map[string]string            `json:"nature"`
		SkillDamType map[string]string            `json:"skill_dam_type"`
		TalentRate   map[string]string            `json:"talent_rate"`
		PartnerMark  map[string]string            `json:"partner_mark"`
		Speciality   map[string]string            `json:"speciality"`
		Medal        map[string]Medal             `json:"medal"`
		Opcodes      map[string]string            `json:"opcodes"`
		NatureEffect map[string]NatureEffect      `json:"nature_effect"`
		FilterIcons  map[string]map[string]string `json:"filter_icons"`
		BloodIcons   map[string]string            `json:"blood_icons"`
		BloodNames   map[string]string            `json:"blood_names"`
		MedalIcons   map[string]string            `json:"medal_icons"`
		StaticIcons  map[string]string            `json:"static_icons"`
		Images       map[string]imageEntry        `json:"images"`
		ImageBase    map[string]uint32            `json:"image_base"`
		EggGroup     map[string]EggGroup          `json:"egg_group"`
		Petbase      map[string]struct {
			N  string   `json:"n"`
			B  uint32   `json:"b"`
			F  string   `json:"f"`
			S  uint32   `json:"s"`
			E  uint32   `json:"e"`
			Eg []uint32 `json:"eg"`
			HL uint32   `json:"hl"`
			HH uint32   `json:"hh"`
			WL uint32   `json:"wl"`
			WH uint32   `json:"wh"`
		} `json:"petbase"`
		Scenes          map[string]string   `json:"scenes"`
		SceneDefaultRes map[string]int32    `json:"scene_default_res"`
		SceneRes        map[string]sceneRes `json:"scene_res"`
		Maps            map[string]struct {
			N     string `json:"n"`
			OX    int32  `json:"ox"`
			OY    int32  `json:"oy"`
			Side  int32  `json:"side"`
			World bool   `json:"world"`
			Rooms int    `json:"rooms"`
		} `json:"maps"`
		Layers map[string]struct {
			N    string `json:"n"`
			Grp  int32  `json:"grp"`
			Res  int32  `json:"res"`
			Img  string `json:"img"`
			OX   int32  `json:"ox"`
			OY   int32  `json:"oy"`
			Side int32  `json:"side"`
			Afid uint32 `json:"afid"`
		} `json:"layers"`
		POIKinds []POIKind         `json:"poi_kinds"`
		POIs     map[string][]POI  `json:"pois"`  // scene_res_id -> 该场景 POI(世界坐标)
		Zones    map[string]string `json:"zones"` // 营地 id -> 区域名
	}
	if err := json.Unmarshal(namesJSON, &raw); err != nil {
		return nil, err
	}
	opcodes := make(map[uint16]string, len(raw.Opcodes))
	for k, v := range raw.Opcodes {
		if n, err := strconv.ParseUint(k, 10, 16); err == nil {
			opcodes[uint16(n)] = v
		}
	}
	imageBase := make(map[string]string, len(raw.ImageBase))
	for k, v := range raw.ImageBase {
		imageBase[k] = key(v)
	}
	petbase := make(map[uint32]PetBaseInfo, len(raw.Petbase))
	evoIndex := map[uint32][]uint32{}
	for k, v := range raw.Petbase {
		id, err := strconv.ParseUint(k, 10, 32)
		if err != nil {
			continue
		}
		petbase[uint32(id)] = PetBaseInfo{
			Name: v.N, Book: v.B, Form: v.F, Stage: v.S, Evo: v.E, EggGroups: v.Eg,
			HeightLow: v.HL, HeightHigh: v.HH, WeightLow: v.WL, WeightHigh: v.WH,
		}
		if v.E != 0 {
			evoIndex[v.E] = append(evoIndex[v.E], uint32(id))
		}
	}
	eggGroup := make(map[uint32]EggGroup, len(raw.EggGroup))
	for k, v := range raw.EggGroup {
		if id, err := strconv.ParseUint(k, 10, 32); err == nil {
			eggGroup[uint32(id)] = EggGroup{ID: uint32(id), Name: v.Name, Desc: v.Desc}
		}
	}
	// embed 的图片清单:异色图未导出时据此回退普通图,避免空图标。
	imgFiles := map[string]bool{}
	fs.WalkDir(ImageFS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			imgFiles[p] = true
		}
		return nil
	})
	maps := make(map[uint32]MapInfo, len(raw.Maps))
	for k, v := range raw.Maps {
		if id, err := strconv.ParseUint(k, 10, 32); err == nil {
			maps[uint32(id)] = MapInfo{Name: v.N, OX: v.OX, OY: v.OY, Side: v.Side, World: v.World, Rooms: v.Rooms}
		}
	}
	layers := make([]LayerInfo, 0, len(raw.Layers))
	for k, v := range raw.Layers {
		id, err := strconv.ParseUint(k, 10, 32)
		if err != nil {
			continue
		}
		layers = append(layers, LayerInfo{ID: uint32(id), Name: v.N, Group: v.Grp, Res: v.Res,
			Img: v.Img, OX: v.OX, OY: v.OY, Side: v.Side, AreaFunc: v.Afid})
	}
	sort.Slice(layers, func(i, j int) bool { return layers[i].ID < layers[j].ID })
	pois := make(map[uint32][]POI, len(raw.POIs))
	for k, v := range raw.POIs {
		if res, err := strconv.ParseUint(k, 10, 32); err == nil {
			pois[uint32(res)] = v
		}
	}
	return &DB{
		scenes:       raw.Scenes,
		sceneDefRes:  raw.SceneDefaultRes,
		sceneRes:     raw.SceneRes,
		maps:         maps,
		layers:       layers,
		poiKinds:     raw.POIKinds,
		pois:         pois,
		zones:        raw.Zones,
		species:      raw.Species,
		nature:       raw.Nature,
		skillDamType: raw.SkillDamType,
		talentRate:   raw.TalentRate,
		partnerMark:  raw.PartnerMark,
		speciality:   raw.Speciality,
		medal:        raw.Medal,
		opcodes:      opcodes,
		natureEffect: raw.NatureEffect,
		filterIcons:  raw.FilterIcons,
		bloodIcons:   raw.BloodIcons,
		bloodNames:   raw.BloodNames,
		medalIcons:   raw.MedalIcons,
		staticIcons:  raw.StaticIcons,
		images:       raw.Images,
		imageBase:    imageBase,
		petbase:      petbase,
		eggGroup:     eggGroup,
		evoIndex:     evoIndex,
		imgFiles:     imgFiles,
	}, nil
}

// PetImage 返回宠物各尺寸图片的相对路径(经 base_id 归并到 petbase 形态;缺图为空串);
// shiny=true 时优先取异色图(无专属异色图或未 embed 时回退普通)。
func (db *DB) PetImage(confID uint32, shiny bool) PetImage {
	pid, ok := db.imageBase[key(confID)]
	if !ok {
		pid = key(confID) // base==自身,直接按 conf_id 查 petbase
	}
	return db.imageOf(pid, shiny)
}

// PetImageByBase 按 petbase_id 直接取图片(base_conf_id 本身即 petbase id,给出当前形态)。
func (db *DB) PetImageByBase(petbaseID uint32, shiny bool) PetImage {
	return db.imageOf(key(petbaseID), shiny)
}

func (db *DB) imageOf(petbaseID string, shiny bool) PetImage {
	e, ok := db.images[petbaseID]
	if !ok {
		return PetImage{}
	}
	head, big, ps := e.H, e.B, e.PS
	// 异色变体仅在「索引有该字段且对应 webp 确已 embed」时启用,否则回退普通图。
	if shiny {
		if e.SH != "" && db.imgFiles["HeadIcon/"+e.SH+".webp"] {
			head = e.SH
		}
		if e.SB != "" && db.imgFiles["BigHeadIcon256/"+e.SB+".webp"] {
			big = e.SB
		}
		if e.SPS != "" && db.imgFiles["Pet256/JL_"+e.SPS+".webp"] {
			ps = e.SPS
		}
	}
	var img PetImage
	if head != "" {
		img.Head = "HeadIcon/" + head + ".webp"
	}
	if big != "" {
		img.BigHead = "BigHeadIcon256/" + big + ".webp"
	}
	if e.P != "" {
		img.Portrait = "Pet1024/JL_" + e.P + ".webp"
	}
	if ps != "" {
		img.PortraitSmall = "Pet256/JL_" + ps + ".webp"
	}
	return img
}

// PetBase 返回 petbase 形态元数据(base_conf_id);ok=false 表示未知。
func (db *DB) PetBase(petbaseID uint32) (PetBaseInfo, bool) {
	v, ok := db.petbase[petbaseID]
	return v, ok
}

// PetEggGroups 返回某 petbase 形态的蛋组列表(社区名+描述,按配置顺序);无则返回 nil。
func (db *DB) PetEggGroups(petbaseID uint32) []EggGroup {
	info, ok := db.petbase[petbaseID]
	if !ok || len(info.EggGroups) == 0 {
		return nil
	}
	out := make([]EggGroup, 0, len(info.EggGroups))
	for _, id := range info.EggGroups {
		if g, ok := db.eggGroup[id]; ok {
			out = append(out, g)
		}
	}
	return out
}

// ChainStep 是进化链上的一个形态(按阶段升序)。
type ChainStep struct {
	Petbase uint32   `json:"petbase"`
	Name    string   `json:"name"`
	Book    uint32   `json:"book"`
	Stage   uint32   `json:"stage"`
	Image   PetImage `json:"image"`
}

// EvolutionChain 返回 petbase 所属进化链(同一形态线,按阶段升序);未知或单形态返回自身一项。
func (db *DB) EvolutionChain(petbaseID uint32) []ChainStep {
	info, ok := db.petbase[petbaseID]
	if !ok {
		return nil
	}
	ids := db.evoIndex[info.Evo]
	if info.Evo == 0 || len(ids) == 0 {
		ids = []uint32{petbaseID} // 无进化链分组:仅自身
	}
	steps := make([]ChainStep, 0, len(ids))
	for _, id := range ids {
		pi := db.petbase[id]
		steps = append(steps, ChainStep{Petbase: id, Name: pi.Name, Book: pi.Book, Stage: pi.Stage, Image: db.PetImageByBase(id, false)})
	}
	// 按阶段升序;同阶段(分支进化,如果冻→抹茶/椰浆/熔岩布丁)再按图鉴号,保证顺序稳定。
	sort.Slice(steps, func(i, j int) bool {
		if steps[i].Stage != steps[j].Stage {
			return steps[i].Stage < steps[j].Stage
		}
		return steps[i].Book < steps[j].Book
	})
	return steps
}

// NatureEffect 返回性格的 +10%/-10% 维度(六维编号 1-6;0 表示无)。
func (db *DB) NatureEffect(natureID uint32) NatureEffect { return db.natureEffect[key(natureID)] }

// OpcodeNames 返回 opcode 整数到 ZoneSvrCmd 名称的映射。
func (db *DB) OpcodeNames() map[uint16]string { return db.opcodes }

// SceneName 返回场景名(scene_cfg_id → SCENE_CONF.scene_name)。
func (db *DB) SceneName(cfgID int32) string {
	return db.scenes[strconv.FormatInt(int64(cfgID), 10)]
}

// SceneResName 返回场景资源名(scene_res_cfg_id → SCENE_RES_CONF)。
func (db *DB) SceneResName(resID int32) string {
	return db.sceneRes[strconv.FormatInt(int64(resID), 10)].N
}

// DefaultSceneRes 返回某 scene_cfg_id 的默认 scene_res_id(SCENE_CONF 主行);无则 0。
// 当无法从进入/传送通知得知精确 res 时(中途开抓/无缓存),据此兜底定位底图。
func (db *DB) DefaultSceneRes(cfgID int32) int32 {
	return db.sceneDefRes[strconv.FormatInt(int64(cfgID), 10)]
}

// MapInfo 返回某 scene_res_cfg_id 的大地图投影参数;第二返回值表示该场景是否有底图。
func (db *DB) MapInfo(resID uint32) (MapInfo, bool) { m, ok := db.maps[resID]; return m, ok }

// MapImage 返回某场景底图的 webp 文件名(不含扩展名),前端拼 /img/bigmap/<名>.webp;无底图返回 ""。
// 家园室内(Rooms>0)按房屋等级分图 <res>_<level>(level 越界则夹取,未知按 1);其余场景为 <res>。
func (db *DB) MapImage(resID uint32, room int32) string {
	m, ok := db.maps[resID]
	if !ok {
		return ""
	}
	if m.Rooms > 0 {
		if room < 1 {
			room = 1
		}
		if int(room) > m.Rooms {
			room = int32(m.Rooms)
		}
		return strconv.FormatInt(int64(resID), 10) + "_" + strconv.FormatInt(int64(room), 10)
	}
	return strconv.FormatInt(int64(resID), 10)
}

// Project 把场景世界坐标(厘米)投影为底图归一化坐标 u,v∈[0,1](复刻客户端
// BigMapUtils.ScenePosToImagePosF)。该 scene_res 无底图时 ok=false。u,v 可能越界
// [0,1](角色在底图覆盖范围外,如迷雾区),调用方自行决定是否裁剪。
func (db *DB) Project(resID uint32, x, y int32) (u, v float64, ok bool) {
	m, ok := db.maps[resID]
	if !ok || m.Side == 0 {
		return 0, 0, false
	}
	return float64(x-m.OX) / float64(m.Side), float64(y-m.OY) / float64(m.Side), true
}

// LayerIn 返回玩家当前所在的分层地图:activeFuncs 是服务器区域进/出事件维护的「玩家当前所在
// 区域的 area_func_id 集合」(见 scene.ParseAreaActs),命中本场景(res)某层的 AreaFunc 即在该层。
// 不在任何层返回 ok=false(显示地表底图)。
//
// 复刻客户端 BigMapModuleData:GetCurMapLayerId(它同样是拿玩家所在 zone 的 area_func_id 查分层表)。
// 早前用「位置点在区域多边形内」近似,会在洞穴正上方的地表误叠洞穴图——多边形只有 x/y,
// 而区域触发体是 3D 的,分不清人在洞里还是在洞顶。见 docs/data.md 3.2。
func (db *DB) LayerIn(res int32, activeFuncs map[uint32]bool) (LayerInfo, bool) {
	if len(activeFuncs) == 0 {
		return LayerInfo{}, false
	}
	for _, l := range db.layers {
		if l.Res == res && l.AreaFunc != 0 && activeFuncs[l.AreaFunc] {
			return l, true
		}
	}
	return LayerInfo{}, false
}

func key(id uint32) string { return strconv.FormatUint(uint64(id), 10) }

// Species 返回种类名(conf_id)。
func (db *DB) Species(confID uint32) string { return db.species[key(confID)] }

// Nature 返回性格名(nature id)。
func (db *DB) Nature(id uint32) string { return db.nature[key(id)] }

// SkillDamType 返回系别名(SkillDamType enum 整数值)。
func (db *DB) SkillDamType(v int32) string { return db.skillDamType[strconv.FormatInt(int64(v), 10)] }

// iconPath 由「原始文件名」拼出 <group>/<name>.webp;name 为空或未 embed 时返回空串。
func (db *DB) iconPath(group, name string) string {
	if name == "" {
		return ""
	}
	p := group + "/" + name + ".webp"
	if !db.imgFiles[p] {
		return ""
	}
	return p
}

// filterIcon 查 filter_icons 索引(组名 + 枚举整数值)拿原名,拼 filter/<原名>.webp。
func (db *DB) filterIcon(group string, v int32) string {
	return db.iconPath("filter", db.filterIcons[group][strconv.FormatInt(int64(v), 10)])
}

// SkillDamTypeIcon 返回系别(属性)图标路径(SkillDamType enum 整数值)。
func (db *DB) SkillDamTypeIcon(v int32) string { return db.filterIcon("skill_dam_type", v) }

// SkillDamTypeIcons 返回系别中文名 -> 图标路径(供前端系别筛选按钮显示图标)。
func (db *DB) SkillDamTypeIcons() map[string]string {
	out := make(map[string]string, len(db.skillDamType))
	for k, name := range db.skillDamType {
		if v, err := strconv.ParseInt(k, 10, 32); err == nil {
			if p := db.SkillDamTypeIcon(int32(v)); p != "" {
				out[name] = p
			}
		}
	}
	return out
}

// AttributeTypeIcon 返回六维属性图标路径(AttributeType enum 整数值;1-6 即六维编号,
// 79-84 为对应增益类)。
func (db *DB) AttributeTypeIcon(v int32) string { return db.filterIcon("attribute_type", v) }

// PartnerMarkIcon 返回搭档标记图标路径(PetPartnerMarkType enum 整数值)。
func (db *DB) PartnerMarkIcon(v int32) string { return db.filterIcon("partner_mark", v) }

// BloodIcon 返回血脉主图标路径 blood/<原名>.webp(PET_BLOOD_CONF.blood,1-24);无图或未 embed 时空串。
func (db *DB) BloodIcon(bloodID uint32) string {
	return db.iconPath("blood", db.bloodIcons[key(bloodID)])
}

// BloodName 返回血脉中文短名(普通/草/火…;PET_BLOOD_CONF.blood_name)。
func (db *DB) BloodName(bloodID uint32) string { return db.bloodNames[key(bloodID)] }

// StaticIcon 返回杂项静态图标路径 static/<原名>.webp(语义键:shiny/colorful/shiny_colorful/
// pollution/partner_frame);未知键或未 embed 时空串。
func (db *DB) StaticIcon(sem string) string { return db.iconPath("static", db.staticIcons[sem]) }

// MedalIcon 返回奖牌小图路径 medal/<原名>.webp(MEDAL_CONF.id → icon(BagItem));无图或未 embed 时空串。
func (db *DB) MedalIcon(medalID uint32) string {
	return db.iconPath("medal", db.medalIcons[key(medalID)])
}

// POIKinds 返回大地图 POI 图层清单(有序:魔力之源、炼金釜、守护地、庇护所、眠枭之星)。
func (db *DB) POIKinds() []POIKind { return db.poiKinds }

// POIIcon 返回 POI 图层的图标路径 worldmap/<原名>.webp;未 embed 时空串。
func (db *DB) POIIcon(kind POIKind) string { return db.iconPath("worldmap", kind.Icon) }

// POIs 返回某场景的全部 POI(世界坐标);无底图的场景不收录,返回 nil。
func (db *DB) POIs(resID uint32) []POI { return db.pois[resID] }

// ZoneName 返回区域名(键为该区域营地(魔力之源)的刷新点 id,即服务器收集进度里的区域键)。
func (db *DB) ZoneName(camp int32) string { return db.zones[key(uint32(camp))] }

// TalentRate 返回天分评价名(talent_rank)。
func (db *DB) TalentRate(rank uint32) string { return db.talentRate[key(rank)] }

// PartnerMark 返回标记名(PetPartnerMarkType enum 整数值)。
func (db *DB) PartnerMark(v int32) string { return db.partnerMark[strconv.FormatInt(int64(v), 10)] }

// Speciality 返回特长名(speciality_id)。
func (db *DB) Speciality(id uint32) string { return db.speciality[key(id)] }

// Medal 返回奖牌名称与描述(wear_medal_conf_id)。
func (db *DB) Medal(id uint32) (Medal, bool) { m, ok := db.medal[key(id)]; return m, ok }

// MedalEntry 是带 id 的奖牌(用于全量奖牌墙)。
type MedalEntry struct {
	ID   uint32 `json:"id"`
	Name string `json:"name"`
	Desc string `json:"desc"`
	Icon string `json:"icon,omitempty"` // medal/<原名>.webp(无图或未 embed 时空)
}

// AllMedals 返回全部奖牌,按 id 升序(供前端奖牌墙展示全部奖牌)。
func (db *DB) AllMedals() []MedalEntry {
	out := make([]MedalEntry, 0, len(db.medal))
	for k, v := range db.medal {
		id, _ := strconv.ParseUint(k, 10, 32)
		out = append(out, MedalEntry{ID: uint32(id), Name: v.Name, Desc: v.Desc, Icon: db.MedalIcon(uint32(id))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// AllSpecialities 返回全部特长名(按 id 升序去重),供前端高亮规则点选。
func (db *DB) AllSpecialities() []string { return sortedNames(db.speciality) }

// sortedNames 把 id(字符串)→名 的映射按数值 id 升序取名、去空去重。
func sortedNames(m map[string]string) []string {
	type kv struct {
		id   uint64
		name string
	}
	arr := make([]kv, 0, len(m))
	for k, v := range m {
		if v == "" {
			continue
		}
		id, _ := strconv.ParseUint(k, 10, 64)
		arr = append(arr, kv{id, v})
	}
	sort.Slice(arr, func(i, j int) bool { return arr[i].id < arr[j].id })
	out := make([]string, 0, len(arr))
	seen := map[string]bool{}
	for _, e := range arr {
		if seen[e.name] {
			continue
		}
		seen[e.name] = true
		out = append(out, e.name)
	}
	return out
}

// GenderName 返回性别符号。
func GenderName(g uint32) string {
	switch g {
	case 1:
		return "♂"
	case 2:
		return "♀"
	default:
		return ""
	}
}
