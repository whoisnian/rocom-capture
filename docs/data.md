# 数据来源与解析

本项目的解包数据**全部随仓库提交**(均为 FModel 从 Windows 客户端提取),不依赖任何外部仓库;
编译期 `embed` 进二进制，运行时也不依赖原始解包目录。两份 vendored 源:

- **游戏二进制配置 `nrc/bin/`**:提供**中文名称表**。游戏自有的 `.bytes`(数据)+ `.non`
  (schema)+ `BinLocalize/dev_CN`(本地化),用 vendored 的 `scripts/decode_bin.py` 解码。
- **游戏描述符 `nrc/all.pb`**:游戏自带的 protobuf 描述符(`FileDescriptorSet`,即运行时
  `pb.loadufsfile` 加载的同一份),提供 `internal/pb` 的**字段号/类型**与 **opcode/枚举**。
  含字段号,可直接喂给 protoc 生成 Go,无需 .proto 文本。

字段号/枚举是**追加式**的(新版本只加不改号),故几乎无需跟版本更新;名称表随游戏内容变动。
要更新到新版本游戏:用 `scripts/unpack.sh`(或 FModel 手动导出)重新提取覆盖 `nrc/bin/` 与
`nrc/all.pb`,再跑两个生成脚本。行 id 同样跨版本稳定(实测大版本更新后星星刷新行 id 原样不动)。

> **Linux 一键解包(`scripts/unpack.sh`)**:直接从游戏 pak(目录或安卓 .apk)产出与 FModel
> 手动导出**完全一致**的 `~/Downloads/NRC/Content/` 布局——`bin`/`pb` 原始文件、`icons`/`bigmap`
> Texture2D→PNG、`atlas` 五组图集的 Frames→Save Properties JSON + Textures→PNG(即下文各节的
> 全部 FModel 前置操作),下游 gen_* 脚本零改动。`Parallel.ForEach` 并行解码,默认跳过已存在
> 文件(增量),`--list` 预览、`--only` 选类别、`--pet1024` 追加全身大图、`--raw` 导任意前缀。
> C# 实现在 `scripts/unpack/`,基于 CUE4Parse 内置的 `GAME_RocoKingdomWorld` 支持(自定义
> AES 字节置换变体、Bin/luac 专属处理,无需 usmap)。依赖 dotnet-sdk 10+ 与 `~/Git/gh/CUE4Parse`
> 克隆(`CUE4PARSE_DIR` 覆盖);首次运行自动下载 oodle/zlib-ng 到 `~/.cache/nrc-unpack`。
> AES 主密钥与 Windows FModel `AppSettings.json → AesKeys` 同一把,默认值已内置在
> `unpack.sh`(`DEFAULT_AES`,换密钥的版本用 `--aes <hex>`/`@文件` 覆盖);FModel 该游戏条目的 UeVersion=68812827 即
> `GAME_RocoKingdomWorld`,usmap endpoint 未启用,与本工具口径一致。游戏 pak 在 Windows 客户端
> `<安装目录>\Win64\NRC\Content\Paks`(拷到 Linux 或喂安卓 .apk 均可)。FModel 手动导出仍可用,
> 两者互为校验。

> **2026-07 大版本起,策划专用字段(editor_name、max_num、npc_pendant_id 等)从发布数据剥离**,
> 解析只可依赖仍随包发布的字段与表:石像奖励行按刷新区域顶点数排除、带星石像按 NPC_PENDANT_CONF
> 判定(见 3.3);星点→区域归属走 CAMP_CONF 管辖区外键链(见 3.4)。
(历史上名称/opcode 曾取自 pak-public-kit、字段号曾取自 world-data;现都被自有提取替代,
且修正了 pak-public-kit 的 PET_CONF 名整体错位 bug,见第 5 节。)

## 1. 名称表数据来源(`nrc/bin/`)

`nrc/bin/` 下(只收录需要的表):

| 路径 | 内容 |
| --- | --- |
| `BinConf/*.non` | 表结构 schema(JSON,字段名/类型/偏移) |
| `BinDataCompressed/*.bytes` | 表数据(游戏自有压缩二进制) |
| `BinLocalize/dev_CN/*.bytes` | 本地化字符串(`ELocalizedString` 字段经此解析) |

`scripts/gen_gamedata.py` 调 vendored 的 `scripts/decode_bin.py` 把上述解码成
`{"RocoDataRows":{id:{...}}}`。opcode/枚举不在 Bin 里,取自 `nrc/all.pb`(见第 2、3 节)。

关键表：

- `MONSTER_CONF` + `PET_CONF` — 宠物种类名(`conf_id → name`)。
  常规宠物在 MONSTER_CONF，彩蛋/特殊宠物在 PET_CONF，两表 id 不重叠，合并取用。
- `AUDIO_NATURE_CONF` — 性格名(`nature_id → name`，内联 `EString`，无需本地化)
- `MEDAL_CONF` — 奖牌名(`ELocalizedString`)与描述
- `PET_TALENT_CONF` — 特长名(`speciality_id → name`)
- `PET_FILTER_CONF` — 系别/天分/标记的 `filter_enum_value → filter_desc`(中文);另含筛选图标引用(见 3 节末)
- `PET_BLOOD_CONF` — 血脉(24 条:18 属性系 + 首领/巨兽/黑魔法/异核/污染/奇异)的主图标 `icon` 引用(见 3 节末)
- `PET_LIKE_ELEMENT_CONF` — 蛋组(繁殖组)。`id`(1~15)即 `PETBASE_CONF.egg_group` 列表里的编号,
  `pet_like_reason` 对应 `all.pb` 的 `PetEggGroup` 枚举 `PEG_*`;`editor_name1` 为策划编辑器标签
  (「名称:描述」格式,官方 Bin 字段而非本地化 UI 串),取「:」后作蛋组描述保留。显示名不用其内定名,
  改用社区更流行的叫法(未发现/巨灵/两栖/昆虫/天空/动物/妖精/植物/拟人/软体/大地/魔力/海洋/龙/机械,
  硬编码于 `gen_gamedata.py` 的 `EGG_GROUP_NAMES`)。id 16+ 为繁殖组合标记,忽略。
- `PETBASE_CONF` + `MODEL_CONF` — 宠物图片引用(`JL_res` 全身图、`model_conf→icon` 头像;见 3 节末)
- `SCENE_CONF` + `SCENE_RES_CONF` + `WORLD_MAP_BLOCK_CONF` — 场景名与大地图投影(见 3.1 节)
- `LAYERED_WORLD_MAP_CONF` + `AREA_FUNC_CONF` — 分层地图(洞穴/地下层)切片图与投影(见 3.2 节)
- `WORLD_MAP_CONF` + `NPC_REFRESH_CONTENT_CONF` + `AREA_CONF` + `SCENE_OBJECT_CONF`
  — 大地图 POI(炼金釜/魔力之源/…)的图标与坐标(见 3.3 节)。这几张是 `nrc/bin` 里最大的
  (AREA_CONF 8.1M、NPC_REFRESH 3.3M),但坐标只能从它们来,故一并 vendored
  (`NPC_CONF` 2.4M 现已不被生成脚本读取,留作星星 NPC id/`min_map_disappear` 外键的查证依据)
- `NPC_PENDANT_CONF` — NPC 挂件(带星石像的判据与挂件星 npc,见 3.3/3.4;行 id = 石像刷新行 id
  = pcap 里的 `pendant_cfg_id`)
- `WORLD_EXPLORING_STATISTIC_CONF` — 探索统计注册表:「眠枭之星」行的 npc 清单即服务器
  explore_infos 计数的那批 npc_id(九个,与 STAR_NPCS/star.go 的 starNpc 同一批);生成脚本
  据此做防锈校验,新版本增删星 npc 会报警(见 3.3)
- `CAMP_CONF` — 营地表(行 id = 营地刷新点 id = explore_infos 的 belong_camp):
  `manage_area_func` 外键给出区域管辖多边形,是星点→区域归属的权威来源(见 3.4)
- opcode/系别/天分/标记的整数枚举取自 `nrc/all.pb`(`ZoneSvrCmd`/`SkillDamType` 等)

