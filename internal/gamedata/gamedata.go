// Package gamedata 提供从游戏解包数据(Bin 配置)提取的 id->中文名 查找表(编译期 embed)。
// 文件划分:pet(宠物/奖牌/蛋组/进化链)/ map(场景/底图投影/分层/POI)/ icons(UI 图标)。
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

// 宠物图片与 UI 图标(webp,rocom-parse 的 gen_images/gen_icons/gen_bigmap 产出,make gamedata 生成)。
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

// DB 是只读名称查找库。
type DB struct {
	species      map[string]string
	nature       map[string]string
	skillDamType map[string]string
	talentRate   map[string]string
	partnerMark  map[string]string
	speciality   map[string]string
	medal        map[string]Medal
	natureEffect map[string]NatureEffect
	images       map[string]imageEntry  // petbase_id -> 文件名
	imageBase    map[string]string      // conf_id -> petbase_id(base==自身者不入表)
	petbase      map[uint32]PetBaseInfo // petbase_id -> 形态元数据
	eggGroup     map[uint32]EggGroup    // 蛋组id(1-15) -> 社区名/描述
	npcPets      map[uint32]uint32      // 野生宠 NPC_CONF id -> petbase_id(取名称/头像,见 3.5)
	npcBosses    map[uint32]bool        // 野外首领的 NPC_CONF id(throwing_interact_type=4,见 3.8)
	battleCfg    map[uint32]BattleCfg   // 赛季奇遇战斗 battle_cfg_id -> 战斗名/敌方宠物 conf_id
	glass        glassData              // 炫彩外观与色卡素材(见 Glass / GlassDesc)
	evoIndex     map[uint32][]uint32    // 进化链分组 -> 该链各 petbase_id
	imgFiles     map[string]bool        // 实际 embed 的图片相对路径(异色图缺失时回退普通)
	// UI 图标索引: 语义键 -> 图标原始文件名(webp 保持原名),Go 侧拼 <组>/<原名>.webp。
	filterIcons map[string]map[string]string // 组名 -> {枚举整数值: 原名}(filter/)
	bloodIcons  map[string]string            // 血脉id -> 原名(blood/)
	bloodNames  map[string]string            // 血脉id -> 中文短名(普通/草/火…)
	medalIcons  map[string]string            // 奖牌id -> 原名(medal/)
	staticIcons map[string]string            // 语义键 -> 原名(static/:异色/炫彩/污染等)
	// 场景与大地图(实时地图页):见 docs/map.md 1、3.2。
	scenes      map[string]string   // scene_cfg_id -> 场景名(SCENE_CONF)
	sceneDefRes map[string]int32    // scene_cfg_id -> 默认 scene_res_id(res 未知时兜底定位)
	sceneRes    map[string]sceneRes // scene_res_cfg_id -> {名称, 所属 scene_cfg_id}
	maps        map[uint32]MapInfo  // 有大地图底图的 scene_res_cfg_id -> 投影参数
	layers      []LayerInfo         // 分层地图(洞穴/地下层),按 cave_name 前缀/位置定位
	poiKinds    []POIKind           // 大地图 POI 图层清单(有序,前端开关)
	pois        map[uint32][]POI    // scene_res_cfg_id -> 该场景的 POI(世界坐标)
	zones       map[string]string   // 区域(营地 id) -> 区域名;眠枭之星收集进度按此键统计
	// 稀兽花种(见 flower.go 与 docs/map.md 8):花种不是固定点位,列表由流量给,这里只存索引。
	flowerNpcs   map[string]FlowerNpc  // 花种 NPC_CONF id -> 类别与血脉花图
	flowerSpots  map[uint32]FlowerSpot // 刷新行 id(content_cfg_id) -> 候选点世界坐标
	flowerStarLv map[string]int32      // 普通花种:星级 -> 里面那只精灵的等级
	flowerSpecLv map[string][]int32    // 命定花种:spec_flower_seed_id -> 按星级的等级表
	// 精灵蛋与家园小窝(见 docs/eggs.md):
	eggConf    map[uint32]EggConf // 物种 conf_id -> 蛋自身的身高体重区间与孵化时长
	eggItems   map[uint32]EggItem // 背包蛋物品 id -> 显示名/物种/图标/品质
	eggNPCs    map[uint32]uint32  // 窝上蛋 NPC 的 NPC_CONF id -> 蛋物品 id
	eggTypes   map[int32]EggType  // 蛋品类 precious_egg_type -> 名称/排序号/角标
	sizeMedals []SizeMedal        // 按百分位自动授予的奖牌(体重两枚 + 嗓音两枚)
	nestFurn   map[uint32]Nest    // 小窝家具 config_id -> 家具(精灵小窝 / 学院小窝)
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
		NatureEffect map[string]NatureEffect      `json:"nature_effect"`
		FilterIcons  map[string]map[string]string `json:"filter_icons"`
		BloodIcons   map[string]string            `json:"blood_icons"`
		BloodNames   map[string]string            `json:"blood_names"`
		MedalIcons   map[string]string            `json:"medal_icons"`
		StaticIcons  map[string]string            `json:"static_icons"`
		Images       map[string]imageEntry        `json:"images"`
		ImageBase    map[string]uint32            `json:"image_base"`
		EggGroup     map[string]EggGroup          `json:"egg_group"`
		NpcPets      map[string]uint32            `json:"npc_pets"`
		NpcBosses    []uint32                     `json:"npc_bosses"`
		BattleCfg    map[string]BattleCfg         `json:"battle_cfg"`
		Glass        glassData                    `json:"glass"`
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
		EggConf  map[string]EggConf `json:"egg_conf"`
		EggItems map[string]struct {
			N   string `json:"n"`
			C   uint32 `json:"c"`
			Img string `json:"img"`
			NPC uint32 `json:"npc"`
			Q   int32  `json:"q"`
			S   int32  `json:"s"`
		} `json:"egg_items"`
		EggTypes      map[string]EggType   `json:"egg_types"`
		SizeMedals    []SizeMedal          `json:"size_medals"`
		NestFurniture map[string]Nest      `json:"nest_furniture"`
		FlowerNpcs    map[string]FlowerNpc `json:"flower_npcs"`
		Flowers       map[string][3]int32  `json:"flowers"` // 刷新行 id -> [res, x, y]
		FlowerLevels  struct {
			Star map[string]int32   `json:"star"` // 普通花种:星级 -> 等级
			Spec map[string][]int32 `json:"spec"` // 命定花种:限定花种 id -> 按星级的等级表
		} `json:"flower_levels"`
		POIKinds []POIKind         `json:"poi_kinds"`
		POIs     map[string][]POI  `json:"pois"`  // scene_res_id -> 该场景 POI(世界坐标)
		Zones    map[string]string `json:"zones"` // 营地 id -> 区域名
	}
	if err := json.Unmarshal(namesJSON, &raw); err != nil {
		return nil, err
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
	npcPets := make(map[uint32]uint32, len(raw.NpcPets))
	for k, v := range raw.NpcPets {
		if id, err := strconv.ParseUint(k, 10, 32); err == nil {
			npcPets[uint32(id)] = v
		}
	}
	npcBosses := make(map[uint32]bool, len(raw.NpcBosses))
	for _, id := range raw.NpcBosses {
		npcBosses[id] = true
	}
	battleCfg := make(map[uint32]BattleCfg, len(raw.BattleCfg))
	for k, v := range raw.BattleCfg {
		if id, err := strconv.ParseUint(k, 10, 32); err == nil {
			battleCfg[uint32(id)] = v
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
	eggConf := make(map[uint32]EggConf, len(raw.EggConf))
	for k, v := range raw.EggConf {
		if id, err := strconv.ParseUint(k, 10, 32); err == nil {
			eggConf[uint32(id)] = v
		}
	}
	eggItems := make(map[uint32]EggItem, len(raw.EggItems))
	eggNPCs := make(map[uint32]uint32, len(raw.EggItems))
	for k, v := range raw.EggItems {
		id, err := strconv.ParseUint(k, 10, 32)
		if err != nil {
			continue
		}
		eggItems[uint32(id)] = EggItem{ID: uint32(id), Name: v.N, Conf: v.C, Icon: v.Img, Quality: v.Q, SortID: v.S}
		if v.NPC != 0 {
			eggNPCs[v.NPC] = uint32(id)
		}
	}
	eggTypes := make(map[int32]EggType, len(raw.EggTypes))
	for k, v := range raw.EggTypes {
		if id, err := strconv.ParseInt(k, 10, 32); err == nil {
			v.ID = int32(id)
			eggTypes[int32(id)] = v
		}
	}
	nestFurn := make(map[uint32]Nest, len(raw.NestFurniture))
	for k, v := range raw.NestFurniture {
		if id, err := strconv.ParseUint(k, 10, 32); err == nil {
			nestFurn[uint32(id)] = v
		}
	}
	flowerSpots := make(map[uint32]FlowerSpot, len(raw.Flowers))
	for k, v := range raw.Flowers {
		if id, err := strconv.ParseUint(k, 10, 32); err == nil {
			flowerSpots[uint32(id)] = FlowerSpot{Res: v[0], X: v[1], Y: v[2]}
		}
	}
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
		flowerNpcs:   raw.FlowerNpcs,
		flowerSpots:  flowerSpots,
		flowerStarLv: raw.FlowerLevels.Star,
		flowerSpecLv: raw.FlowerLevels.Spec,
		species:      raw.Species,
		nature:       raw.Nature,
		skillDamType: raw.SkillDamType,
		talentRate:   raw.TalentRate,
		partnerMark:  raw.PartnerMark,
		speciality:   raw.Speciality,
		medal:        raw.Medal,
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
		npcPets:      npcPets,
		npcBosses:    npcBosses,
		battleCfg:    battleCfg,
		glass:        raw.Glass,
		evoIndex:     evoIndex,
		imgFiles:     imgFiles,
		eggConf:      eggConf,
		eggItems:     eggItems,
		eggNPCs:      eggNPCs,
		eggTypes:     eggTypes,
		sizeMedals:   raw.SizeMedals,
		nestFurn:     nestFurn,
	}, nil
}

func key(id uint32) string { return strconv.FormatUint(uint64(id), 10) }
