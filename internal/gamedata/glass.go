package gamedata

// 炫彩(MDT_GLASS / GlassInfo)的外观与「色卡」。色卡就是游戏内点开炫彩标记弹出的那张小卡
// (客户端 UMG_Pet_DazzlingTips_C),前端按 GlassCard 复刻绘制,素材由 rocom-parse gen_icons.py
// 的 glass 组产出、gen_gamedata.py 写索引。详见 rocom-parse docs/data.md 的炫彩段。

// 炫彩类型(GlassInfo.glass_type,dataconfig.GlassType)。
const (
	GlassNull   = 0 // GT_NULL,非炫彩
	GlassCommon = 1 // GT_COMMON,普通炫彩(glass_value 是打包色号)
	GlassHidden = 2 // GT_HIDDEN,隐藏炫彩(glass_value 是 HIDDEN_GLASS_CONF.id)
)

// glassParticleShift 是普通炫彩色号的打包位宽:glass_value = (粒子id << 20) | 配色id
// (客户端 PetUtils.GetShineDataValue 即按 20 位拆)。
const glassParticleShift = 20

// glassData 是 names.json 的 glass 段(两张遮罩 + 三张索引表)。
type glassData struct {
	Base   string `json:"base"` // 色卡底(圆角矩形遮罩)
	Wave   string `json:"wave"` // 色卡上半波浪遮罩
	Hidden map[string]struct {
		N    string `json:"n"`    // 外观名(暗夜拾光…)
		NC   string `json:"nc"`   // 名字的游戏内显示色
		S    string `json:"s"`    // 归属:常驻隐藏 / 第N赛季限定
		Card string `json:"card"` // 整张烤好的色卡
		Icon string `json:"icon"` // 该炫彩的标记图
		Yise string `json:"yise"` // 同上的异色炫彩合成版
	} `json:"hidden"`
	Colors map[string]struct {
		N  string `json:"n"`  // 配色名(亮X亮 - 绿红…)
		C1 string `json:"c1"` // ui_color_1
		C2 string `json:"c2"` // ui_color_2
	} `json:"colors"`
	Particles map[string]struct {
		N    string `json:"n"`    // 粒子名(四角星/爱心…)
		Card string `json:"card"` // 色卡的粒子层
	} `json:"particles"`
	Pollution *struct {
		C1   string `json:"c1"`   // PET_GLOBAL_CONFIG.hb_record_nightmare_color 前一色(着波浪)
		C2   string `json:"c2"`   // 同上后一色(着底)
		Card string `json:"card"` // hb_record_nightmare_icon 圆点层
	} `json:"pollution"`
}

// GlassCard 是一只炫彩宠物的色卡。两种画法二选一(见 UMG_Pet_DazzlingTips_C:ShowInfo):
//
//	隐藏炫彩 → Card 是整张烤好的图,配色已画进去,原样贴上即可;
//	普通炫彩 → 三层叠:Base 着 Color2 打底 → Wave(上半波浪)着 Color1 → Card(粒子层)原色压最上。
//
// Base/Wave 是纯白 + alpha 的遮罩,前端用 CSS mask 上色。
// 污染血脉的宠物也借这张卡(Pollution,见 PollutionCard):游戏图鉴里它的卡与炫彩摆在一排,拼法同普通炫彩。
type GlassCard struct {
	Hidden    bool   `json:"hidden,omitempty"`    // 隐藏炫彩(赛季款/常驻款);否则普通炫彩
	Pollution bool   `json:"pollution,omitempty"` // 污染卡(不是炫彩,是污染血脉)
	Name      string `json:"name"`                // 暗夜拾光 / 亮X亮 - 绿红(普通炫彩即配色名)
	NameColor string `json:"nameColor,omitempty"` // 隐藏炫彩名的游戏内显示色
	Season    string `json:"season,omitempty"`    // 隐藏炫彩的归属:常驻隐藏 / 第2赛季限定
	Particle  string `json:"particle,omitempty"`  // 普通炫彩的粒子名(爱心/四角星…)
	Color1    string `json:"color1,omitempty"`    // 普通炫彩:上半波浪色
	Color2    string `json:"color2,omitempty"`    // 普通炫彩:底色
	Icon      string `json:"icon,omitempty"`      // 炫彩标记图(异色宠物给异色炫彩合成版)
	Card      string `json:"card,omitempty"`      // 隐藏:整张卡;普通:粒子层
	Base      string `json:"base,omitempty"`      // 普通:底遮罩(着 Color2)
	Wave      string `json:"wave,omitempty"`      // 普通:上半波浪遮罩(着 Color1)
}