## 2. 描述符 → Go(`scripts/gen_proto.py`，数据源:all.pb)

`all.pb` 已是合法的 `FileDescriptorSet`(含字段号/类型)，直接喂给
`protoc --descriptor_set_in` 即可生成 Go，**无需 .proto 文本，也无需 fix_proto 修补
syntax/enum**(那是旧 world-data `.proto` 才有的坑，已随数据源切换一并去除)。

只生成 `com_pet.proto` + `com_pet_team.proto`(大世界队伍)两个根的**依赖闭包**(由脚本从描述符
**动态求取并合并**,随 all.pb 版本而变,当前约 9 个文件:com_pet/com_base_types/com_battle_enum/
com_monster/com_pet_skill/com_season/rpc_options/xls_enum/com_pet_team),
用 `--go_opt=M...` 映射到单一 Go 包 `internal/pb`。
`all.pb` 不含 well-known 的 `descriptor.proto`(被 rpc_options 依赖),脚本用 protobuf 运行时
自带的描述符在内存里补进描述符集(见 `scripts/pbdesc.py`)。产物为 `internal/pb/*.pb.go`(已提交)。

核心结构 `PetData`(`com_pet.proto`)字段对应展示项：

| 截图字段 | PetData 字段 |
| --- | --- |
| 编号 | `gid`(实例唯一 id) |
| 种类 | `conf_id` → PET_CONF.name |
| 昵称 | `name`(玩家命名) |
| 系别 | `skill_dam_type`(repeated SkillDamType) |
| 性格 | `nature` |
| 性别 | `gender`(1=♂,2=♀) |
| 等级 | `level` |
| 身高/体重 | `height`/100 米、`weight`/1000 千克 |
| 天分 | `talent_rank` → PetTalentRate |
| 奖牌 | `wear_medal_conf_id` → MEDAL_CONF |
| 特长 | `speciality_id` → PET_TALENT_CONF.name |
| 标记 | `partner_mark` |
| 声音 | `voice` |
| 捕捉时间 | `add_time`(unix 秒) |
| 六维 | `attribute_new_info`(最终面板值，按 AttributeType 1-6 取) |

## 3. 名称表 → JSON(`scripts/gen_gamedata.py`)

从上述表提取精简 `id → 中文名` 写入 `internal/gamedata/data/names.json`(已提交)，
`internal/gamedata` 包编译期 `embed` 加载。包含维度：

```
species  nature  nature_effect  skill_dam_type  talent_rate
partner_mark  speciality  medal  images  image_base  opcodes
```

名称表由 `decode_bin.py` 解 `nrc/bin/` 得到;系别/天分/标记的整数值通过解析 `nrc/all.pb`
枚举(名→整数)再 join `PET_FILTER_CONF` 的(枚举名→中文)得到。种类合并 MONSTER_CONF+
PET_CONF，特长直接取 PET_TALENT_CONF，opcode 取自 `nrc/all.pb` 的 `ZoneSvrCmd` 全集
(枚举/opcode 均经 `scripts/pbdesc.py` 读描述符,与 `internal/pb` 同源),性别为硬编码。

### 宠物图片索引(`images` / `image_base`)

链路:`PetData.conf_id` → `MONSTER_CONF`/`PET_CONF` 行的 **`base_id`** → `PETBASE_CONF.id`(基础形态)
→ 全身图取 `PETBASE.JL_res`(`Pet1024/Pet256/JL_<拼音>`),头像经 `PETBASE.model_conf` →
`MODEL_CONF.icon`/`big_icon`(`HeadIcon/BigHeadIcon256/<n>`)。**文件名不能用 id 拼**——728 个
形态共用他人头像(如 3228 用 3012),全身图是拼音代号而非 id,故必须存表。

`gen_gamedata.py` 输出两张:`images`(petbase_id → `{h,b,p,ps,…}` 文件名,1041 项)与
`image_base`(conf_id → petbase_id,仅 base≠自身者,约 1.7 万项;base==自身者 Go 侧回退直查)。
`gamedata.PetImage(confID, shiny)` 据此拼出相对路径(`HeadIcon/3001.webp` 等),挂到 `Pet.Image`,
前端拼到 `/img/` 下。未上线宠(如占位的圣草帝魔)无美术资源,`PetImage` 返回空,前端给占位图。
> 实际形态以 `PetData.base_conf_id`(当前 petbase)为准:`ToPet` 优先用它取名称/头像/图鉴/形态,
> 缺失才回退 `conf_id`(进化线一阶 base)——否则已进化宠物会显示成基础形态(详见进化形态一节)。

**异色(shiny)变体**:部分宠物有专属异色美术——头像 `MODEL_CONF.shiny_icon`/`big_shiny_icon`
(形如 `3010_1`)、全身图 `PETBASE.JL_shiny_res`/`JL_small_shiny_res`(形如 `JL_<拼音>_yise`)。
`images` 仅在与普通版**不同**时额外存 `{sh,sb,sps}`(本版本 146/123/132 项;多数宠异色复用普通图)。
`PetImage(confID, true)` 在「索引有该字段**且**对应 webp 确已 embed」时才用异色图,否则回退普通——
故未导出异色 PNG 时异色宠仍显示普通美术,不会出现空图标。

图片本体(webp)**embed 进二进制**:把 `Common/Icon` 的 `HeadIcon`/`BigHeadIcon256`/
`Pet256` 子目录以 **PNG** 导出(`scripts/unpack.sh --only icons`,或 FModel 手动;异色图
`*_1.png`/`JL_*_yise.png` 在同目录,一并导出即可),
`uv run python scripts/gen_images.py <PNG源>` 转成 webp 落到 `internal/gamedata/data/img/`
(`//go:embed all:data/img`),`internal/server` 经 `/img/` 提供。
35MB 的 `Pet1024` 全身大图暂不 embed(体积考量),需要时把 `Pet1024` 加进 `gen_images.py` 的 `DIRS`。

**可复现 / 防 git 噪音**:同一 libwebp 版本下 PNG→webp 转码是确定性的(webp 无时间戳,
实测同源字节一致)。为此 `pyproject.toml` 把 pillow **钉死精确版本**且 `requires-python>=3.10`
(避免 3.9/3.10 解析到不同 pillow → 不同 libwebp → 全量图片 diff)。`gen_images.py` 还**默认跳过
已存在的 webp**:常规重跑零改动,游戏更新只为新增宠编码,libwebp 万一漂移也不动老文件;
换了 quality 等需整体重编时用 `--force`。

### UI 图标(`gen_icons.py`)

宠物头像/全身图之外的 UI 图标由 `scripts/gen_icons.py` 统一产出到 `internal/gamedata/data/img/<组>/`。
**webp 一律保持原始解包文件名**并按文件名**去重**(多个枚举值/id 复用同一资产时只存一份,故图标
数少于语义键数);语义键(enum/id)→ 原名 的映射由 `gen_gamedata.py` 写进 `names.json`。分五组、
两种资源机制:

| 组 | 数据源 | 内容 | 文件数 |
| --- | --- | --- | --- |
| `filter` | `PET_FILTER_CONF.filter_icon` | 系别(属性)18 + 六维 6+6(`AttributeType` 增益类/裸值同图,整数 1-6 即六维编号)+ 搭档标记 10 | 34 |
| `blood` | `PET_BLOOD_CONF.icon` | 24 条血脉主图标(18 属性系 + 6 特殊;异核/黑魔法共用) | 23 |
| `static` | 脚本内 `STATIC` 清单 | 人工挑选的杂项(异色/炫彩/污染、伙伴标记外框) | 5 |
| `worldmap` | 脚本内 `WORLDMAP` 清单 | 人工挑选的大地图 POI(炼金釜/魔力之源/守护地、矿石与植物标记、眠枭庇护所、蓝/黄/紫眠枭之星与精灵果实) | 13 |
| `medal` | `MEDAL_CONF.icon` | 55 枚奖牌小图(BagItem;部分奖牌共用) | 47 |

> `filter` 组只收 `filter_icons` 实际输出的三组枚举(`gen_icons.py` 的 `FILTER_ENUMS`,与
> `gen_gamedata.py` 同一白名单):2026-07 版 `PET_FILTER_CONF` 新增 **PetBloodType**(游戏内
> 血脉筛选)等组,其图标与 `PET_BLOOD_CONF` 同为 XueMai 图集精灵,照单全收会往 `img/filter`
> 重复转码 21 张 `img/blood` 已有的图。另注意该表**行 id 会整体重排**(2026-07 版 id 19 从
> PetTalentRate 变成了 PetBloodType),一切取用只认 `filter_enum_name`/`filter_enum_value`。

**两种机制**:
- **图集精灵(PaperSprite,`filter`/`blood`/`static`/`worldmap`)**——本身不含像素,从图集(`Texture2D`)按 UV
  裁一块。游戏包是 unversioned cooked 资产,`.uexp` 位打包无标签序列化(手写解析不可靠),故 UV
  矩形(`BakedSourceUV`/`BakedSourceDimension`)取自 FModel 的 **Save Properties(.json)**;图集本体
  经 **Save Texture(PNG)**(Frames 的 Save Texture 置灰,但其引用的 `Textures/` 图集可导)。脚本按
  `icon` 引用的完整路径定位 sprite JSON(`ui_pet_attribute_0N` 在 PetUI/PetSystem 两处同名,故不能只
  用 basename),再按同名 basename 回退(增益类指向 PetSystem,若只导出 PetUI 则取等价图标)。
- **整张贴图(`Texture2D`,`medal`)**——Save Texture 出的 PNG 直接转码,无需裁切(同宠物头像)。

`WorldMapNpc` 的 `Frames/` 下**混着两类资产**:数字名(`00102` 等)是各自独立的 256×256 `Texture2D`
(NPC 头像,未收录),语义名(`img_*` / `TipDes_*` / `Interestplace_*` 等)才是 PaperSprite;`worldmap`
只挑后者。其 `BakedSourceTexture.ObjectPath` 前缀为 `NRC/Content/...` 而非 `/Game/...`,`game_to_src`
的正则两种都认,无需特殊处理。

前置(`scripts/unpack.sh --only atlas` 一步完成;FModel 手动则导到下载根,默认
`~/Downloads/NRC`):对 `Common/Icon/Species/Frames`、
`PetUI/Raw/Atlas/PetUI/Frames`、`Common/CommonStatic/Frames`、`Common/Icon/XueMai/Frames`、
`System/BigMap/Raw/Atlas/WorldMapNpc/Frames` Save Properties(.json)并对各自 `Textures/` 图集
Save Texture(PNG);对 `Common/Icon/BagItem` Save Texture(PNG)。webp 转码确定性,默认跳过已存在、
`--force` 重编。

**索引/访问**:`gen_gamedata.py` 从 vendored 的 `PET_FILTER_CONF`/`PET_BLOOD_CONF`/`MEDAL_CONF`
(+ `all.pb` 枚举)生成 `names.json` 的三张「语义键 → 图标原名」索引(纯 vendored 数据、无需图片即可
重跑),`gamedata` 据此拼 `<组>/<原名>.webp` 并校验确已 embed(缺则返回空串):

| 索引 | 形状 | 访问器 |
| --- | --- | --- |
| `filter_icons` | `{组名: {枚举整数值: 原名}}` | `SkillDamTypeIcon` / `AttributeTypeIcon` / `PartnerMarkIcon(v)` |
| `blood_icons` | `{血脉id: 原名}` | `BloodIcon(id)` |
| `medal_icons` | `{奖牌id: 原名}` | `MedalIcon(id)` |

`static` / `worldmap` 无数据驱动(游戏侧由 UI 蓝图直接引用,`nrc/bin/` 各表均无引用),故无 Go 访问器,
前端按固定路径 `/img/<组>/<原名>.webp` 引用;新增往 `STATIC` / `WORLDMAP` 清单加一行并 Save Properties
对应 sprite。经 `//go:embed all:data/img` 收录、`/img/` 提供。血脉的 `icon_1`/`icon_flower` 等变体、
奖牌 `big_icon`(Item190 大图)暂不收录。

## 3.1 场景与大地图(`gen_gamedata.py` 的 scenes/scene_res/maps + `gen_bigmap.py`)

协议里 `ZoneEnterSceneRsp`(0x0152)、`ZoneSceneTeleportNotify`(0x015c)同时给两个 id:

| 字段 | 表 | names.json |
| --- | --- | --- |
| `scene_cfg_id` | `SCENE_CONF`(85 行) | `scenes`: id → 场景名 |
| `scene_res_cfg_id` | `SCENE_RES_CONF`(119 行) | `scene_res`: id → `{n:名称, s:所属 scene_cfg_id}` |

**大地图底图只有 4 个场景配了**(`WORLD_MAP_BLOCK_CONF`:10003 卡洛西亚大陆、10018 魔法学院、
30001 家园室内、30002 家园种植园)→ `maps` 索引。其余场景(副本/洞穴/室内)无底图,只能显示
场景名 + 原始坐标。注意 10018 的 `scene_res` 归属 `scene_cfg_id=103`(是大陆的子场景),
**进子场景时服务器下发的 res id 是父是子需实测**,可能要做一层子→父 block 的回落。

**坐标单位**:协议 `Position{x,y,z}` 就是 **UE 世界坐标,1 单位 = 1 厘米**,取整,无缩放
(客户端 `SceneUtils.ClientPos2ServerPos` 的 factor 默认 1.0)。两个坑:

- 玩家位置有 **85cm 的 Z 偏移**:移动包走 `PlayerPos2ServerPos`,内部 `Z - HALF_HEIGHT(85)`,
  即包里的 `to_pos.z` 是**脚底**高度,角色中心要 +85。
- `to_rot` / `Point.dir` **不是坐标而是旋转**:`FRotator × 10`(单位 0.1 度),且
  `x=Roll, y=Pitch, z=Yaw`;移动包里 Roll/Pitch 恒为 0,只有 `z`(朝向)有意义。

**投影**(复刻客户端 `BigMapUtils.ScenePosToImagePosF`):底图左上角 = 地块中心 − 边长/2,
世界坐标 → 底图归一化坐标,与底图分辨率无关:

```
ox, oy = map_center_position_xyz.xy − side_length/2      # 已算好存进 maps
u = (world_x − ox) / side      v = (world_y − oy) / side  # [0,1],直接乘底图像素宽
```

**底图**(`gen_bigmap.py`):游戏把每张地图切成 **4×4 共 16 张 1024² 瓦片,行主序**编号
(`piece = row*4 + col + 1`)。网页端不需要分块按需加载,拼成整图 + CSS 缩放平移即可。
输出 `img/bigmap/<scene_res_cfg_id>.webp`;家园室内按房屋等级分层,输出 `30001_<level>.webp`
(选层用 `ZoneEnterSceneRsp.home_room_level`)。世界地图保留原始 4096²,家园场景 2048² 足够;
8 张合计约 3.4MB(PNG 源 145MB)。

**已用真实 pcap 验证**(卡洛西亚大陆聆风塔→站内传送月牙湖岸→传送魔法学院学院广场):24/24 移动包
解析成功,场景状态机(进入/传送更新 res,移动包投影)正确,且**轴向无需交换/翻转**——世界 X→u
向右、世界 Y→v 向下、地图北=上(实测:向北飞 v 减小、向西走 u 减小),各点均落在对应地名上。

## 3.2 分层地图(洞穴/地下层,`gen_gamedata.py` 的 layers + `gen_bigmap.py`)

进入洞穴/地下层时,大地图把该层的**局部切片图**(`LayerMap/` 单张,不是 4×4 瓦片)**叠加在地表
底图之上**(不替换),坐标系不变(仍是所属 `scene_res_id`,如 10003)。数据源 `LAYERED_WORLD_MAP_CONF`
(15 行:每组 sort=1 是地表无图=用底图,sort=2+ 是各楼层带图)→ `layers` 索引(只收 9 个有图层)。