// Glass 组装一只宠物的色卡。glassType/glassValue 取自 PetData.glass_info,shiny 决定标记图
// 用普通版还是异色炫彩版。非炫彩、或该版本配置里查不到这一款时返回 nil(调用方退回通用炫彩图标)。
func (db *DB) Glass(glassType, glassValue int32, shiny bool) *GlassCard {
	switch glassType {
	case GlassHidden:
		h, ok := db.glass.Hidden[key(uint32(glassValue))]
		if !ok {
			return nil
		}
		icon := h.Icon
		if shiny && h.Yise != "" {
			icon = h.Yise
		}
		return &GlassCard{
			Hidden: true, Name: h.N, NameColor: h.NC, Season: h.S,
			Icon: db.iconPath("glass", icon), Card: db.iconPath("glass", h.Card),
		}
	case GlassCommon:
		if glassValue <= 0 {
			return nil
		}
		p := db.glass.Particles[key(uint32(glassValue)>>glassParticleShift)]
		c := db.glass.Colors[key(uint32(glassValue)&(1<<glassParticleShift-1))]
		if p.N == "" && c.N == "" {
			return nil
		}
		sem := "colorful"
		if shiny {
			sem = "shiny_colorful"
		}
		return &GlassCard{
			Name: c.N, Color1: c.C1, Color2: c.C2, Particle: p.N,
			Icon: db.StaticIcon(sem),
			Card: db.iconPath("glass", p.Card),
			Base: db.iconPath("glass", db.glass.Base),
			Wave: db.iconPath("glass", db.glass.Wave),
		}
	}
	return nil
}

// BloodPollution 是污染血脉的 PET_BLOOD_CONF 行 id(PetData.blood_id)。
const BloodPollution = 23

// PollutionCard 是污染血脉宠物的色卡:拼法同普通炫彩,两色与圆点层来自 PET_GLOBAL_CONFIG 的
// hb_record_nightmare_color / hb_record_nightmare_icon(rocom-parse docs/data.md 的炫彩段)。
// 该版本配置里没有这两行时返回 nil。
func (db *DB) PollutionCard() *GlassCard {
	p := db.glass.Pollution
	if p == nil {
		return nil
	}
	return &GlassCard{
		Pollution: true, Name: "污染", Color1: p.C1, Color2: p.C2,
		Card: db.iconPath("glass", p.Card),
		Base: db.iconPath("glass", db.glass.Base),
		Wave: db.iconPath("glass", db.glass.Wave),
	}
}

// GlassDesc 返回炫彩外观的一行中文描述(见 docs/map.md 5):
// 隐藏炫彩给外观名(暗夜拾光…),普通炫彩给「配色·粒子」(亮X暗 - 浅紫橙·四角星)。
// 非炫彩或查不到时返回空串(调用方自行兜底)。
//
// 主名(配色/外观名)在前,与详情页那枚炫彩标记的提示同序(见 web/src/components/badges.jsx
// 的 glassDesc);那边分隔用空格,因为它整条已经被 ` · ` 断过一次了。
func (db *DB) GlassDesc(glassType, glassValue int32) string {
	g := db.Glass(glassType, glassValue, false)
	if g == nil {
		return ""
	}
	if g.Name != "" && g.Particle != "" {
		return g.Name + "·" + g.Particle
	}
	return g.Name + g.Particle // 二者至多有一个非空(见 Glass)
}