**选层机制:服务器的区域进/出事件(权威依据)**。`ZONE_SCENE_PLAY_ACTS_NOTIFY`(0x0414,s2c)里的
`enterted_catcher`/`left_catcher`(拼写即游戏原样)给出玩家**真正踩进/离开某个区域触发体**时的
`area_func_conf_id`;它命中本表的 `area_func_id`(写进 `layers[].afid`)即在该层。客户端同此:
`AreaAndZoneModule` 据这两个 act 维护玩家所在 zone 集合,`BigMapModuleData:GetCurMapLayerId` 再拿其中
的 `area_func_id` 查分层表。`DB.LayerIn(res, activeFuncs)` 即此。一个 func 可含多个 area(如信仰者村落
一层同时进入 541030265/541030499),故按 func 存 area 集合:离开其中一个仍在该层,集合空了才算离开。
区域进出只在跨越触发体时下发,故与场景 res 一样**落盘**(`sessions.areas`)供抓包重启恢复;换场景/
传送时清空(服务器不为旧区域补发离开事件,只在落地后重发进入事件;客户端同样清空,见
`AreaAndZoneModule:OnTeleportClearAreaInfo`)。

**层变化要能脱离移动包**:选层只在移动包上算的话,传送进洞后玩家站着不动就永远不叠图(实测落地后
3s 才等到第一个移动包,不动则永不)。故:①传送通知一到就按落点 `to_pt` 推一条位置(见 protocol.md 6);
②区域进/出事件到达时当场结算层,层变了就推一条只更新分层的消息(`layerOnly`,前端只叠加/撤下切片图,
不动位置锚点,免得用旧位置重置外推让箭头往回跳);③去抖中的待定变化借该连接的任意消息(心跳约 1.6s
一条)推进,不必干等移动包。实测(203934):传送落点即时可见,洞穴层在落地事件到达时(45.145)就叠上,
而非等到 3s 后的移动包;离开洞穴同理,比原来早 5s。

**必须去抖**(`layerDebounce`,300ms):触发体之间有接缝,在洞内正常走动会短暂「擦出」所有区域
(实测空窗 0/0/94ms,人明明还在地下室 z=-599),贴着楼梯口走也会短暂擦进上层(实测 107ms 的假进入);
照单全收则叠加图一闪一闪,看着像层图与底图不同步。真正的进出层空窗是秒级的(实测 3.8/5.1/15.7s),
差一个数量级,故只采纳「稳定超过 300ms」的变化。**换场景/传送后到首个移动包之间的「落地窗口」不去抖**
(`layerState.fresh`):此时玩家还没动,区域事件是落地时的权威状态、不可能是擦接缝的噪声;若照样去抖,
站着不动就没有下一个包来推进它,洞穴层图会一直不出现。客户端不受此扰是因为大地图是**面板**、开则取
当前值,而本项目是**实时**页面。(浏览器实测:底图与层图在同一个 transform 合成层里,120 帧内相对位移恒为
0.000px——抖动确实来自层图闪烁,不是渲染脱节。)

> **曾用「位置点在 AREA_CONF 多边形内」判定,已废弃**:多边形只有 x/y,而区域触发体是 3D 的,
> 于是**站在洞穴正上方的地表也会误叠洞穴图**(实测 021353:人还在家园一楼地面 z=3 就叠了地下室;
> 二叠山丘、二楼同理;472 个位置点里 119 个判错,全是抢先误叠)。客户端的 `GetLayerIdByPos` 只用于
> **地图标记点**判层,不是玩家选层。协议侧的 `cave_name`(`ZoneSceneClientCaveStateReq` 0x1838)则
> **只在传送进流送洞穴时才发,移动进入不发**(实测飞入月兔暗港无 0x1838),同样不可靠。

层的 `res`:优先 LAYERED 表 `scene_res_id`,家园层(表内为空)从其区域行(`area_func_id` →
`AREA_FUNC_CONF.area_id` → `AREA_CONF.scene_res_id`)补齐(=30001),否则 `LayerIn(30001,..)` 会漏掉家园
楼层。`AREA_CONF`(8.1MB/35592 行)与 `AREA_FUNC_CONF` 现已随仓库 vendored(为 3.3 的 POI 坐标一并
入库),`gen_gamedata.py` 直接读 `nrc/bin`,不再需要 `~/Downloads` 兜底。

**叠加渲染**:层世界范围 `[OX,OX+Side]×[OY,OY+Side]` 投影为底图归一化矩形(u0,v0)-(u1,v1)放进 payload
的 `layer{img,u0,v0,u1,v1}`,前端把切片图(`.map-layer`)按该矩形定位在 `.map-world` 内(透明处透出
底图,`.map-base`);玩家点仍用底图投影,自然落在矩形内。**进/出层不改缩放**(与外层保持一致):缩放/
跟随只在底图(`pos.img`,即换场景/家园等级)变化时重置,层图变化只重试层图。

**地图平移量必须对齐设备像素**:底图与层图是两个元素,浏览器绘制时各自把位置吸附到整像素;地图按
小数像素逐帧平移(实时跟随)时两者吸附时机错开,看起来就是层图与底图**错位抖动**。把平移量钉在设备
像素网格上(`applyFrame` 的 `snap()`,箭头同),层图的小数偏移即成常量,相对位置锁死。浏览器实测:
Firefox 152 小数平移抖 1.00px、对齐后 0.00px;Chromium 两者皆 0.00px(它把整张地图合成为一张纹理、
平移不重绘,故几乎看不出)。详见 [architecture.md](architecture.md) 7(含一次**无效**的尝试:把两图
合并为同一元素的两层 CSS 背景——Firefox 对两层背景照样各自吸附,仍抖)。

**切片图**(`gen_bigmap.py`):`LayerMap/<map_resource>.png` 单张转 `img/bigmap/layer/<map_resource>.webp`
(保持原名、保留 alpha);9 张约 860KB。

**已用真实 pcap 验证(洞穴 + 家园楼层)**:轨迹地表→飞入月兔暗港→传送信仰者村落一层(203934),及
家园一楼→二楼(z≈312)→一楼→地下室(z≈-598)(021353);服务端逐包推的 `layer` 与服务器区域事件
逐一吻合(移动进入触发、地表不误叠、楼层区分对、传送切层对),切片叠在对应位置、箭头落点对、缩放不变。
家园楼层切片(`camera_center`/`Ortho_width` 与家园底图一致)故矩形≈全图,楼层平面图覆盖整张家园底图。

## 3.3 大地图 POI(`gen_gamedata.py` 的 poi_kinds/pois + 实时地图页图层开关)

实时地图页可叠加显示 7 类地图标记(默认只开**魔力之源**与**炼金釜**;守护地、大/小型眠枭庇护所、
蓝/黄眠枭之星默认关,后两者有一两百个点)。图标本身早已由 `gen_icons.py` 的 worldmap 组切出
(`img/worldmap/<原名>.webp`),本节解决的是**它们在哪**。

**坐标要走三跳**(`WORLD_MAP_CONF` 是「地图元素」表:一行 = 一个可在大地图/小地图/罗盘显示的元素,
带图标与文案 `element_text_name`,但**本身没有坐标**):

```
WORLD_MAP_CONF.npc_refresh_ids → NPC_REFRESH_CONTENT_CONF[id].refresh_param
    refresh_type=1 → AREA_CONF[param].center_xyz           (炼金釜/魔力之源/守护地/眠枭之星)
    refresh_type=4 → SCENE_OBJECT_CONF[param].position_xyz (眠枭庇护所,actor 名 BP_NPCOwl_*)
```

坑与判据:

- `SCENE_OBJECT_AWARD` 与 `SCENE_OBJECT_CONF` **id 相同、含义不同**(前者是可采集物,如同 id 下是棵树),
  取错表会得到完全另一处的坐标。
- **只收游戏真会显示的元素**:`WORLD_MAP_CONF` 有 9 个显示开关(大地图/小地图/罗盘 × 未探索/已探索/
  未完成),全空 = 纯触发体。魔力之源就有 5 行「空npc,用于分层地图切换」(id 54001-54005,散落在真实
  魔力之源 65-260m 外),不滤掉会在图上多出 5 个假图标。**不能只看大地图那 3 个开关**——有的元素按设计
  只上小地图/罗盘(大地图开关全空)。刷新行的 `disable` 同样要跳过。

**眠枭之星不走 WORLD_MAP 匹配**,按 `NPC_CONF` id 白名单直取刷新行(`gen_gamedata.py` 的
`STAR_NPCS`)。口径 = **攻略/游戏总数**,一颗星有三种形态,每处算一颗:

| 形态 | 蓝(A1) | 黄(A2) | 紫(A2-2,2026-07 新区) | 说明 |
| ---- | ------- | ------- | ------- | ---- |
| 独立星 | 55162 × 98 | 55163 × 138 | 55601 × 60 | 常驻悬浮的星;蓝有 2 颗在风眠圣所(res 10013) |
| 光点   | 55500 × 28 | 55510 × 55  | 55602 × 26 | 交互后出一颗星 |
| 石像   | 58308 × 21 | 58318 × 35  | 55632 × 18 | 星星魔法命中后浮现一颗星,触碰收集;本体不消失(见 3.4) |
| 合计   | **147**    | **228**     | **104**    | 蓝/黄与三方攻略一致(蓝 147 = 1任务+5隐藏+141常规) |

发现过程:这批 NPC 靠 `NPC_CONF.min_map_disappear` 反查得到——字段名像「小地图消失距离」,实为
**WORLD_MAP_CONF.id 外键**(值全是合法 wm id 且全为眠枭之星系)。A1=蓝、A2=黄、A2-2=紫由
wm 30000/30001/30004 的图标文件名(lan/huang/zi)定出。石像无 wm 绑定,判据是 **`NPC_PENDANT_CONF`**
(挂件表,已 vendored):**带星石像的刷新行 id 与挂件表行 id 一一对应**,挂件的 `npc_id` 即挂着的
星(50206=蓝/50240=黄/50270=紫),对应 pcap 里的 `pendant_cfg_id`(见 3.4)。
**必须排除**的同族刷新行(否则蓝会虚增到 224、黄 194):

- 独立星里**刷新区域是多顶点**的行:石像关联的**奖励星预设落点**(蓝 94 行:51 单星 + 43 多星,
  区域 2/6/12 顶点)。实测石像的星走实体挂件、触碰即收(见 3.4),这些行未见刷出,不是常驻点位。
  **真星点的刷新区域全部单顶点**(三色全量验证),几何判别免维护(紫独立星无奖励行);
  **不能**用「距最近石像的距离」替代(奖励行最远离石像 394.6m,而真独立星最近仅 3.6m);
- **装饰石像**:58303-58305(A1)/58313-58316(A2)/55633、55635、55636(-A2 名)共 248 行,
  与带星石像**坐标互不重合**,行 id 不在挂件表里——石像上没挂自己的星,服务器也不计数。
  这些 NPC id 不入 `STAR_NPCS`;
- 50206「增加血上限_眠枭之星」(特殊星,大地图 6 行,另有 1 行 refresh_type=8 的任务刷星)与
  50240(废弃数据,1 行 2 星):**它们同时是蓝/黄石像的挂件星 npc**,
  但自身刷新行不进游戏区域计数,也不在攻略总数里;
- 55196/55197/55198(掉落版)、55530(挖光点)、50270(紫挂件星)、55002-55005:无启用刷新行。

**落库与投影**:`names.json` 的 `pois` 是 `scene_res_id → [{k:图层键, x, y, n:名称}]`(**世界坐标**,
厘米),`poi_kinds` 是有序图层清单(`k/n/icon/on`,`on` 即默认开启)。投影不在生成期做:后端
`GET /api/pois?res=<scene_res>` 用 3.1 的同一个 `db.Project` 把 x/y 换成底图归一化 u/v 再下发
(公式只此一处,与玩家位置同一套),前端只管开关与摆放(`.map-poi` 在 `.map-world` 内,随底图一起平移,
尺寸不随缩放变大)。只收**有底图的场景**:副本(20xxx,含守护地)与风眠圣所(10013)无底图不入库——
副本里的星(蓝 21:守护地 10 + 其他副本 3 + 光点 8;黄 40)按攻略口径本就忽略;风眠圣所那 2 颗蓝星
是 147 里仅有的暂不显示的点(大地图图层实为 145)。洞穴/楼层的点仍属地表 res(如 10003),照常落在底图上。

**图标不是方的**(魔力之源 54×66、精灵果实 47×55、庇护所 62×58…):CSS 只能定高、宽度按原始比例
自适应(`width:auto`),写死等宽高会把竖长的图标横向拉伸。图层开关在实时地图页的**左侧栏**(移动端
收进侧滑抽屉,复用宠物列表筛选栏的 `.filters`/`.filters-backdrop`/`.filter-toggle` 那套)。

**点数**(启用行,卡洛西亚大陆 10003;2026-07 大版本向东南扩了 8 个新区,坐标仍落在原 10003 投影
范围内,底图重新生成即可):魔力之源 43(另魔法学院 10018 有 4)、炼金釜 24(另家园种植园 30002 有 1)、
守护地 31、大型眠枭庇护所 37、小型 11、蓝色眠枭之星 145(+2 见上)、黄色 228(蓝/黄刷新行与旧版
完全一致)、紫色 104(全在新区坐标带;另有 2 独立星在副本、3 光点行 + 2 石像行 disable)。
「启用行」除 `disable` 外还须 `refresh_rule≠0`(规则表没有 id=0,rule=0 的行刷新系统从不刷出:
4 个炼金釜 700015-700018 实地无釜,2026-07-17 用户实证圣所前哨魔力之源东侧一例)且其
WORLD_MAP 行未标 `is_disable`(守护地搬家/换行留下的停用旧行:雪巨人旧址 wmc 13260、
不咕钟重复行 wmc 13256,显示开关还开着,仅此标记表明废弃)。
紫星按 CAMP_CONF 管辖区归属到新 8 区(见 3.4),其服务器分区计数已实测复核:8 区合计
60/26/18,与配置逐形态**精确相等**(紫星候选点全部计数,不像蓝/黄有不刷的多余候选)。
但截至 2026-07-17,紫星实体**尚未开放刷出**(wire 级扫描零出现,详见 3.4 的守卫说明)。

## 3.4 眠枭之星的收集状态(实时地图页「收集模式」)

**核心事实(pcap 实测)**:星/光点**已收集的服务器根本不刷**。未收集的才作为 NPC 实体(`ActorInfo`)
下发,实体的 `npc.npc_base.npc_content_cfg_id` 就是刷新点 id(= names.json 里 POI 的 `r`)。于是:

```
收到某点的实体            ⇒ 该点未收集
玩家走到某点附近却没实体  ⇒ 该点已收集
```

石像是**例外**,见本节末。

实体有两个来源(见 `internal/scene/star.go`):进场景/传送后的**周边快照**
`ZONE_SCENE_CLIENT_ENTER_SCENE_FINISH_NTY_ACK`(0x014a,`other_actors`),以及移动中随 AOI 变化
**持续补发**的 `ZONE_SCENE_PLAY_ACTS_NOTIFY/BATCH`(0x0414/0x0413,`acts.actor_enter.actors`
——与选层用的区域事件同一个通知)。实体离开(`actor_leave`)既可能是走远出 AOI,也可能是**刚被收走**,
只能靠距离区分:玩家不可能隔着几十米收集,故只在他就在旁边(30m 内)时才据此判已收集。
> 已实测(`rocom-20260715-031704`:传送风眠圣所 → 飞到三颗叠放的星旁 → 只收最下面那颗):三颗同 xy、
> 仅高度不同(z=16300/16800/17300),回放后**只有被收的 z=16300(refresh 1002506)判为已收集**,另两颗
> 仍是未收集。同趟飞行还顺带扫出 3 个已收集点(进了判定半径但无实体),逐点判定同样正常。

**AOI 是按格子下发的,不是圆形半径**:实测反复出现「更远的实体下发了、更近的没下发」(154m 未发但
170m 发了;127m 未发但 146m 发了),配置里也存在按格子号(如 `11032278_28*29`)跨 AOI 拆分的区域。故不能拿单一半径当 AOI 边界,只能取一个**保守判定半径**:
4 份 pcap 里凡距玩家轨迹 **≤100m 的固定 POI(必定存在的那些)全部下发,无一例外**,故代码取 **80m**
(`starSweepRadius`)留足余量。进场景后还要等快照到齐(`starSettle`)再判,否则会把「还没下发」当成
「已收集」。

**区域进度(仅展示)**:游戏内的星星**按区域计数**(商店街周边/月牙湖岸/风眠圣所…),进场景包里给出
每区域的「已收集/总数」:

```
self_info(11) → avatar(12) → world_map_info(19) → layered_world_map_explore_info(4)
  → explore_infos(1,重复) = {npc_id, belong_camp, explore_num, total_num}
```

`belong_camp` 的键是该区域**营地(魔力之源)的刷新点 id**;`WORLD_MAP_CONF` 里带
`zone_name` + `camp_refresh_id` 的行是区域行(43 行:旧 35 区 id 1-74 间散布,2026-07 新增
8 区为 id 8128-8135),据此翻成中文区域名(names.json 的 `zones`)。`npc_id` 按形态
分开计数(独立星/光点/石像各一条,同表还有精灵果实、智慧树苗等其它收集物,`starNpc` 只放行星星那
九个 id);后端聚合同区域各形态为一条进度(`/api/pois` 的 `zones`)。

**星点→区域的归属走权威外键链**(全部为随包发布的产品字段,43 区含新区全覆盖):

```
CAMP_CONF(行 id = 营地刷新点 id,即 belong_camp)→ manage_area_func(营地管辖区)
  → AREA_FUNC_CONF.area_id → AREA_CONF 多边形(每区恰一个)
```

相邻管辖区有**重叠带**,个别星点同时落入两区且归属无法静态定夺(实测「面积小者优先」「最近营地」
两种决胜规则都会与服务器分区计数矛盾),故 POI 的 `z` 是**候选区域列表**(477 点:454 单区 +
16 双区 + 7 不在任何管辖区):前端仅当列表非空且**全部**收满(每个候选 `got>=tot`)才隐藏——
方向永远安全,绝不误藏未收集的星。**校验方法**:回放库 `star_zone` 的分区分形态 `total` 是
服务器真值,每区每形态的候选点数必须 ≥ tot;当前链路两份 pcap 分别 93 行/117 行(含新 8 区紫星)
**0 矛盾**、86/109 行恰好相等(略多的是配置里不刷的候选点)。
> **勿再蹈的歧路**:按**区域名**匹配 `AREA_FUNC_CONF.name` 得到的是**播报触发体**(进区域时的
> 屏幕播报,互相大面积重叠,如「风眠圣所」的播报区盖到风息山口),归属会错 265/477 点——必须走
> `CAMP_CONF.manage_area_func` 外键。历史上曾按策划字段(区域名标注的多边形)归属,拿服务器
> tot 校验后发现 **51 行矛盾**(如给风眠圣所 0 颗蓝星而服务器 tot=15)——那套分区看着合理但
> 从来就是错的,已随策划字段剥离一并废弃。其余不成立的路子:`NPC_REFRESH_CONTENT_CONF.belong_camp`
> (星星那几百行全为空,该字段只有野生宠物在用)、`CAMP_CONTENT_NPC_CONF`(空表)、
> 「最近营地」归属(65 组只对上 11 组)。

**注意口径**:服务器区域计数**不含全部点**——蓝 94+27+19=140、黄 136+55+35=226(全图解锁后的合计,
pcap 实测),而配置/攻略总数为 147/228。差额有三种来历:少数星不计入任何区域(黄独立星 138−136=2,
即攻略说的「2 颗不计在区域内」);月牙湖岸有一颗蓝光点因 bug 被服务器**临时移除**(配置仍在,
28→27,曾实测 `got=2/tot=1`,**2026-07-17 版官方已修复为 `got=1/tot=1`**——`got>=tot` 判定作为
防御保留);蓝独立星 98 里也有 4 颗不计区域(计入 94)。故**不要拿配置点数当分母**算进度,
进度一律用服务器给的 `explore_num/total_num`,且 `got>=tot` 才算收满。

**「配置就位但未开放刷出」的守卫**(2026-07-17 实测踩坑):新区紫星 104 点配置、计数(tot)全部
就位,但服务器**根本不发实体**(wire 级 varint 扫描,紫星刷新行 id 在全部流量里零出现)——
「走近无实体 ⇒ 已收集」在此失效,玩家路过会把未开放的点全误判成已收集。守卫:**某点的候选区域
只要有一个「有计数行且 `got=0`」的,就不判已收集**(该区一颗未收,任何点「已收集」都不可能成立),
只是不判、照常显示。误判自愈:一旦星星真正开刷,玩家走近时实体出现,照常翻回未收集。
注意区分「`got=0`」与「**该区根本没有计数行**」:月兔暗港(camp 130175)在 `explore_infos` 里
一行都没有(该区不注册任何星),但望风半岛 4 个重叠带点的候选列表含它——没有计数行的区不可能是
真归属区,**跳过不挡**,否则这些点永远判不了已收集(`rocom-20260717-210612` 实测:玩家贴脸 2m
无实体仍不隐藏)。区域计数整体为空(还没抓到进场景包)时守卫无从工作,全部不判。

**结账时机**(2026-07-17 `rocom-20260717-223727` 实测踩坑):实体按**跨 AOI 格**触发下发,可晚于
玩家进 80m 判定圈 4-31s,晚到时玩家已近至 21-59m(12 份 pcap 共 5 例);圈边缘徘徊时延迟无上界。
「进圈无实体即判已收集」会**闪烁**:接近途中图标先消失,实体随后到达又翻回未收集、图标重现。
空间邻近也推不出「该格已下发」(实测有星点 20m 内他者实体早到 14s、星实体反而晚 31s——格边界
可以贴着点过)。故进圈无实体只记录**本场景最近距离 minD**,在两种实体必已下发的时机才结账:
**贴脸**(≤10m,实测最早晚到距离 21m 的一半)或**已过最近点回撤**(距离回升 ≥minD+15m,接近段
已结束,实体要来早来了;回撤结账在圈外也生效,擦圈边而过的点回撤时往往已出圈)。代价只是隐藏
推迟到走过之后几秒;12 份 pcap 新旧策略并行复演:零闪烁、无误判、无漏判(仅 pcap 截断处未及结账)。

**石像的判定是另一套**(`rocom-20260715-215606` 实测:登录在一座未收集石像旁 → 星星魔法命中 →
浮现蓝星 → 触碰收集):

- **石像本体收集后不消失**,实体一直下发——「出现/消失」不携带任何收集信息,绝不能按星/光点那套
  「出现 ⇒ 未收集」处理(会把已收集的石像全标回未收集,曾经踩过的坑)。
- 收集状态在**实体自身的挂件字段**里:`npc(11).pendant_info(11).pendant_item_infos(3).status(4)`
  (`ActorInfo_NpcPendant`/`NpcPendantItemInfo`,即石像上方约 4m 那颗星),**2 = 星还挂着(未收集),
  1 = 已收集**;`pendant_cfg_id` 恰为石像刷新行 id。于是走近石像(进 AOI)即知其状态,连扫描都不用。
- **收集瞬间**:星星魔法命中是投掷(c2s 0x0200/0x0202 `BEGIN/END_THROW`,END 带石像 actor/坐标);
  浮现的星**不是新实体**(全程无 actor_enter);触碰收集 = c2s 0x0272
  `ZONE_SCENE_NPC_PENDANT_INTERACT_REQ` `{npc_id:实体id, pendant_cfg_id:刷新行id, id:挂件序号}`,
  s2c 0x0273 `ret=0` + 0x0243 奖励通知。后端解 0x0272 拿刷新行 id、等 0x0273 成功即判已收集。
- 代码里(`starSee`)石像不进「实体离开 + 玩家在旁 ⇒ 被收走」的判定(它的离开只可能是出 AOI);
  `seen` 的语义(true ⇒ 未收集)对石像成立——挂件已收的石像不置 `seen`,故 80m 扫描无需分叉。

**前端**:图层栏的「收集模式」开关(默认关)。开启后隐藏两类点——所在区域已收满的、逐点判定为已收集的;
**其余一律仍显示**(宁可多显示,不能藏掉没拿的)。确认「还在」的点描一圈金色。状态按账号存库
(`star_state`/`star_zone`),玩家一边走后端一边判、经 SSE(`stars`)推增量。

## 4. 宠物列表解析流程(`internal/pet`)

```
s2c 0x1346 DATA 明文 body
  → ParsePetListRsp: protowire 取 field 4(pet_info)
  → proto.Unmarshal 成 PetDataInfoList → []*PetData
  → ToPet(pd, gamedata): pb.PetData + 名称库 → 业务模型 Pet(已中文化)
```

`ToPet` 完成单位换算(身高/体重)、枚举翻译(系别/性格/天分/奖牌/标记/特长)、
六维提取。离线回放 `sample.pcap` 实测解出 **543 只**宠物，与游戏内宠物总数一致。

### 六维 / 天分 / 性格

每项六维(`Stat`)含三部分：

- **最终面板值**(`value`)：取自 `attribute_new_info`(已含等级/努力/奖牌加成)。
- **天分**(`talentLv`)：取自 `attribute_info.*.talent_add_value`，即该维度的个体值 1–10
  (无天分则为 0)。宠物在 1–3 个维度上有天分。
- **性格影响**(`nature`)：性格使一维 +10%、一维 −10%。增减维度由权威性格表(30 种，
  按性格名匹配，见 `gen_gamedata.py` 的 `NATURE_TABLE`)生成 `nature_effect`;若用道具
  改过性格，则以 `changed_nature_pos/neg_attr_type` 为准。
  (注:`NATURE_CONF` 推导对个别性格如"平和"的 id 错位，故改用权威表。)

`talent_rank`(天分评级)由天分项数与是否和性格增益维度重合决定，实测吻合：
一般般=1项、还不错=2项、了不起=3项(1项与性格增益重合)、相当好=3项(不重合)。
例：火神固执(+物攻−魔攻)，天分在生命/物攻/速度三项，物攻与性格增益重合 → 了不起。

## 5. 已修复 / 待校准

已修复(实测对齐截图)：
- **种类名**：合并 MONSTER_CONF+PET_CONF,自有 FModel 提取 + `decode_bin.py` 解码,全量
  543 只 0 空种类。**修正了 pak-public-kit 的 PET_CONF 名整体错位**:其 `ELocalizedString`
  本地化对彩蛋宠(PET_CONF)整体偏移一位(3011001 误为"恶魔叮",应为"恶魔狼"),
  累计 4787 个彩蛋宠名错误;经 FModel + 两个独立 world-data 源三方比对确认后改用自有解码;
- **六维**：改用 `attribute_new_info` 最终面板值，火神 410/277/163/229/119/139 与截图完全一致；
- **天分/性格**：`talent_add_value` 修正为天分(1–10)而非性格修正；性格 ±10% 维度
  改由 `NATURE_CONF` 推导(火神固执=+物攻−魔攻),天分评级逻辑实测吻合；
- **特长**：取 PET_TALENT_CONF 中 `filter_enum_value=PTFN_TALENT_*` 的 11 种固定特长
  (无/奇袭/亲密/灵巧/疾行/同乘/无畏/爱分享/家里蹲/热心教/慈悲为怀),id=502 按游戏
  显示为"无畏"(表内 name 为"勇敢"),覆盖率 100%；
- **放生**：接入 `ZONE_PET_FREE_RSP(453)`，解析 `pet_gid`(field2)→ 从库中移除并刷新前端;
  宠物减少不计入捕获事件(捕获事件页只统计获得),故不记录也不推送事件;
- **孵蛋事件**：`ZONE_CRACK_EGG_RSP(780)` 用 `FindNewPet` 递归提取奖励
  (`ret_info.goods_reward.rewards[].pet`)中的新宠物 → 入库 + obtain(孵蛋)事件;
- **战斗外捕捉**：`0x1983`(赛季球/高级球，大 body 含新宠物)同样用 FindNewPet
  → 入库 + obtain(捕捉);与放生形成完整链(实测 #2 捕5放5、#3 捕3放3 全为菊花梨);
- **(普通)战斗内捕捉**：经 `ZONE_GOODS_REWARD_NOTIFY(0x0243)` 下发新宠物;
- **花种(稀兽)战斗内捕捉**：不走 `GOODS_REWARD`,新宠物经通用的 `ZONE_PLAYER_SYNC_NOTIFY(0x0160)`
  下发(实测 20497,`catch_way=4`)。该 opcode 通用(玩家数据同步),除 `FindNewPet` 严格判据外
  额外加 `add_time` 时近性守卫(相对本包时间,默认 120s 内)以防 PvP 对手/旧快照污染;
  实测 6 个样本里 0x0160 仅花种捕捉那一条携带有效新宠物,全量同步 543 只 0 误报;
- **传说精灵战后捕捉**：挑战传说精灵、击败后耗体力捕捉,新宠物**仅**经 `ZONE_BATTLE_FINISH_NOTIFY(0x132c,4908)`
  下发(实测 21692 凡雀,`catch_way=5`),不走 `GOODS_REWARD`/`PLAYER_SYNC`。与 0x0160 同为通用通知通道,
  故同样在 `FindNewPet` 严格判据外加 `add_time` 时近性守卫;实测普通/花种捕捉的战斗也会带 0x132c(与
  `GOODS_REWARD` 重复,靠 `isNew` 去重),而无捕捉的战斗其 body 不含带中文名的新宠 `PetData`,不误报;
- 上述五个获得 opcode(孵蛋/战斗外/普通战斗内/花种战斗内/传说精灵战后)统一处理:`FindNewPet` 加严格判据
  (conf_id>1000 且名称含中文)防误报,按 `catch_way` 区分子类型(1/4/5=捕捉、3=孵蛋),
  `isNew` 去重(同宠物可能多 opcode 下发);受赠宠物 `catch_way` 仍为 1 但应记「赠送获得」,
  见下「共同捕捉转赠」;
- **共同捕捉转赠(好友互赠)**:世界主人与到访好友「一起捕捉」一只宠物再转赠(4 小时转赠窗口),
  宠物 `PetData.together_catch_info`(#90)记录双方——`related_uin`=接收方、`catched_uin`=捕捉方
  (另有 `transfer_deadline` 等)。**捕捉与赠送是相互独立的事件**,两端分别处理:
  - **送出方**:先由捕捉回包 `0x1983` 照常记「捕捉」入库;之后开盒子手动赠送,经
    `ZONE_TOGETHER_CATCH_PET_FOR_GIFTING_RSP(0x1808)` 确认 → `ParseTogetherCatchGiftRsp`
    取 `pet_gid`(顶层 field3)从库中移除(减少不计入事件,不记录)。该 opcode 有两种回包且都在顶层带 gid:
    内嵌完整 PetData 的宠物详情(赠送前预览/同步)与紧凑 ack(仅 `ret_info`+gid);**只认后者**
    (内嵌 PetData 的返回 0),避免预览误删 + 两种回包重复处理;
  - **接收方**:受赠宠物经 `ZONE_GOODS_REWARD_NOTIFY(0x0243)` 下发(走 `FindNewPet` 入库),
    其 `catch_way` 仍为 1,故靠 `together_catch_info` 区分:`related_uin`==本账号 且 `catched_uin`≠本账号
    → 记 obtain「赠送获得」而非「捕捉」;
  - 判据**对称**、不依赖 opcode:送出方 `catched_uin`==本账号故仍记「捕捉」,接收方 `related_uin`==本账号
    故记「赠送获得」(`catchWayName` 据 `uidFromAcc(acc)` 判定)。实测两侧样本(20645 送出、20646 受赠)
    各自事件与宠物库变更均正确;
- **异色/炫彩**：`mutation_type` 为位标志,bit0=异色、bit3=炫彩(9 样本实测验证);
  炫彩的颜色/粒子细节(`glass_value`)不解析,仅记录是否炫彩。
- **盒子位置**：`PetData` 无位置字段,位置由仓库布局 `PetBackpackInfo` 表达——
  `ZONE_LOGIN_RSP(0x0102)` 登录数据(或盒子操作回包 6272-6292)携带 `boxes[]`,每个 `PetBox` 有
  `box_id`(**盒号即展示位置,1 起**)、`mark_type`(WarehouseMarkType:1首领/2污染/4奇异/8炫彩/16闪光)、
  `box_name`(玩家命名)、`lock`、`pet_gid[]`(**有序数组,每盒 30 格,空格=0**)。**位置 =(box_id, pet_gid[] 下标)**。
  `ParseBackpack` 取非零 gid 数最多的候选(排除误解析),展开为 gid→位置存入 `pet_box`(占用),
  同时把**全量盒子元数据**(含空盒:box_id/name/mark/lock)存入 `pet_boxes` 表;读取宠物时 JOIN 注入
  `Pet.Box`(盒名/标记以 `pet_boxes` 为权威,移入空的命名盒也拿得到盒名)。实测 0x0102 解出 ~525 只
  (27 盒),`/api/pets/21`→污染1 第11格。
- **盒子元数据(数量/名称/位置)**:盒子的存在性/盒名/标记/`box_id`(位置)独立于是否有宠物,存
  `pet_boxes` 表(与占用表 `pet_box` 解耦),故**空盒也可见**、盒数/盒名/换位都能表达。`BoxLayouts`
  从 `pet_boxes` 枚举全部盒子(含空盒)、占用格从 `pet_box` 填入。三条来源:
  - **全量**:登录/整理(`PetBackpackInfo`,`ParseBackpack`)与**整理排列**(改名/换位)的
    `ZONE_PET_BOX_SETTING_UP_RSP(0x1891)` 整体 `ReplacePetBoxMetas`+`ReplacePetBoxes`。后者不是
    `PetBackpackInfo` 而是裸的 `repeated PetBox`(挂顶层 field2),故 `ParseBackpack` 解不出时按
    `ParseBoxSettingUp` 再试。换位即 `box_id` 重排,整体替换让**盒内宠物随盒换位**。
  - **增量(单盒)**:解锁 `ZONE_PET_BOX_UNLOCK_RSP(0x1883)`(新盒挂 field2,盒数+1)、
    设标记/改名 `ZONE_PET_BOX_SET_MARK_TYPE_RSP(0x1893)`(自定义结构 `{ret=1,box_id=2,mark=3,name=4,lock=5}`),
    各自 `UpsertPetBoxMeta` 只动单盒——单独解锁/改名不必等下次全量/重登即时生效。
  - 实测两 pcap:①解锁盒29→改名 newbox→移到第20位(盒数28→29、box20=newbox);②重登后把
    18号盒`孵蛋`里的 20644 移入20号盒`newbox`(移入空命名盒仍正确显示盒名)——均正确落库。
- **队伍位置**：在队宠物**不在盒子里**,位置由 `PlayerPetInfo.team_infos`(同在 0x0102 登录数据)
  里 `team_type==PTT_BIG_WORLD(1)` 的 `PetTeamInfo` 表达——`teams[]`(最多 3 队,**队号取数组下标**,
  实测 `PetTeam.team_idx` 恒 0),每队 `pet_infos[]`(6 位)的 `pet_gid`。`ParseTeams`(取宠物数最多的
  大世界候选)→ gid→(队,位)存 `pet_team` 表,JOIN 注入 `Pet.Team`(与 `Box` 互斥);实测 3 队 18 只全命中。
  为此 `gen_proto.py` 把 `com_pet_team.proto` 加为第二根(闭包 +1 文件)。
- **位置移动增量**(运行期实时刷新):
  - **盒位**:`ZONE_PET_BOX_CHANGE_PET_RSP(0x1888)` 携带 `GoodsChangeItem.box_pet_change`
    (`PetBoxPetChange`:`pet_gid`/`is_in_team`/`id`=盒/`pos`=格,**pos 1 起**)。`ParseBoxMoves` 抽出
    非在队、gid 非 0 的落位项(`slot=pos-1`),`ApplyBoxMoves` 增量 upsert `pet_box`(盒名/标记取自
    `pet_boxes` 元数据,移入空盒也拿得到;元数据缺失才回退取该盒既有宠物行)并清其队位。
    **仅在 0x1888 解析**(其他 opcode 的子消息易误判为 PetBoxPetChange)。
  - **队位**:队伍变更/盒子操作回包(`CarriesTeam`:登录/6272-6292/524-527)常一并刷新完整队伍快照,
    复用 `ParseTeams` 整体 `ReplacePetTeams`。
  - 实测 pcap(交换队首两位 + 盒内 1→30 移位 + 盒内 2/3 互换):三处变更均正确落库。
- **宠物奖牌墙**(每只宠物拥有的全部奖牌):数据在 **登录 `0x0102`** 的 `PlayerSvrDataInfo.pet_medal_info`
  → `PlayerPetMedalInfo.medal_infos[]`(`PetMedalInfo`:#1 medal_conf_id / #2 medal_type / #3 owner 组[]),
  组内 #2 记录里宠物 gid = `#8(obtain_pet_gid) ?? #6 ?? #2`。**注:该消息线上 wire 格式与 all.pb 的
  `PetMedalOwnerInfo` 定义不一致(版本偏移),故 `pet.ParsePetMedals` 纯按 wire 经验解码**,不走 pb。
  解出 gid↔medal 存 `pet_medal` 表,读取时注入 `Pet.MedalIDs`(覆盖 `ToPet` 里仅佩戴的那枚);
  前端 `/api/medals` 全量奖牌 + `medalIds` 过滤出该宠物拥有的渲染奖牌墙。实测火神(gid=1)解出
  命定勇者/结伴同行/燃了鸭/同心相伴 4 枚。奖牌数据**仅完整登录携带**(普通/快速登录可能不含)。
- **多账号身份**:`ZONE_LOGIN_RSP(0x0102)` 取玩家 `user_id` 作账号键(`"UID:"+id`)——wire 三层
  下钻 `body → #2(LoginData) → #1(base) → {#1=user_id(varint), #3=nickname(bytes)}`
  (`pet.ParseLoginAccount`,实测两用户 839694713/873234858)。按 user_id 而非客户端 IP 归属
  (多台设备常经 NAT 共用同一 IP,无法区分);各账号数据在同库内按 `account` 列隔离,
  详见 [服务架构](architecture.md) 第 5 节「多账号隔离」。
- **宠物减少途径已覆盖**:游戏内无「删除宠物」操作入口,玩家能主动减少宠物的途径只有放生
  (`ZONE_PET_FREE_RSP(0x01c5)`)与赠送(共同捕捉转赠 `0x1808`),二者均已接入(见上)。
  协议里虽存在 `DELETE_REQ(397)`,但无对应 UI 入口、玩家不可触发,故无需接入。
- **别处放生的对账清除**:上述放生/赠送回包只在**抓包在线时**才能捕获;玩家在其他环境(未抓包)
  放生后回来重登,那批宠物不会经 `PET_FREE_RSP` 通知本服,只是从新的登录快照里消失。因登录快照
  (`ZONE_GET_PET_INFO_BY_PAGE_RSP(0x1346)`)只做增改、从不删,残留的旧宠会以「⏳位置待同步」滞留列表。
  为此对**连续一轮分页快照**(`req_page` 依次 1..`total_page`,注意 `page_num` 字段实为每页容量 50、非页序)
  累积全部 gid,在末页据完整快照 `PruneMissingPets` 清除库中缺席者;仅在 1..total 连续到达时触发
  (乱序/单独翻某页不触发,避免误删),且只删对账开始前就存在(`updated_at` 早于本轮起始)的宠物,
  放过对账期间刚捕获入库的新宠。

待校准(多数需含相应事件/宠物的新样本)：
- **咕噜球/技能名**本地化尚未梳理(蛋组已接入 `PET_LIKE_ELEMENT_CONF`,见上);
- **性格** `nature_id` 用 `AUDIO_NATURE_CONF`，个别可能与游戏显示略有偏差。
