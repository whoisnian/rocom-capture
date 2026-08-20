# 数据来源与解析

数据流程分两级,原始解包数据**不进仓库**,仓库只提交精炼后的生成物:

1. **原始 pak**:从游戏目录原样复制到 `~/Downloads/rocom/Paks/`
   (Windows 客户端 `<安装目录>\Win64\NRC\Content\Paks`;安卓 .apk 亦可直接喂给解包器)。
2. **全量解包**:`scripts/unpack.sh` 用 CUE4Parse 把 pak 尽可能全量导出到
   `~/Downloads/rocom/parsed/`(json/png/lua/bin 等,详见下)。
3. **生成物入库**:`scripts/gen_*.py` 直接读 `parsed/`,产出 `internal/pb/*.pb.go`、
   `internal/gamedata/data/names.json` 与 `img/` 下的 webp——这些**随仓库提交**并
   编译期 `embed` 进二进制,故 `go build` 开箱即用,运行时不依赖解包目录。

生成脚本用到的两类源(均在解包目录内,`ROCOM_PARSED` 环境变量可覆盖解包根):

- **游戏二进制配置 `NRC/Content/ScriptC/Data/Bin/`**:提供**中文名称表**。游戏自有的
  `.bytes`(数据)+ `.non`(schema)+ `BinLocalize/dev_CN`(本地化),由 `scripts/bin2json.py`
  解为紧邻的 `.json`(unpack.sh 已自动解码),生成脚本读该 JSON。
- **游戏描述符 `NRC/Content/ScriptC/Data/PB/all.pb`**:游戏自带的 protobuf 描述符
  (`FileDescriptorSet`,即运行时 `pb.loadufsfile` 加载的同一份),提供 `internal/pb` 的
  **字段号/类型**与 **opcode/枚举**。含字段号,可直接喂给 protoc 生成 Go,无需 .proto 文本。

字段号/枚举是**追加式**的(新版本只加不改号),故几乎无需跟版本更新;名称表随游戏内容变动。
要更新到新版本游戏:重新复制 pak、重跑 `scripts/unpack.sh`(增量),再跑各生成脚本。
行 id 同样跨版本稳定(实测大版本更新后星星刷新行 id 原样不动)。

> **全量解包(`scripts/unpack.sh`)**:从游戏 pak(目录或安卓 .apk)按虚拟路径镜像导出到
> `~/Downloads/rocom/parsed/`(顶层即挂载根 `NRC/Content/...`):`.uasset`/`.umap` 导出为
> 同路径属性 **.json**(含 PaperSprite UV 等全部导出属性),内含纹理的包另出同路径 **.png**
> (Texture2D 解码);其余文件(`.bytes`/`.non`/`.pb`/`.lua`/`.luac`/`.ini` 等)**原样字节**;
> `.uexp`/`.ubulk` 随包体读取不单独落盘。`Parallel.ForEach` 并行解码,**增量**跳过产物存在
> 且不比其来源 pak 旧的项(小版本补丁包 `_N_P` 多是原地改同名文件,只判存在与否会把这些改动
> 全部静默跳过、解包停在旧版本,故按 mtime 比对),`--list` 预览、`--filter` 按前缀选导、
> `--force` 全部重导。**默认排除**纯客户端运行时
> 资源(ArtRes 三维美术、Movies 视频、WwiseAudio 音频、AI 行为树、PVS/着色器/PSO 缓存、Engine;
> 约占全量 74G/80G,下游脚本零引用,清单见 `--help`),`--exclude <前缀>` 追加排除、
> `--no-exclude` 恢复真·全量。RenderTarget/视频纹理无像素数据,只出属性 json 不出 png
> (降级为记录、json 照写)。
>
> 导出后自动跑两个**后置步骤**(增量,`--no-post` 跳过;`--list`/`--help`/导出致命错时不跑):
> ①全树 RocoBinData `.bytes` → 紧邻 `.json`(`scripts/bin2json.py`,需 uv);②`.luac` → `.lua` 反编译
> (`scripts/decompile_luac.sh`,需 unluac)。`.luac` 本是标准 Lua 5.4 字节码(编译产物),
> unluac 反编译回可读源码(绝大多数成功);单文件 `timeout`(默认 60s,`LUAC_TIMEOUT` 覆盖)
> 兜住 unluac 对个别字节码的死循环,失败/超时打 `.lua.nodecomp` 标记、增量重跑跳过不再白耗;
> 空模块(源仅注释/空)合法解出空 `.lua`。
> C# 实现在 `scripts/unpack/`,基于 CUE4Parse 内置的 `GAME_RocoKingdomWorld` 支持(自定义
> AES 字节置换变体、Bin/luac 专属处理,无需 usmap)。**CUE4Parse 的 NRCLua 只解无头 luac,
> 漏了带 `{0xFA,0xE5,0xC0}+len` 头的那批(约占 9 成,其 AES 对整段解密 padding 失败),
> `scripts/unpack/patches/nrclua-luac-header.patch` 剥头修复,由 unpack.sh 幂等应用到克隆**。
> 依赖 dotnet-sdk 10+ 与 CUE4Parse 克隆
> (默认 `~/Git/gh/CUE4Parse`,`CUE4PARSE_DIR` 覆盖);首次运行自动下载 oodle/zlib-ng 到
> `~/.cache/nrc-unpack`。AES 主密钥默认值已内置在 `unpack.sh`(`DEFAULT_AES`,换密钥的版本用
> `--aes <hex>`/`@文件` 覆盖;与 Windows FModel `AppSettings.json → AesKeys` 同一把,该游戏
> 条目的 UeVersion=68812827 即 `GAME_RocoKingdomWorld`,usmap endpoint 未启用,口径一致)。

> **2026-07 大版本起,策划专用字段(editor_name、max_num、npc_pendant_id 等)从发布数据剥离**,
> 解析只可依赖仍随包发布的字段与表:石像奖励行按刷新区域顶点数排除、带星石像按 NPC_PENDANT_CONF
> 判定(见 3.3);星点→区域归属走 CAMP_CONF 管辖区外键链(见 3.4)。
(历史上名称/opcode 曾取自 pak-public-kit、字段号曾取自 world-data;现都被自有提取替代,
且修正了 pak-public-kit 的 PET_CONF 名整体错位 bug,见第 5 节。)

## 1. 名称表数据来源(解包目录 `ScriptC/Data/Bin/`)

Bin 目录下:

| 路径 | 内容 |
| --- | --- |
| `BinConf/*.non` | 表结构 schema(JSON,字段名/类型/偏移) |
| `BinDataCompressed/*.bytes` | 表数据(游戏自有压缩二进制) |
| `BinLocalize/dev_CN/*.bytes` | 本地化字符串(`ELocalizedString` 字段经此解析) |

`scripts/bin2json.py` 按 CUE4Parse 的 `FRocoBinData` 算法(自行实现,是全仓 `.bytes` 解码的
唯一实现)把全树 RocoBinData `.bytes` 解为紧邻的 `.json`:压缩/定长表 `{"RocoDataRows":{id:{...}}}`、
本地化 `{"LocalizationStrings":{...}}`(magic `0x53DF17BE` 识别,非此格式如 BigMap 的 `.bytes` 跳过)。
`gen_gamedata.py`/`gen_icons.py` 直接读 `BinDataCompressed/<表>.json`,不再自行解 `.bytes`。
opcode/枚举不在 Bin 里,取自 `all.pb`(见第 2、3 节)。unpack.sh 导出后自动解码,也可手动
`uv run python scripts/bin2json.py` 重跑(增量,秒级);之后直接 grep/jq。

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
  — 大地图 POI(炼金釜/魔力之源/…)的图标与坐标(见 3.3 节)。这几张是 Bin 里最大的
  (AREA_CONF 8.1M、NPC_REFRESH 3.3M),但坐标只能从它们来
  (`NPC_CONF` 2.4M 现已不被生成脚本读取,留作星星 NPC id/`min_map_disappear` 外键的查证依据)
- `NPC_PENDANT_CONF` — NPC 挂件(带星石像的判据与挂件星 npc,见 3.3/3.4;行 id = 石像刷新行 id
  = pcap 里的 `pendant_cfg_id`)
- `WORLD_EXPLORING_STATISTIC_CONF` — 探索统计注册表:「眠枭之星」行的 npc 清单即服务器
  explore_infos 计数的那批 npc_id(九个,与 STAR_NPCS/star.go 的 starNpc 同一批);生成脚本
  据此做防锈校验,新版本增删星 npc 会报警(见 3.3)
- `CAMP_CONF` — 营地表(行 id = 营地刷新点 id = explore_infos 的 belong_camp):
  `manage_area_func` 外键给出区域管辖多边形,是星点→区域归属的权威来源(见 3.4)
- opcode/系别/天分/标记的整数枚举取自 `all.pb`(`ZoneSvrCmd`/`SkillDamType` 等)

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

## 2.1 描述符 → pcapdump 精确解码(`scripts/gen_pbdesc.py`)

`internal/pb` 只覆盖宠物相关那几个消息(线上解析路径要静态类型),调试新协议时够不着。
`gen_pbdesc.py` 另出一份**运行时反射用**的生成物 `internal/pbdesc/data/`(已提交,embed):

- `opmsg.json`:opcode → 消息全名(1625 条)。映射表在客户端 `ProtoCMD.lua`
  (`[ProtoCMD.ZoneSvrCmd.X] = ".Next.Y"`),opcode 数值取 all.pb 的 `ZoneSvrCmd`/`ZoneSvrGmCmd`
  枚举,两边对得上才收(有 20 个消息名 lua 里有、描述符里还没有,跳过)。
- `proto.desc.gz`:裁剪过的 `FileDescriptorSet`(gzip 178KB)。只留从上述消息**字段可达**的
  消息与枚举(3085/3816 消息、170/1088 枚举),service/自定义 option 扩展全丢;
  被引用的嵌套枚举若其外层消息用不上,外层留个空壳撑住命名(否则解析报找不到类型)。

pcapdump 用它 + `dynamicpb` 解出带字段名/枚举名的树(`cmd/pcapdump/typed.go`)。消息在
`AppBody` 里的边界要试:头部 s2c 是 0、c2s 还剩 6 字节子头,尾部是 tsf4g 校验尾,
以 `"tsf4g"` 为锚在 `[起始 0..16] × [结束 tail-24..tail]` 里取「解出来没有未知字段 +
消费字节最多 + 回序列化长度一致」的候选。104 种 opcode 实测 103 种能精确解出,
唯一的例外 `0x013f ZONE_SCENE_HEARTBEAT_RESULT_NTY` 根本不是 protobuf(定长二进制结构),
自动退回通用 wire 级解码。

> 回序列化只比长度不比字节:Go 按字段**声明顺序**编码,服务端按**字段号**顺序,
> 本协议里两者常不一致,字节序列不同但长度必然相同。

## 3. 名称表 → JSON(`scripts/gen_gamedata.py`)

从上述表提取精简 `id → 中文名` 写入 `internal/gamedata/data/names.json`(已提交)，
`internal/gamedata` 包编译期 `embed` 加载。包含维度：

```
species  nature  nature_effect  skill_dam_type  talent_rate
partner_mark  speciality  medal  images  image_base  opcodes
```

名称表由 `bin2json.py` 解出的 Bin JSON 得到;系别/天分/标记的整数值通过解析 `all.pb`
枚举(名→整数)再 join `PET_FILTER_CONF` 的(枚举名→中文)得到。种类合并 MONSTER_CONF+
PET_CONF，特长直接取 PET_TALENT_CONF，opcode 取自 `all.pb` 的 `ZoneSvrCmd` 全集
(枚举/opcode 均经 `scripts/pbdesc.py` 读描述符,与 `internal/pb` 同源),性别为硬编码。

### 宠物图片索引(`images` / `image_base`)

链路:`PetData.conf_id` → `MONSTER_CONF`/`PET_CONF` 行的 **`base_id`** → `PETBASE_CONF.id`(基础形态)
→ 全身图取 `PETBASE.JL_res`(`Pet1024/Pet256/JL_<拼音>`),头像经 `PETBASE.model_conf` →
`MODEL_CONF.icon`/`big_icon`(`HeadIcon/BigHeadIcon256/<n>`)。**文件名不能用 id 拼**——461 个
形态的头像文件名不是自身 id(如 3242 用 3012),全身图是拼音代号而非 id,故必须存表。

`gen_gamedata.py` 输出两张:`images`(petbase_id → `{h,b,p,ps,…}` 文件名,1112 项)与
`image_base`(conf_id → petbase_id,仅 base≠自身者,约 2 万项;base==自身者 Go 侧回退直查)。
`gamedata.PetImage(confID, shiny)` 据此拼出相对路径(`HeadIcon/3001.webp` 等),挂到 `Pet.Image`,
前端拼到 `/img/` 下。未上线宠(如占位的圣草帝魔)无美术资源,`PetImage` 返回空,前端给占位图。
> 实际形态以 `PetData.base_conf_id`(当前 petbase)为准:`ToPet` 优先用它取名称/头像/图鉴/形态,
> 缺失才回退 `conf_id`(进化线一阶 base)——否则已进化宠物会显示成基础形态(详见进化形态一节)。

**异色(shiny)变体**:部分宠物有专属异色美术——头像 `MODEL_CONF.shiny_icon`/`big_shiny_icon`
(形如 `3010_1`)、全身图 `PETBASE.JL_shiny_res`/`JL_small_shiny_res`(形如 `JL_<拼音>_yise`)。
`images` 仅在与普通版**不同**时额外存 `{sh,sb,sps}`(本版本 220/196/204 项;多数宠异色复用普通图)。
`PetImage(confID, true)` 在「索引有该字段**且**对应 webp 确已 embed」时才用异色图,否则回退普通——
故未导出异色 PNG 时异色宠仍显示普通美术,不会出现空图标。

图片本体(webp)**embed 进二进制**:解包目录里 `Common/Icon` 的 `HeadIcon`/`BigHeadIcon256`/
`Pet256` 子目录已是 PNG(异色图 `*_1.png`/`JL_*_yise.png` 在同目录),
`uv run python scripts/gen_images.py` 转成 webp 落到 `internal/gamedata/data/img/`
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
  矩形(`BakedSourceUV`/`BakedSourceDimension`)取自解包出的**属性 .json**(`Frames/` 下);图集本体
  取其引用的 `Textures/` 图集 **PNG**(Frames 包自身无纹理,不出 PNG)。脚本按
  `icon` 引用的完整路径定位 sprite JSON(`ui_pet_attribute_0N` 在 PetUI/PetSystem 两处同名,故不能只
  用 basename),再按同名 basename 回退(同名资产任取等价一份)。
- **整张贴图(`Texture2D`,`medal`)**——解包出的 PNG 直接转码,无需裁切(同宠物头像)。

`WorldMapNpc` 的 `Frames/` 下**混着两类资产**:数字名(`00102` 等)是各自独立的 256×256 `Texture2D`
(NPC 头像,未收录),语义名(`img_*` / `TipDes_*` / `Interestplace_*` 等)才是 PaperSprite;`worldmap`
只挑后者。其 `BakedSourceTexture.ObjectPath` 前缀为 `NRC/Content/...` 而非 `/Game/...`,`game_to_src`
的正则两种都认,无需特殊处理。

用到的图集:`Common/Icon/Species`、`PetUI/Raw/Atlas/PetUI`、`Common/CommonStatic`、
`Common/Icon/XueMai`、`System/BigMap/Raw/Atlas/WorldMapNpc`(各自 `Frames/` 的 .json +
`Textures/` 的 PNG),以及 `Common/Icon/BagItem` 的整张 PNG——全量解包后即齐备,无需单独前置。
webp 转码确定性,默认跳过已存在、`--force` 重编。

**索引/访问**:`gen_gamedata.py` 从 `PET_FILTER_CONF`/`PET_BLOOD_CONF`/`MEDAL_CONF`
(+ `all.pb` 枚举)生成 `names.json` 的三张「语义键 → 图标原名」索引(纯 Bin 配置、无需图片即可
重跑),`gamedata` 据此拼 `<组>/<原名>.webp` 并校验确已 embed(缺则返回空串):

| 索引 | 形状 | 访问器 |
| --- | --- | --- |
| `filter_icons` | `{组名: {枚举整数值: 原名}}` | `SkillDamTypeIcon` / `AttributeTypeIcon` / `PartnerMarkIcon(v)` |
| `blood_icons` | `{血脉id: 原名}` | `BloodIcon(id)` |
| `medal_icons` | `{奖牌id: 原名}` | `MedalIcon(id)` |

`static` / `worldmap` 无数据驱动(游戏侧由 UI 蓝图直接引用,Bin 各表均无引用),故无 Go 访问器,
前端按固定路径 `/img/<组>/<原名>.webp` 引用;新增往 `STATIC` / `WORLDMAP` 清单加一行即可
(sprite .json 已在全量解包内)。经 `//go:embed all:data/img` 收录、`/img/` 提供。血脉的 `icon_1`/`icon_flower` 等变体、
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
楼层。`AREA_CONF`(8.1MB/35592 行)与 `AREA_FUNC_CONF` 同在解包 Bin 目录(为 3.3 的 POI 坐标一并
入库),`gen_gamedata.py` 直接读取。

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

实时地图页可叠加显示 9 类地图标记(默认只开**魔力之源**与**炼金釜**;守护地、大/小型眠枭庇护所、
蓝/黄/紫眠枭之星、不咕钟零件默认关,星星有一两百个点)。图标本身早已由 `gen_icons.py` 的 worldmap 组
切出(`img/worldmap/<原名>.webp`;不咕钟零件是 BagItem 整张贴图 100946,游戏 MEGAMAP_CONF 的
大地图钉直接复用背包图标,走 copy_texture 而非图集裁切),本节解决的是**它们在哪**。
`poi_kinds` 的 `collect` 标记「可收集图层」(三色星星 + 不咕钟零件):点带刷新点 id 参与收集判定
(3.4),后端按此标记替代早前的 `star` 前缀判断(`gamedata.CollectibleKind`)。

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
(挂件表):**带星石像的刷新行 id 与挂件表行 id 一一对应**,挂件的 `npc_id` 即挂着的
星(50206=蓝/50240=黄/50270=紫),对应 pcap 里的 `pendant_cfg_id`(见 3.4)。
**必须排除**的同族刷新行(否则蓝会虚增到 224、黄 194):

- 独立星里**刷新区域是多顶点**的行:石像关联的**奖励星预设落点**(蓝 94 行:51 单星 + 43 多星,
  区域 2/6/12 顶点)。实测石像的星走实体挂件、触碰即收(见 3.4),这些行未见刷出,不是常驻点位。
  **真星点的刷新区域全部单顶点**(三色全量验证),几何判别免维护(紫独立星无奖励行);
  **不能**用「距最近石像的距离」替代(奖励行最远离石像 394.6m,而真独立星最近仅 3.6m);
- **装饰石像**:58303-58305(A1)/58313-58316(A2)/55633、55635、55636(-A2 名)共 251 行,
  与带星石像**坐标互不重合**,行 id 不在挂件表里——石像上没挂自己的星,服务器也不计数。
  这些 NPC id 不入 `STAR_NPCS`;
- 50206「增加血上限_眠枭之星」(特殊星,大地图 6 行,另有 1 行 refresh_type=8 的任务刷星)与
  50240(废弃数据,1 行 2 星):**它们同时是蓝/黄石像的挂件星 npc**,
  但自身刷新行不进游戏区域计数,也不在攻略总数里;
- 55196/55197/55198(掉落版)、55530(挖光点)、50270(紫挂件星)、55002-55005:无启用刷新行。

**不咕钟零件**(2026-07 更新的收集品,道具 104501「能打开某个神秘守护地的大门」)与星星同为
NPC 白名单直取(`gen_gamedata.py` 的 `NPC_WHITELIST`,含 `STAR_NPCS` + `part_bugu`):
`MEGAMAP_GATHERING_CONF`「不咕钟零件」→ NPC 55901「A2-2-不咕钟零件」,90 行刷新候选中
39 行 `disable`,启用 51 行,与三方攻略全收集数恰好一致(策划从 90 个候选点里挑投 51 个)。
实体行为与星/光点完全同套(`rocom-20260718-201615` 实测:未收集才刷、
`npc_content_cfg_id`=刷新行 id;收集 = c2s 0x02fb CLIENT_OPERATION `action_type=10012` +
s2c 0x0243 奖励,实体随即离开——落进「实体离开+人在旁」判定,无需解 0x02fb)。交互 option
55601-55610(action_param1 220070001-220070010)是**按点位实例**在刷出通知里下发的,NPC_CONF
静态行只写 55601。零件**不在** `WORLD_EXPLORING_STATISTIC_CONF` 里——服务器不给分区进度,
点位不带候选区域(zone),收集模式只走逐点判定(3.4 的守卫与 bumpStarZone 对它自然不作用)。

**落库与投影**:`names.json` 的 `pois` 是 `scene_res_id → [{k:图层键, x, y, z, n:名称}]`(**世界坐标**,
厘米,`z` 为高度,供 3.4 的洞穴层守卫;星点另带 `r:刷新点id` 与 `zone:候选区域`),`poi_kinds` 是
有序图层清单(`k/n/icon/on`,`on` 即默认开启)。投影不在生成期做:后端
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
完全一致)、紫色 104(全在新区坐标带;另有 2 独立星在副本、3 光点行 + 2 石像行 disable)、
不咕钟零件 51(见上)。
「启用行」除 `disable` 外还须 `refresh_rule≠0`(规则表没有 id=0,rule=0 的行刷新系统从不刷出:
4 个炼金釜 700015-700018 实地无釜,2026-07-17 用户实证圣所前哨魔力之源东侧一例)且其
WORLD_MAP 行未标 `is_disable`(守护地搬家/换行留下的停用旧行:雪巨人旧址 wmc 13260、
不咕钟重复行 wmc 13256,显示开关还开着,仅此标记表明废弃)。
紫星按 CAMP_CONF 管辖区归属到新 8 区(见 3.4),其服务器分区计数已实测复核:8 区合计
60/26/18,与配置逐形态**精确相等**(紫星候选点全部计数,不像蓝/黄有不刷的多余候选)。
但截至 2026-07-17,紫星实体**尚未开放刷出**(wire 级扫描零出现,详见 3.4 的守卫说明)。

## 3.4 眠枭之星/不咕钟零件的收集状态(实时地图页「收集模式」)

本节以眠枭之星为主;**不咕钟零件与星/光点完全同一套判定**(实体白名单 `scene/star.go` 的
`starNpc` 含 55901,管线全复用),差别只有:无分区进度(不在探索统计表里,点不带 zone,got=0
守卫逐点放行、bumpStarZone 对它 no-op)、无石像/挂件形态。判定已用 `rocom-20260718-201615`
回放验证:拾取点判已收集(实体离开+人在旁)、路过的历史收集点判已收集(判定圈扫描)、
未拾的邻点保持未收集,且星星各表与改动前回放逐行一致。

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

**不能拿单一半径当 AOI 边界**:实测反复出现「更远的实体下发了、更近的没下发」(154m 未发但
170m 发了;127m 未发但 146m 发了)。曾据此以为 AOI 是按格子下发的(配置里确有按格子号如
`11032278_28*29` 跨 AOI 拆分的区域),**2026-08-16 逐类复测后修正**:边界其实是**每个 NPC 各自配的
水平圆半径**(星/光点 80m、果树与首领 150/200m),跨类比较才显得远近颠倒,再叠上「跨进边界到实体
真正下发有 6–47s 延迟」——详见 3.7。对本节的判定而言结论不变:仍只能取一个**保守判定半径**:
4 份 pcap 里凡距玩家轨迹 **≤100m 的固定 POI(必定存在的那些)全部下发,无一例外**,曾据此取 80m;
但 2026-07-19 用户实测 80m 下仍偶发「接近途中图标先消失再出现」(结账后实体才到),故收窄到 **50m**
(`starSweepRadius`)——判定圈越小实体下发越可靠、回撤结账位置也越近,代价只是要走得更近才能确认。进场景后还要等快照到齐(`starSettle`)再判,否则会把「还没下发」当成
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
两种决胜规则都会与服务器分区计数矛盾),故 POI 的 `zone` 是**候选区域列表**(477 点:454 单区 +
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

**收集当下的分区进度**:服务器只在进场景包里给全量进度,收集时不推增量(全部 pcap 核实:
0x01DC/0x01DF worldmap 通知从未出现,收集时刻只有 AOI 通知 + 0x0243 奖励)。为让「收完某区
最后一颗 ⇒ 该区其他星整片隐藏」即时生效,后端在两条**刚收走**路径(实体离开+在旁、挂件交互
成功)上把该点唯一候选区域的本地 got +1 并广播(`pipeline.bumpStarZone`);按「库内尚未是已收集」防重,
双候选点不归因、`sweepStars` 补判的历史收集不计(服务器 got 已含),下次 0x0152 全量校准。
`rocom-20260717-223727` 验证:港口驻地一次会话收了石像星+光点,got 0→2 恰与两条路径各一次吻合。

**结账时机**(2026-07-17 `rocom-20260717-223727` 实测踩坑):实体按**跨 AOI 格**触发下发,可晚于
玩家进判定圈 4-31s(80m 圈时代的实测),晚到时玩家已近至 21-59m(12 份 pcap 共 5 例);
圈边缘徘徊时延迟无上界。
「进圈无实体即判已收集」会**闪烁**:接近途中图标先消失,实体随后到达又翻回未收集、图标重现。
空间邻近也推不出「该格已下发」(实测有星点 20m 内他者实体早到 14s、星实体反而晚 31s——格边界
可以贴着点过)。故进圈无实体只记录**本场景最近距离 minD**,在两种实体必已下发的时机才结账:
**贴脸**(≤10m,实测最早晚到距离 21m 的一半)或**已过最近点回撤**(距离回升 ≥minD+15m,接近段
已结束,实体要来早来了;回撤结账在圈外也生效,擦圈边而过的点回撤时往往已出圈)。代价只是隐藏
推迟到走过之后几秒;12 份 pcap 新旧策略并行复演:零闪烁、无误判、无漏判(仅 pcap 截断处未及结账)。

**洞穴层守卫**(2026-07-19 用户实测踩坑):部分星点落在洞穴/分层地图**里**,玩家走在其正上方的
地表时平面距离可以贴脸,但服务器按实际空间下发 AOI、实体不会来——纯 2D 判定会把这些未收集的点
误判成已收集,且不进洞永不自愈(实体没机会出现翻回),下次路过洞顶还会再误判一次。POI 现带世界
坐标 `z`(高度,来自刷新点配置的 `position_xyz`/`center_xyz` 第三维,此前生成时被丢弃),sweep
在 minD 之外同步维护**进圈期间与该点的最小高度差 minDz**,结账另要求 `minDz ≤ 8m`
(`starLayerDz`):同层走近时脚底与星点高差通常仅数米(星略浮空 + 地形起伏),洞穴层与地表的
高差远大于此(家园地下室与一楼已差 6m,野外洞穴更深)。超限只是不判、照常显示,方向安全;
代价是「站在高台边缘俯视下方星点」这类同场景大高差的结账推迟到真正走近同层。
「实体离开+人在旁」路径同加此守卫(防出洞后站在洞顶时,洞内实体出 AOI 的离开事件被平面距离
误认作「刚收走」)。8m 为初值,尚未经 pcap 全量复演校准;若再遇浅洞误判可收紧,遇同层漏判可放宽。

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
  `seen` 的语义(true ⇒ 未收集)对石像成立——挂件已收的石像不置 `seen`,故判定圈扫描无需分叉。

**前端**:图层栏里每个可收集图层(`kinds[].collect`)的行右侧各有一个收集模式小开关(✓,默认关,
无文字说明,按图层独立记忆于 localStorage;图层未开启时禁用,只有开着图标才能启用收集模式)。开启后隐藏该图层两类点——所在区域已收满的、逐点判定为已收集的;
**其余一律仍显示**(宁可多显示,不能藏掉没拿的)。确认「还在」的点描一圈金色。状态按账号存库
(`star_state`/`star_zone`),玩家一边走后端一边判、经 SSE(`stars`)推增量。

## 3.5 野生宠物的个体属性(实时地图页「野生宠物」图层)

大世界里的野生宠物就是普通 NPC 实体(`ActorInfo`),**个体属性直接挂在 `npc_base` 上**,
来源与星星同两处:进场景/传送后的周边快照(`0x014a` 的 `other_actors`)、移动中随 AOI 补发的
`actor_enter`(`0x0413`/`0x0414`)。解析见 `scene.NpcActor`/`parseActorInfo`。
**下发范围**:普通野生宠是**水平 80m 的圆**(离开 88–90m 才撤,高度不计),首领 150/200m;
跨进边界到实体真正下发另有 6–47s 延迟——见 3.7。

**捕捉前后一致的属性**(2026-08-02 `rocom-20260802-190313.pcap00` 实测:捕捉珀尔鼬与友爱天天,
把野生侧 `npc_base` 与捕捉后 `ZONE_GOODS_REWARD_NOTIFY`(0x0243)里的 `PetData` 逐字段核对):

| 野生 `npc_base` / `base` | 捕捉后 `PetData` | 珀尔鼬 | 友爱天天 |
| --- | --- | --- | --- |
| `base.lv`(11) | `level` / `catch_lv` | 38 | 13 |
| `height`(11) | `height` | 74 | 32 |
| `weight`(12) | `weight` | 11048 | 3360 |
| `voice`(31) | `voice` | -75 | 100 |
| `mutation_type`(14) | `mutation_type` | 0 | 0 |
| `glass_info`(30) | `glass_info` | GT_NULL | GT_NULL |
| `npc_cfg_id`(1) →`NPC_CONF.traverse_data_param` | `base_conf_id`/`catch_base_id` | 10782→3758 | 10254→3228 |
| `blood_mix_skill_dam_type`(20),无则 `blood_normal_…`(27) | `blood_id`(经 `PET_BLOOD_CONF.blood_type`) | 2 → 血脉 1 | 2 → 血脉 1 |

身高/体重逐个体随机、落在 `PETBASE_CONF` 的 `height_low/high`、`weight_low/high` 内(同批三只
友爱天天为 32/32/35,叫声 54/100/-51),故**丢球之前就能筛**身高、体重、叫声、血脉、等级。
元素属性(`skill_dam_type`)取自 `PETBASE_CONF.unit_type`,是种族固定值,前后一致是当然的。

> **上表的 `mutation_type` 与 `blood_id` 只对「直接丢球捉到」的个体成立。**
> 被污染的个体要打一场,战斗会把污染洗掉,这两项随之变化——见下「污染个体」。
> 等级/身高/体重/叫声则连打带捉全程不变(2026-08-02 污染爬爬实测)。

**捕捉后才有**:个体值(`attribute_info.*.talent`/`talent_rank`)、性格 `nature`、性别 `gender`、
特性 `speciality_id`、技能表,以及 gid/exp/add_time/caught_camp 等。野生包里没有这些。

**两个坑**:`npc_base.nature`(13)对全场 26 只野生宠**恒为 30**,是占位常量而非性格
(真性格 22/20 只在捕捉后出现,已用 `nature_effect` 的 pos/neg 与 `attribute_new_info` 的
type 79–84 交叉验证);`npc_base.world_nature`(15)等于 `PETBASE_CONF.world_nature`,是种族常量。
另有只在野生侧存在的 `height_scale`(量化为 n/130),`PetData` 无对应字段。

**是不是野生宠物,两道闸**(`scene.NpcActor.IsWildPet` + `gamedata.DB.NpcPetBase`):

1. 实体自带 `height`+`weight` ⇒ 是一只宠物实体(静态 NPC/传送点/采集物/星星都没有这两项);
2. 该 `npc_cfg_id` 在**可丢球捕捉**清单里(`npc_pets`,由 `NPC_CONF.throwing_interact_type ∈ {1,4}`
   且 `traverse_data_param` 指向 `PETBASE_CONF` 生成,1483 行;其中 `type=4` 的**首领**另出一张
   `npc_bosses`(116 行),涂地要排除它们,见 3.8)⇒ 是野外能抓的。只靠第 1 道会把
   **家园里摆着的自己的宠物**也算进去(实测幽星光 710346、鸭吉吉 710012 同样带身高体重叫声,
   但 `NPC_CONF` 没有 `throwing_interact_type`)。该表同时用于取形态名与头像。

**变异(`mutation_type`)是位标志**,取值即客户端 `Enum.MutationDiffType`
(`Data/Config/Enum.lua`),`npc_base` 与 `PetData` 同一套:

| 位 | 枚举 | 含义 | 客户端渲染入口(`PetMutationUtils.UpdateMutation`) |
| --- | --- | --- | --- |
| 1 | `MDT_SHINING` | 异色 | `SetColorDiffMutation` |
| 2 | `MDT_CHAOS` | 噩梦一型 | `SetNightmareFirstMutation` |
| 4 | `MDT_CHAOS_TWO` | 噩梦二型 | `SetNightmareSecondMutation` |
| 8 | `MDT_GLASS` | **炫彩** | `SetGlassyDiffMutation` |
| 32 | `MDT_CHAOS_THREE` | 噩梦(按 id 掩码) | `SetNightmareByIDMask` |
| 64 | `MDT_VACANT` | 空缺态 | `SetVacantMutation`(客户端 `UIUtils` 不给它出变异标,本项目同样忽略) |
| 128 | `MDT_CHAOS_PRIMORDIAL` | 太初噩梦 | `SetNightmarePrimordial` |

**炫彩 = `MDT_GLASS` = `glass_info` 非空,三者是一回事**(全部 pcap 的 363 只变异宠物零反例)。
`glass_info{glass_type, glass_value}` 只是进一步说明**是哪一种**炫彩:

- `glass_type=GT_HIDDEN(2)` → `glass_value` 是 `HIDDEN_GLASS_CONF.id`,即**隐藏炫彩**名
  (1 暗夜拾光、2 狂欢怪谈、3 铅字幻梦、1000 黑白;名称带富文本标签,`gen_gamedata.py` 剥掉后存
  `glass_names`);
- `glass_type=GT_COMMON(1)` → `glass_value` 是**打包色号** `(粒子id << 20) | 配色id`
  (客户端 `PetUtils.GetShineDataValue` 即按 20 位拆,见 `UMG_Pet_DazzlingTips_C:ShowNormalGlassInfo`),
  分别查 `PARTICLE_RANDOM_CONF`/`COLOR_RANDOM_CONF`,如 1048609 → 「四角星·亮X暗 - 浅紫橙」。
  **不要拿它去查 `HIDDEN_GLASS_CONF`**——那是两套编号。

组装成中文描述的是 `gamedata.DB.GlassDesc(glassType, glassValue)`。宠物列表里的
`Pet.Colorful`(`mutation_type & 8`)与这里的炫彩是同一件事,只是那边不解析具体外观。

**污染个体**(`mutation_type` 的 `MDT_CHAOS` 家族;游戏文案叫「污染」,客户端渲染函数叫
`SetNightmare*`,同一件事)。野外实测 4 只全是 `MDT_CHAOS_TWO(4)`。它与其它稀有个体**流程不同**
——**丢球不会直接捉住,而是进战斗**(2026-08-02 `rocom-20260802-204107.pcap00`,污染爬爬全流程):

```
0x0200/0x0202 丢球  →  0x1316 BATTLE_ENTER_NOTIFY(敌方 mutation_type=4, nightmare_elite_id=100000)
  →  0x131a 回合1(仍 mutation_type=4)  →  打空血量
  →  0x131a 回合2(mutation_type **变 0**,污染解除;nightmare_elite_id 仍在)
  →  0x0243 GOODS_REWARD_NOTIFY 给出 PetData  +  0x132c BATTLE_FINISH_NOTIFY(result=TRUE_BATTLE_RESULT_WIN_CATCH)
```

捕捉前后对照(野生污染爬爬 → 捕捉后 gid 36251):

| 字段 | 野生(污染中) | 捕捉后 | |
| --- | --- | --- | --- |
| `lv` / `height` / `weight` / `voice` | 44 / 38 / 6054 / -55 | 44 / 38 / 6054 / -55 | **不变** |
| `mutation_type` | 4 | 0 | 战斗中洗掉 |
| 血脉 | `blood_normal_skill_dam_type=1`(SDT_NONE,即 `blood_type=1` 那批特殊血脉) | `blood_id=11` 虫系血脉(= 种族 `unit_type[0]`) | 随污染解除而变 |
| `nightmare_elite_id` | 野生侧**没有这个字段** | 100000 | 捕捉后才出现,是「曾被污染」的印记 |

`NIGHTMARE_ELITE_CONF` 100000 给的是**战斗中**的强化(体型 ×1.4、护盾/攻防 ×2、`IV_fix_min/max=8/10`);
这些**不进捕捉后的 PetData**——实测这只捉到手是 `talent_rank=1`、个体值 0/0/0/0/0/7,就是普通爬爬
(与用户观察一致)。污染的真正甜头在血脉:`PET_BLOOD_CONF` 23「污染血脉」的描述说
「削弱精灵的魔力后,一部分噩梦能量**可能**会残留在精灵身上,形成独特的污染血脉」——即打完有几率
留下 blood 23;这次没留下(拿到的是种族普通的虫系血脉 11)。

野生污染个体在 `ActorInfo` 里另有两处旁证(判定用不到,排查时有用):`attrs.hp` 是实数
(191/191,普通野生宠是 0/0——它要打,所以有血条)、`misc_info.size_scale=0`(普通野生宠是 100)。

**位置只有一次**:标记位置就是实体下发时的位置,之后不再更新。野生宠的 AI 跑在客户端
(`NpcBase.is_server_ai` 为假),它在刷新点附近溜达根本不过网——16 份 pcap 里 `server_move`(12)
只出现 1 次、`client_move`(11)3387 次全属玩家 avatar,没有一条属于野生宠;`set_npc_pos`(222)与
`npc_mutation_info_change`(515)一次都没出现过。故标记位置≈刷新点,误差是它自己绕的那几米。

**体重/嗓音稀有条件**:体重使用协议原始 `weight` 与当前形态
`PETBASE_CONF.weight_low/high` 做交叉相乘比较;普通条件沿用奖牌百分位窗口,
MAX 精确比较上下限,两者都不使用界面展示值的两位小数舍入:

| 后端 `kind` | 条件 |
| --- | --- |
| `weight-big` | 体重百分位 `>= 98%`(大块头,包含 MAX) |
| `weight-small` | 体重百分位 `<= 2%`(小不点,包含 MAX) |
| `weight-big-max` | 协议原始 `weight == weight_high`(精确 100%) |
| `weight-small-max` | 协议原始 `weight == weight_low`(精确 0%) |
| `voice-high` | 协议原始 `voice >= 96`(婉转声,包含 MAX) |
| `voice-low` | 协议原始 `voice <= -96`(粗嗓门,包含 MAX) |
| `voice-high-max` | 协议原始 `voice == 100`(婉转声MAX,声音 100%) |
| `voice-low-max` | 协议原始 `voice == -100`(粗嗓门MAX,声音 0%) |

**前端**:图层栏「野生宠物」的开关由 `WILD_LAYERS` 定义,选择记忆于 localStorage:

| 开关 | 覆盖的后端 `wildKinds` | 默认 |
| --- | --- | --- |
| 异色/炫彩 | `shiny` + `colorful` | **开** |
| 污染 | `pollution` | 关 |
| 大块头 / 小不点 | `weight-big` / `weight-small` | 关 |
| 大块头MAX / 小不点MAX | `weight-big-max` / `weight-small-max` | 关 |
| 婉转声 / 粗嗓门 | `voice-high` / `voice-low` | 关 |
| 婉转声MAX / 粗嗓门MAX | `voice-high-max` / `voice-low-max` | 关 |

`OR` / `AND` 是两套**分别记忆、同时生效**的条件页签,按钮高亮只表示当前正在编辑哪套:

- `OR` 条件集:命中任一已勾选条件即显示;
- `AND` 条件集:体重组内取 OR、声音组内取 OR,已选择的不同属性组间取 AND;
- 两套结果最终取并集。大块头/小不点、婉转声/粗嗓门及四个 MAX 都可加入任一条件集,
  MAX 加入 `AND` 时归入对应的体重或声音组;
- 「异色/炫彩」「污染」排除在两套条件之外,使用跨页签共享的独立开关,命中即附加显示。

例如在 `AND` 页签勾选大块头、小不点、婉转声、粗嗓门,在 `OR` 页签勾选四个 MAX,另开启
异色/炫彩与污染,最终条件是:

```
((大块头 OR 小不点) AND (婉转声 OR 粗嗓门))
OR 异色 OR 炫彩 OR 污染
OR 大块头MAX OR 小不点MAX OR 婉转声MAX OR 粗嗓门MAX
```

「异色/炫彩」本身仍是复合按钮,两种 kind 任一命中即可。三套选择以 `.v4` 对象存储;
旧 `.v3` 单条件集按原先保存的 OR/AND 模式迁入对应的新条件集,异色/炫彩与污染迁入共享开关;
更早 `.v2` 的 `voice`(原本就是精确 +100)先迁移为 `voice-high-max`。`map.wildMatchMode.v1`
现在只记忆侧栏最后编辑的页签,不再关闭另一套条件。

标记是圆形头像(异色个体用异色头像)+ 类别描边:每个开关有独立颜色,复合命中时
`wildRing` 将当前开启且命中的条件色做等权 RGB 混合。MAX 会取代同方向的普通条件色,
避免同一维度重复加权。标记不可点击,
只有悬浮说明,故用 `cursor: help`(别用 `pointer` 骗点击),悬停另放大 1.15× 便于确认指到了哪只。
提示格式与事件页那行对齐,一眼能对上:

```
{种类} Lv.44 异色炫彩 W 19.00% V -55
```

`W` 是体重在本形态取值范围内的百分位(后端用 `pet.SizePercentile` 算好放进 `weightPct`,
与宠物列表/事件页的「W xx%」同一口径,勿各算各的),固定显示两位小数。只有协议原始体重
精确等于 `weight_low`/`weight_high` 时才显示 `0.00%`/`100.00%`,避免舍入产生假性边界；
`V` 是嗓音原值。
数据经 SSE(`wildpets`,成员或状态变化时推全量,实体进出 AOI 是低频事件故不节流)+ 加载时
`GET /api/wildpets` 回显。前端只绘制载荷 `sceneResId` 与当前地图一致的列表,避免位置与图层两条
SSE 在切场景时到达顺序不同而短暂画错底图。

标记的撤下分两种:

- **离开 AOI**(走远了,或被**别人**捉走了——这两者确实无从区分):不立刻抹掉,标 `stale`
  置灰保留 4 小时,免得刚瞥见一只稀有的、一转身标记就没了;这么久是把灰点当「本次上线在
  这一带见过什么」的备忘(野生宠刷新周期远长于几分钟)。同场景传送时当前目标全部转为灰点;
  跨场景则按 `scene_res_id` 缓存在内存中,新场景只显示自己的目标,返回原场景时恢复。离开场景
  的时刻作为仍在 AOI 目标的最后所见时间,所有场景的灰点都在 4 小时后过期;服务重启后清空;
  灰点**照样计入侧栏图层行的数量**(计数与图上标记一一对应,否则侧栏写着 0、图上还挂着几个,
  只会让人以为标记出错),悬浮该行看「视野内 N · 已离开视野 M」的拆分;
- **这只已经没了**(自己捉走或打死):当场撤掉,不留灰点。两条路各有各的结果通知,
  都直接带 actor_id,不必靠位置猜(`internal/scene/catch.go`):

  | 路径 | 消息 | 字段 |
  | --- | --- | --- |
  | 战斗外直接丢球 | `0x0414`/`0x0413` 的 `acts.throw_catch_notify`(173,`SpaceAct_DeleteThrowNotify`) | `npc_id(3)` + `is_catch_success(4)`,及 `npc_catch_infos(11){npc_id(1), is_catch_success(2)}` |
  | 污染个体(要打一场) | `0x132c` `ZoneBattleFinishNotify` | `settle_info(1).monster_info(8){state(2), npc_obj_id(16)}`,`state ∈ {CATCHED(1), DEFEATED(0)}` |

  战斗那条**捉走与打死一视同仁**——对地图标记是一回事,那儿已经没这只了
  (2026-08-02 两份 pcap:污染爬爬 `WIN_CATCH`、污染矿晶虫 `WIN_DEFEAT`)。
  另两个状态 `RUNAWAY(2)`/`ALIVE(3)` 不算消失:打输或它逃了,走回去还能再遇上,
  标记该继续按「离开 AOI」置灰。

  三个坑:①`throw_catch_notify` 对**每次投掷物销毁**都下发(扔道具/魔法时不带 `npc_id`),
  且**捕捉失败也发**(`is_catch_success=false`——16 份 pcap 里失败比成功还多),见到 act 就当
  捉到会把还在的标记误撤;②没用 `0x0203 END_THROW_RSP` 的 `catch_results`:那是较新字段,
  只有最近两份 pcap 填了,且 `CRT_CATCH_SUCCESS` 恰好是枚举 0(成功时反而不上线),
  不如显式布尔好判;③`DEFEATED` 同样是枚举 0,但游戏描述符是 **proto2**(显式赋值即上线,
  实测打死那只的 `field2` 确实带着 0),故只在字段确实下发时才采信——不把「字段缺省」当打死,
  万一哪天真缺省了顶多是标记继续置灰,不会误撤还在的实体。
  战斗结算里同时含我方队伍(`npc_obj_id` 为 0),自然被过滤掉。

## 3.6 精灵蛋与孵化(`0x0312` + `PET_EGG_CONF`)

打开宠物盒的孵蛋器会发 `0x0311 ZONE_GET_ALL_HATCH_STATUS_REQ`(空消息),
`0x0312 RSP` 只有两组并列数组 `egg_gid[]` / `hatched_secs[]`(按下标配对);
**蛋的属性搭 `ret_info.goods_change_info.changes[].bag_item` 的便车下发**,
`bag_item.egg_data`(`PetEggBrief`)才是正主。背包全量(`0x1344`)里同样带 `egg_data`,
所以不开孵蛋器也能拿到全部蛋。

| 关心的东西 | 字段 | 说明 |
| --- | --- | --- |
| 唯一 id | `bag_item.gid` | 是**背包物品**的 gid,`egg_gid` 指的就是它;孵出的宠物是另一个 gid(`ZoneCrackEggRsp.hatched_pet_gid`,`0x030c`),两者只有破壳那一刻的响应能对上 |
| 获得时间 | `bag_item.update_time` | 蛋进包/最后变更的 unix 秒;`egg_data.start_hatch_time` 是放进孵蛋器的时刻 |
| 种类 | `egg_data.conf_id` | → `PET_EGG_CONF.name`(=孵出的宠物名);`0` 表示随机蛋,另看 `random_egg_conf` |
| 身高/体重 | `egg_data.height`/100 米、`weight`/1000 千克 | **下蛋时就定死**,区间取 `PET_EGG_CONF.height_low/high`、`weight_low/high`(蛋自己的区间,与 `PETBASE_CONF` 里成体的区间不是一套数);**百分位孵化后原样保留**,见下 |
| 孵化进度 | `hatched_secs` / 上限 | 上限:`conf_id==0` 用 `egg_data.max_hatched_secs`,否则查 `PET_EGG_CONF.hatch_data`(两者实测一致)。百分比 = `floor(secs/上限*100)`,与客户端 `UMG_PetHatchingItem_C:OnUpdateHatchSecs` 同口径 |
| 来源 | `egg_data.src`(`EggAcquireWayType`) | `EAWT_HOME=6` 牧场、`EAWT_BLESSING=5` 好友赐福、`EAWT_NONE=0` 其他(如商人处买的随机蛋) |
| 赐福来源 | `from_player_name`/`from_pet_name`/`from_player_uin`/`from_pet_gid`/`from_pet_base_id`/`from_pet_conf_id` | 是**赐福/赠送**的来源玩家与其宠物(客户端文案「收到了来自{0}的精灵{1}的赐福」),**不是父母本**;牧场自产的蛋这些字段全空 |

**没有的东西**:`PetEggBrief` 里**没有声音(voice)字段**,也没有性格/个体值 ——
`PET_EGG_CONF.voice_percent` 恒为 `[0,100]`(全范围),嗓音只能等破壳。
`mutation_type`(异色)/`glass_info`(炫彩)/`talent_rank`(天分)/`is_precious` 协议上有位置,
但实测 39 个蛋全为空,大概率也是破壳才填。**父母本信息全程没有**:蛋从牧场产出走的是
背包增量(`GoodsChange`),没有独立的「下蛋」opcode,双亲不随蛋下发。

`hatched_secs` 按**墙钟 × 当前倍率**累积,`last_hatch_update_sec` 是服务器算这条数时的时刻。
2026-08-15 三份 pcap 里相邻两次查询恒为 `Δhatched = 5 × Δ墙钟`(+50/10s、+60/12s),
一次不间断、无道具的孵化满足:

```
hatched_secs = 250 + 倍率 × (last_hatch_update_sec − start_hatch_time)
```

(第一份 pcap 三个蛋联立解出倍率 5.0、截距 250,残差为 0;那个随机蛋隔半小时、跨一次登录仍一字不差。)

**那个 5 不是常态,是活动倍率,别写死。** `ACTIVITY_CONF` 里 `activity_type == 18` 是每周
「孵蛋加速日」(`title_icon_text` = 周末事件),这些 pcap 落在 `id 1800022`
「**500%孵蛋加速日**」窗口内(`appear_time` 2026-08-14 04:00:00 ~ `disappear_time`
2026-08-17 03:59:59,文案「背包孵化精灵速度提升至500%」)。早几期(1800001~)写的是
「速度增加100%」即 2 倍。**没有活动时就是 1 倍**,`PET_EGG_CONF.hatch_data` 就是真实秒数
(8h / 12h / 16h)。倍率在配置里只以文案形式存在(`activity_name`/`activity_txt` 里的百分数),
`ACTIVITY_UP_CONF` 那行是空的,没有数值字段可读 —— 要用就**从相邻两次 `hatched_secs` 采样现算**。

而且这个式子只在「挂机不动、没用道具」时成立,不能拿来倒推 `start_hatch_time`。已知两个加项:

- **孵化宝典**:`BAG_ITEM_CONF` 102101/102102/102103/102104,分别 +5%/20%/50%/100% 孵化进度。
- **跑动加成**(三方攻略,本仓库尚未实测):玩家在游戏内奔跑移动会额外加孵化进度。

2026-08-15 第三份 pcap 里随机蛋 3017 正好卡在这上面:入孵 1387 秒、基线 7185,实发 20060,
多出 12875(平均等效 14.5 倍速)。而第一份 pcap 的三颗蛋跨 1.5 小时**一秒不差**地符合 5 倍
—— 那段时间玩家处于离线/未操作状态。两相对照,**超出的部分只能来自在线时的行为**
(跑动或用道具),不是「上一段孵化的余额」:`PET_GLOBAL_CONFIG.hatch_interrupt_text` 明写
「精灵蛋被取出后,孵化进度将不会保留」。

要量化跑动加成,可以这样抓一段:开孵蛋器采一次 → 关掉**跑动** 60 秒 → 再开采一次 →
**站着不动** 60 秒 → 再开采一次,比两段的 `Δhatched_secs / Δ墙钟`。
进度只在 `0x0312`(开孵蛋器)和 `0x1344`(开背包)里下发,没有被动推送,所以必须手动开界面采样。

结论:外推用「**当前 `hatched_secs` + 实测倍率 × 已过秒数**」,别从入孵时刻起算。

到顶后 `hatched_secs` 基本停在上限(实测有一例 57620/57600,溢出 20 秒,不影响百分比钳到 100%)。

蛋的显示名不在配置里成品供着,要拼:物品 `BAG_ITEM_CONF[bag_item.id]` 的 `known_name`
是模板 `"{0}的蛋"`,`{0}` 填 `PET_EGG_CONF[egg_data.conf_id].name`;随机蛋(`conf_id=0`)
没得填,直接用物品 `name`(如 `310049` = 神奇的蛋)。

### 随机蛋(神奇的蛋)的区间藏在哪

随机蛋 `conf_id = 0`,查不到 `PET_EGG_CONF` 行,所以**客户端自己也没有区间可显示**
(`GetPetEggConf(0)` 为 nil,孵蛋器只把 `height`/`weight` 原样打出来;只有自定义炫彩蛋
`PET_CUSTOM_GLASS` 才显示 `???`)。但区间是存在的 —— **物种在下蛋时就已确定,只是对客户端隐藏**:

- `max_hatched_secs` 逐蛋不同(实测 14 个随机蛋里 28800/43200/57600 都有)。这个值只能来自
  `PET_EGG_CONF[隐藏 conf_id].hatch_data`(普通蛋客户端自己查表,随机蛋只能由服务端下发)。
- `height`/`weight` 也就是按那个隐藏物种的蛋区间滚出来的。

于是可以**反推候选物种**:`hatch_data == max_hatched_secs` 且 `height`/`weight` 落在
该行区间内的所有 `PET_EGG_CONF` 行。916 行的表能收得很窄 —— 实测 14 个随机蛋里最窄的只剩
1 个候选(菇菇丁),中位数十来个。候选集假设随机蛋的池子是全表,若实际池子更小
(商人/活动限定)还能再收窄。

**2026-08-15 第三份 pcap 的实测(唯一一例随机蛋破壳)**:随机蛋 `gid 2985`
(h20 w11443,`max_hatched_secs` 57600)孵出 **权杖-Ⅱ**(`conf_id 3410001`,
`hatch_data` 57600)—— 正是上述筛选给出的 3 个候选(布是石 / 首领布是石 / 权杖-Ⅱ)之一,
时长这一维的约束确认有效。

但**随机蛋的身高体重不预测成体尺寸**,同一例反证:

| | 蛋值 | 蛋区间(权杖-Ⅱ) | 蛋百分位 | 成体值 | 成体区间 | 成体百分位 |
| --- | --- | --- | --- | --- | --- | --- |
| 身高 | 20 | [19,23] | 25.0% | 53 | [45,55] | **80.0%** |
| 体重 | 11443 | [9975,14280] | 34.1% | 33816 | [28500,35700] | **73.8%** |

同一份 pcap 里的普通蛋(小独角兽)仍然分毫不差(97.317% → 97.319%),所以不是模型错了,
而是随机蛋走另一套:成体尺寸八成是破壳时才滚的,蛋上那两个数至多是"某个物种的蛋"的样子。
`h`/`w` 用作候选筛选这一维因此存疑(本例真值没被筛掉,但只有 n=1),
**时长维(`max_hatched_secs` == `hatch_data`)才是有实测支撑的那个**。

### 蛋从哪来:家园小窝下蛋与亲本(2026-08-15 第四份 pcap)

`src == EAWT_HOME` 的蛋出自家园的小窝。蛋在窝上是个**场景 NPC**(`0x014a` 进场景数据里
`other_actors.npc`,`detail_type 13`,`npc_cfg_id` 形如 `930xxx`),收取动作**没有专门的
opcode**,走通用的场景交互:

```
c2s 0x0137 ZoneSceneNpcNextActReq{npc_id(=actor_id), option_id}   option_id = 830000000 + (npc_cfg_id − 930000)
s2c 0x0243 ZoneGoodsRewardNotify{goods_reward.rewards{id=蛋物品, gids=新蛋 gid},
                                 goods_change_info.changes.bag_item.egg_data{…},
                                 reward_reason/flow_reason: 223, reward_source: 13}
```

`223` 即 `ProtoEnum.FlowReason.FLOW_REASON_PET_HOME_LAY`(家园宠物下蛋;描述符里这两个字段
是裸 uint32,不是枚举类型,所以 pcapdump 只会打数字)。

**收之前就能看出是什么蛋**:`npc_cfg_id` 反查 `BAG_ITEM_CONF` 里 `npcid` 等于它的那件蛋物品
(如 930028 → 107028),再走 `item_behavior.ratio` → `PET_EGG_CONF` 得物种。但窝上 NPC 的
`npc_base.height/weight/voice` 全是 0,**尺寸要收下来才知道**。

**双亲能对上**。玩法规则(玩家告知):家园最多 10 个小窝,每窝住自己的一只宠物,
**相邻**两窝若一公一母且蛋组匹配,母本就有概率在一段时间后产出一颗蛋;蛋的**物种必定随母本**,
性格与天赋有概率继承双亲,孵出的体重在**双亲百分位均值**上下浮动,声音基本是双亲均值向下取整。
场景数据正好能把这套关系还原出来:

- **蛋挂在母本的窝上**:蛋 NPC 的 `attach_item_info.attach_item_id` == 母本
  `home_pet_info.furniture_guid`(一件家具 = 一个窝 = 一只宠物)。
- **配对只在进场景快照里下发一次**。2026-08-15 第五份 pcap:进家园时有个空窝,随后一只点点
  住了进去——新住户的 actor 只出现在 AOI 通知(0x0414)与喂食请求(0x8205)里,**没有任何消息
  重发 `lay_egg_couple`**(`ZONE_HOME_INFO_CHANGE_NOTIFY` 只是访客标志)。所以本次停留期间
  再有宠物进/出窝,手上的配对就可能不全,得重进一次家园才刷新(代码里标 `couplesStale`)。
- **相邻由窝的摆放位置决定**(`home_pet_info.pos`,家园局部坐标)。该 pcap 的 10 只宠物
  正好两两配成 5 对(每对间距 160,跨对间距 ≥ 400),每对都是一公一母且蛋组有交集 ——
  但**这只是这份布局摆得干净**:窝可以在家园里自由挪动,几个窝挨太近会**串窝**,
  届时同一颗蛋有多个候选父本。协议里既没有父本字段、也没有"这颗蛋配的是谁"的记录,
  所以**父本只能靠布局推,推不出来的时候就是推不出来**;能确定的只有母本(蛋挂在她的窝上)。
- **物种随母本**有两个跨物种的直接证据:点点♀ + 幽星光♂ 那窝出的是**点点的蛋**、
  大耳帽兜♀ + 治愈兔♂ 那窝出的是**大耳帽兜的蛋**(蛋种类由 `npc_cfg_id` 反查得到,见上)。

体重按双亲均值这条在两颗已收的蛋上都对得上(蛋自己的百分位 vs 双亲成体百分位的均值):

| 蛋 | 母本 w% | 父本 w% | 双亲均值 | 蛋实测 | 偏差 |
| --- | --- | --- | --- | --- | --- |
| 小独角兽的蛋 | 37610♀ 93.043% | 37620♂ 96.177% | 94.610% | **96.332%** | +1.72pp |
| 友爱天天的蛋 | 39302♀ 99.918% | 39048♂ 99.590% | 99.754% | **100%** | +0.25pp |

性格/天赋的概率继承也吻合:小独角兽双亲性格 21/21、天赋 rank 2/2 → 孵出的 39339 是 21 / rank 2;
友爱天天双亲 22/22、4/4 → 39322 是 22 / rank 4;而 39323(点点)性格 19 不来自任何一方,
即「概率没中时另滚」。声音那条本仓库还没抓到反例,可证伪的预测是:大耳帽兜♀(-12) + 治愈兔♂(13)
那窝的蛋应孵出 `voice = floor(0.5) = 0`。

**声音为 0 有两个来源,别混为一谈**:牧场蛋是 `floor(双亲均值)`,双亲一正一负就容易收敛到 0;
而**商店买的、活动送的蛋多数直接固定 0**(玩家告知)。3.6 前面那份统计(孵化 catch_way=3
的宠物 71.9% 声音为 0,野捕只有 0.5%)是这两者叠加的结果,不能单独归因于任一条。
本仓库的实例:远行商人处买的神奇的蛋孵出的权杖-Ⅱ `voice: 0`,同期两只牧场蛋孵出的都是 100。
`PetData` 里没有「这只从什么蛋来」的字段,所以事后无法把两类拆开统计。

`home_pet_info` 另带 `pet_gid`/`name`/`feed_info`(食物 + 起止时间)/`feed_round`/`status`,
喂食轮数与产蛋节奏应该在这里,尚未细究。

### 放入孵蛋器 / 破壳(2026-08-15 第二份 pcap)

- **放入孵蛋器**走通用的用道具:`0x0163 ZoneUseBagItemReq{gid, num:1, item_conf_id}`,
  RSP 回来的 `bag_item.egg_data` 就带上了 `start_hatch_time`;下一次 `0x0312` 里
  该 `egg_gid` 才出现(`hatched_secs: 0`)。孵蛋器 3 格,`egg_gid[]` 就是当前占用的格子。
- **孵满**后 `hatched_secs` 停在上限(`28800/28800`;实测有一例 `57620/57600` 溢出 20 秒)。
- **三个槽位按入孵时刻升序**,与背包次序无关。客户端
  `UMG_PetHatching_C:UpdatePanel` 取 `PlayerDataModel:GetPlayerBackpackEggInfo()`
  (即 `PetBackpackInfo.egg_gid` 那串)后先 `table.sort(…, a.start_hatch_time < b.start_hatch_time)`,
  再依次填 1..3 号槽。实测本机三颗在孵的蛋(3104/3109/3110)背包次序恰是入孵次序的**倒序**,
  页面早先照背包顺序摆,看上去就与游戏内整个反了;后端 `pet.SortHatchingEggs` 照客户端重排。
- **破壳**:`0x030b ZoneCrackEggReq{egg_gid, select_ball_gid}`(要选一个精灵球道具的 gid)
  → `0x030c RSP` 里 `ret_info.goods_reward.rewards{type: GT_PET, first_get, pet_data{…}}`
  是**完整 PetData**,末尾 `hatched_pet_gid` 给出新宠物 gid。新宠物 `catch_way: 3`(孵化)、
  `add_time` = 破壳时刻、`level: 1`、`ball_id` 取自所选球。蛋这件背包物品同时被 `OT_DEL`。

**身高/体重的百分位在破壳时原样保留**(实测两只,误差只在取整上):

| | 蛋(`PET_EGG_CONF` 区间) | 成体(`PETBASE_CONF` 区间) |
| --- | --- | --- |
| 友爱天天 | h 17/[12,17]=100%、w 1675/[1040,1676]=99.843% | h 41/[28,41]=100%、w 4189/[2970,4190]=99.918% |
| 点点 | h 23/[17,23]=100%、w 1547/[1019,1556]=98.324% | h 55/[41,55]=100%、w 3874/[2910,3890]=98.367% |
| 小独角兽 | h 59/[42,59]=100%、w 22033/[14525,22240]=97.317% | h 141/[98,141]=100%、w 55222/[41500,55600]=97.319% |

(**仅限已知物种的普通蛋**;随机蛋不适用,见上一节。)

即服务器存的是一个隐藏百分位 `p`,两端各按自己的区间取整呈现
(用取整区间求交完全自洽)。**所以开蛋前就能算出孵出来会有多大**:
`成体值 = 成体下限 + (蛋值 − 蛋下限)/(蛋上限 − 蛋下限) × (成体上限 − 成体下限)`。

声音蛋上没有字段(`PET_EGG_CONF.voice_percent` 916 行全是 `[0,100]`),
但**家园蛋能从双亲推**:`floor((母本嗓音 + 父本嗓音) / 2)`(实测规律见下),页面就按这个显示;
串窝时父本不唯一,逐个候选算一遍给出区间。非家园蛋没有双亲快照,只能留「—」。
同一份 pcap 的宠物全量(782 只)里按 `catch_way` 分:野捕(1)437 只只有 **0.5%** 的
`voice == 0`,孵化(3)224 只却有 **71.9%** 为 0 —— 孵化出来的嗓音不像野捕那样满范围随机,
多数直接是 0,少数带值的按种类扎堆(幽星光/海盔虫/鸭吉吉/友爱天天…),与「牧场蛋继承亲代嗓音」
的猜想一致,但协议里既无亲代字段也无蛋上嗓音,破壳前无从验证。

### 商店买蛋(2026-08-16 pcap)

远行商人处买蛋**不发奖励通知**(`0x0243` 那三条全是空壳),新蛋只随购买回包下来一次:

```
c2s 0x0261 ZoneShopBuyItemReq{shop_id: 3009, buy_item_info{goods_id: 68003, goods_item_num: 5}}
s2c 0x0262 ZoneShopBuyItemRsp{ret_info.goods_change_info.changes[].bag_item.egg_data{…}}
```

`changes` 里一颗蛋一条(`OT_SET`,买 5 颗就是 5 条),载体与收蛋/入孵/破壳完全一样,
故 `ParseChangedEggs` 直接可用,只是 `0x0262` 此前不在 `handleEgg` 的分发列表里——
不加的话得等玩家再开一次背包(`0x1344` 全量)才补上,页面不实时。
神奇的蛋 `item_id: 310049`、`conf_id: 0` + `random_egg_conf: 1`(随机蛋),
`src: EAWT_NONE(0)`(**不是** `1=远行`,来源那栏因此留空),
`max_hatched_secs` 每颗不同(28800/43200/57600),即前面说的「时长维才是可信的候选筛选维」。

### 3.6 在本项目里落地成了什么

| 面向 | 落点 |
| --- | --- |
| 品类角标 | `gen_icons.py` 的 egg 组另收 `EGG_TYPE_CONF.small_icon`(图集精灵,8 张:异色/炫彩/珍贵/唯一…) |
| 蛋图 | `gen_icons.py` 的 **egg 组**:`BAG_ITEM_CONF` 里 `type==8` 的 `icon`(整张贴图)→ `img/egg/<原名>.webp`,293 个唯一图标转出 276(17 个未上线物种的贴图没随包解出,Go 侧回退 `egg_tongyong`) |
| 索引 | `gen_gamedata.py` 五张表:`egg_conf`(物种蛋区间 + 孵化秒数 + 蛋品类)、`egg_items`(蛋物品 → 显示名/物种/图标/窝上 NPC id/品质/排序号)、`egg_types`(蛋品类 → 名称/排序号/角标)、`size_medals`(按百分位自动授予的四枚奖牌)、`nest_furniture`(小窝家具,按 `interact_type==3` 取,实测仅 1001071) |
| 解析 | `internal/pet/egg.go`(BagItem+PetEggBrief、破壳请求/回包、flow_reason)、`internal/scene/home.go`(home_info 的家具与配对、home_pet 实体、蛋 NPC 的 attach_item) |
| 入库 | `internal/store/egg.go` 的 `eggs` 表 = **背包现状**:蛋一行,`parents` 单列存**收蛋那一刻**的双亲快照(亲本被放生也不受影响);破壳/送人/背包对账不到的直接删行(页面只看背包,不留历史) |
| 管线 | `internal/pipeline/eggs.go`(背包分页对账 + 收蛋/买蛋入库 + 认领双亲 + 破壳删行)、`internal/pipeline/home.go`(小窝图层的实时状态与推送) |
| 页面 | 精灵蛋页(`web/src/pages/eggs/`)与实时地图的小窝图层(`web/src/pages/map/useHomeNests.js`) |

#### 蛋的品类与「品质排序」(复刻客户端)

蛋分品类(`dataconfig.PreciousEggType`):异色/异色炫彩/炫彩/珍贵/唯一/自选炫彩/噩梦…
`EGG_TYPE_CONF` 给出每个品类的名称、角标与 `display_order`。**品类不在协议里**——
`PetEggBrief.precious_egg_type`(21)实测服务器不下发(97 颗蛋全空),客户端自己查
`PET_EGG_CONF[conf_id].precious_egg_type`(见 `PetUtils.GetPetEggConfigTypeByGID`),本项目照做
(字段哪天真填了就优先用它)。异色蛋另有独立的 `PET_EGG_CONF` 行(如 小独角兽 3062001 /
异色 3062007),故区间、品类、图标都是分开的。

游戏内背包的两种排序,`internal/pet.SortEggs` 逐条复刻 `BagModuleData`:

| 排序 | 客户端函数 | 键(依次) |
| --- | --- | --- |
| 品质 | `SortEggQualityDown`(蛋专用,别的物品走 `SortQualityDown`) | 品类 `display_order` 升 → `BAG_ITEM_CONF.item_quality` 降(珍贵蛋按 5 算)→ `sort_id` 升 → `update_time` 降 |
| 获取时间 | `SortTimeDown` | `update_time` 降 |

页面上那个 ↑↓ 就是客户端的 `IsReversalSort`(整条比较取反)。

**连排序算法一起复刻**。上面这些键分不出高低的蛋(同一时刻入包的两颗同种蛋,品类/品质/
物品排序号/获得时间全相等)最终谁在前,取决于算法本身:客户端用的是 Lua 的 `table.sort`
——快排,不稳定,同一批数据换个方向排,相等的两个就可能换位置(玩家实测:品质↑↓与时间↑
是 A、B,只有时间↓是 B、A)。补个 gid 之类的兜底键只会排出游戏里根本不会出现的顺序,
用 Go 的稳定排序又只能得到「永远保持原序」。好在 Lua 5.4 的实现是**确定性**的(只有区间
长于 RANLIMIT=100 或分区严重失衡才引入随机枢轴,几十颗蛋走不到),于是照抄一份到
`internal/pet/luasort.go`(与真实 `lua5.4` 随机对拍,见 `luasort_test.go`)。

要对得上,喂给排序的**列表与次序也得一样**:
- 次序 = 背包原始次序(服务器下发顺序),故 `eggs` 表另存一列 `seq`,由背包全量对账时写入
  (`store.SetEggOrder`),`ListEggs` 按它排;
- 列表 = 背包里能看见的那些,**在孵的蛋不算**(客户端先 `IsRemoveEggItem` 摘掉再排),
  故 `handleEggs` 只对非孵化那部分调 `SortEggs`(`can_see` 那道过滤对蛋恒为真,918 件全是 1)。
**在孵的蛋不出现在背包格子里**:客户端 `IsRemoveEggItem` 把孵蛋器里的蛋从背包列表里摘掉,
本页照此分两栏(左孵蛋器、右背包),因而不需要「背包中/孵化中」这类过滤。
破壳后的蛋也不留:`0x030b` 记下 egg_gid、`0x030c` 一到就删行(库里只有背包现状)。

#### 破壳前就能算出的奖牌

`MEDAL_TASK_CONF` 里 `get_condition==3` 的四枚是按百分位自动授予的:
`condition_data1` 是维度、`condition_data2` 是百分位窗口 ——
大块头 `[98,100]`、小不点 `[0,2]`、婉转声 `[98,100]`、粗嗓门 `[0,2]`。
维度虽写作「身高」,**实际判的是体重**:本机 812 只宠物里戴小不点的体重百分位全在 `[0,2]`
(与窗口严丝合缝)而身高百分位到 5,大块头两者都 ≥98.1 不区分。
蛋的百分位孵化后原样保留,所以**体重那两枚破壳前就能定**;嗓音那两枚在**家园蛋**上也能定
——嗓音由双亲均值推出(见上),换算成百分位 `(v+100)/2` 再比窗口(婉转声即 `v ≥ 96`,
与本机 812 只宠物的实测边界一致)。推不出来(非家园蛋/随机蛋)或串窝区间跨在窗口边上的,
不给这枚奖牌(卡片上只列**确定**拿得到的,纯文字,没有就空着)。

两个实现上的取舍值得记一笔:

- **小窝取自家具列表而不是实体**。空窝没有任何实体,只有 `room_layout` 里那一行家具;
  而「哪个窝还空着」正是要显示的信息。窝↔宠物靠 `furniture_guid` 对上,窝↔蛋靠蛋实体的
  `attach_item_info.attach_item_id` 对上。
- **展示模型在读取时重算**(`pet.RefreshEggView`)。`data` 列存的是**写入当时**的展示模型,
  本工具后加的字段(异色标记、品质排序键…)在旧行里根本没有——只按库里那份显示的话,
  背包里那 8 颗异色蛋会一直标成「普通精灵蛋」,得等玩家再开一次背包才对得上。
  故读取时按原始事实(物品/物种/尺寸/来源/孵化时刻,这些库里都有)重跑一遍 `ToEggView`,
  再挂上双亲、补推测嗓音与奖牌;排序键既然是重算出来的,排序自然也放在其后(见 `handleEggs`)。
- **卡片缺什么都留位置**。同一行六张卡片,少一行信息就参差不齐,故声音(推不出来时)、
  双亲(非家园蛋没有)渲染成占位;奖牌那行没有确定的就空着、高度仍占住;
  普通蛋的品类角标位置什么都不画(那一行的高度由孵出物种头像撑着,不会塌)。
- **异色/炫彩用全站统一的那两个标记**(同宠物列表的 `Marks`),不用游戏自己的蛋品类角标:
  品类 2/3 是异色、3/6/7 带炫彩,一眼要能与列表页对上;其余品类(珍贵/唯一/噩梦…)
  才用 `EGG_TYPE_CONF` 的角标。
- **双亲在收蛋那一刻认领**。`0x0243` 只说「你得到了一颗蛋」,不说来自哪个窝;认领靠玩家点窝上
  那颗蛋时的 `0x0137`(记下蛋实体 id),再经 `attach_item_id → 母本的窝 → lay_egg_couple` 得双亲。
  漏抓那次交互时退一步按蛋物品 id 在当前家园里找唯一匹配的窝,仍不唯一就不记(宁缺毋错)。
- **小窝标记不给开关、不占图例**。它只在家园里有内容(别处本来就是空列表),没什么可关的;
  悬浮说明压成一行 `点点 ♀ Lv.1 · W 90% V -50 急躁`(`W` 体重百分位、`V` 嗓音原值,
  与野生宠物标记同一口径),只说住户——窝上有没有蛋看标记右上角那个蛋图标;点住户头像开宠物详情弹窗。
  **地图内的标记不能用普通 `onClick`**:平移要 `setPointerCapture`,之后 `pointerup` 一律
  重定向到视口,浏览器就不再往标记上派 `click`(chromedriver 实测事件序列
  `pointerdown:IMG` → `pointerup:.map-vp` → `click:.map-vp`)。故由 `usePanZoom` 统一判
  「按下到抬起没拖开(≤6px)」再回调,认**按下那一刻**的元素——缩放按钮那条豁免也是同一原因。
  配对候选不进悬浮说明——它只在进场景那一刻下发一次、期间会过期(见上),
  真要看双亲去精灵蛋页(那儿存的是收蛋当时的快照)。

## 3.7 实体下发范围(AOI:多远的东西才会给)

3.4 的收集判定与 3.5 的野生宠物图层都靠「实体有没有下发」推结论,故边界到底是什么值得单列一节。
2026-08-16 用 24 份 pcap 实测(一次性探针,未入库:c2s `0x0133` 的 `to_pos` + `speed` 外推出事件时刻的
玩家坐标,对 `0x014a` 快照与 `0x0413`/`0x0414` 的 `actor_enter`/`actor_leave` 逐条算距离,再按
实体的 `npc_cfg_id` 分组),结论与解包配置对得上:

**阈值是每个 NPC 配的一个水平圆半径**,配置项为 `NPC_CONF.aoi_distance`(RTTI 定义
`Modules/System/PGC/RTTI/Defines/Generates/NPC_CONF.lua`,描述即「aoi 下发距离」)。该字段
`Scope = Server`(只发服务器),故客户端 Bin 的 `NPC_CONF.json` 里**根本没有这一列**、查不到具体值,
但枚举还在:
`Enum.AoiCullDistance`(`Data/Config/Enum.lua`)的**取值就是米数**——

```
ACD_NONE = 0   ACD_NEAR = 80   ACD_DEFAULT = 90   ACD_LONG = 150   ACD_LONGEST = 200
```

实测距离正好落在这几档上:

| 实体 | 进入(`actor_enter`) | 离开(`actor_leave`) | 推断档位 |
| --- | --- | --- | --- |
| 普通野生宠(`throwing_interact_type=1`) | n=253,p50 52.7m,p95 78.8m,**p99 80.8m**,max 87.8m(仅 4 例 >80m) | p90 86.9m,p95 90.3m,max 105m | `ACD_NEAR` 80 |
| 首领(`type=4`,BulkySizeType=4:祭礼巨像/女王蜂/钻石蜗/风暴战犬…) | 128.2–176.6m | 153.7m | `ACD_LONG` 150 /`ACD_LONGEST` 200 |
| 果树 50090/50091/50092、炼金釜 65324 | 149.4–149.9m 封顶 | 162–169m | `ACD_LONG` 150 |
| 星/光点/石像、采集物 | ≤80m(55508 光点 81.1m、55503 83.0m、55164 星 79.2m、58314 石像 78.5m) | 85–96m | `ACD_NEAR` 80 |

三个要点:

- **是水平 2D 距离,高度不计**。按高度差分桶看进入样本:玩家飞在野生宠上方 `dz` 高达 150–170m 时,
  平面距离照样卡在 80m 上沿。故 3.4 的洞穴层守卫(`starLayerDz`)防的是**分层地图/独立空间**
  (那儿实体真的不来),不是垂直距离——纯高度差本身不影响下发。
- **离开阈值 ≈ 1.1 倍**:普通野生宠要走到 88–90m 才收到 `actor_leave`,150 档的在 165 上下。
  即进/出有迟滞,擦着边界来回走不会疯狂抖动。
- **半径是圆的,不是格子**。欧氏距离最大值(80.8m)≈ 切比雪夫距离 `max(|dx|,|dy|)` 最大值,
  若按方格判定该出现的 √2 倍溢出(80m 半宽 → 113m 对角)完全没有。3.4 里「更远的发了、更近的没发」
  是**跨类比较**的错觉:同一片地方星是 80 档、果树/首领是 150/200 档,再叠上下面这条延迟。

**但「明确」的只是阈值,不是时机。** 玩家从 >80m 跨进 80m 到实体真正下发(n=206):
中位 **6.3s**、p75 11.4s、p90 21s、最大 47s;下发时人通常已经进到 40–65m(中位 53m)。
这正是 3.4 的判定不敢「进圈即判」的原因(`starCommitNear`/`starCommitBack` 两个结账时机),
`starSweepRadius` 收窄到 50m 同样是为了躲这段延迟,与本节的 80m 不矛盾:**80m 是服务器的边界,
50m 是「到这儿实体必已到齐」的工程余量**。

**没见到数量截断**:同时在 AOI 内实测上限是野生宠 30、全部实体 58,没有掉队现象。
`BASIC_QUALITY_CONFIG_CONF` 里的 `aoi.PlayerMaxNpcNum{,PC,Moblile}`(50/125)是**客户端画质 cvar**
(按机型限制同屏 NPC 渲染数),与协议下发无关——抓包看到的是服务器给的全量,比客户端画出来的可能更多。

## 3.8 涂地(实时地图页「已扫过的区域」)

遍历大地图找异色/炫彩时,想知道的是「哪片我已经扫过了」。判据不用距离,用**实际下发的实体**:
每收到一只野生宠(3.5 的 `actor_enter` / 进场景快照),就把**玩家 ↔ 这只宠之间的走廊**涂上色
——那条线上的东西此刻确确实实到手了,稀有个体若在,野生宠物图层当时就标出来了。后端
`internal/server/paint.go`,前端 `web/src/pages/map/usePaint.js`。

**为什么按实体而不是按固定半径**(80m 圆的做法一开始试过,已废弃):

- **不涂没宠的地方**。水面、城镇、山崖这类根本不刷宠的地方,按 80m 半径涂会涂掉一大片,看着
  扫得很干净,其实那儿本来就没什么可找的;按实体涂则天然只覆盖有宠的区域,剩下的空白才是
  真正值得跑的。(这类地方仍有一条**贴身安全带**兜底,见下。)
- **不写死任何距离**。给多远涂多远:普通野生宠实测 80m,边界情况自动跟着走。
- **躲开下发滞后**。3.7 实测跨进边界到实体真正下发滞后中位 6.3s、p90 21s;按半径涂会把
  「进了圈但还没下发」的地方也涂上,按实体涂则只认已经到手的那一份。

**但首领要排除**(`gamedata.DB.IsNpcBoss`,即 `NPC_CONF.throwing_interact_type=4`:祭礼巨像/
女王蜂/钻石蜗/风暴战犬…,names.json 的 `npc_bosses`,116 行):它们的下发距离是 150/200m 那两档
(3.7),拿它当「这条线扫过了」的凭据,会把中间那段其实**没下发过普通野生宠**的地方一并涂上。
两份带首领的 pcap 对照复演:`rocom-20260815-020723` 138 → 86 格、`rocom-20260717-210612`
557 → 455 格,少掉的三成正是伸向首领的长走廊。地图标记那层不受影响,首领照常标。

仍要说在前面的一条:**涂过 ≠ 现在也没有**。野生宠会刷新,涂色说的是「你路过那会儿这条线上没有
稀有个体」,隔几小时再回来仍可能刷出新的。它是**遍历辅助**,不是「这片已经空了」的保证。

走廊半宽 **12m**(`paintCorridor`,即带子宽 24m),两端各带半个圆帽(故玩家脚下与宠物脚下都涂到)。
半宽只是「一条视线代表多宽的一片算看过」,与判定距离无关——野生宠在刷新点附近本就晃动几米,
再窄图上只剩蛛网。涂的时机有两处:**每个移动包**(人一动,同样几只宠的走廊就扫过新的一片)与
**每次实体进/离通知**(新看到的方向要当场涂,玩家可能站着不动等不到下一个移动包)。

### 贴身安全带(`paintSafe` = 15m)

地形/剧情限制的区域(峭壁、城镇、剧情场地)本来就不刷宠,只画走廊会让这些地方**永远空着**,
人来回走多少趟也不见涂,反倒像是没扫过。故玩家走过的路两侧再涂一条半宽 15m 的带子
——不管有没有宠物下发。半径 15m 是从历史流量里统计出来的,不是拍的:

| 统计(24 份 pcap,258 次 AOI 补发,已排除首领与不可捕捉的 NPC) | 值 |
| --- | --- |
| 「下发那一刻玩家离它多远」的下沿 | **17.1m**(p2) |
| p5 / p10 / p50 | 26.4m / 30.8m / 52.1m |
| 唯一比 17.1m 更近的一例 | 3.1m(捕捉那份 pcap 里同一只宠在脚下重新下发) |

即**历史上从没有过「玩家已经进到 15m 以内、某只野生宠才姗姗下发」**,故 15m 内没冒出宠物就可以
判定那儿确实没有。那唯一的 3.1m 属于「刷新点就在脚底下」,任何半径都挡不住,而且真站在它身上时
屏幕里早看见了。(下沿低于 17m 的几例——0.9m 点点、6.1/11.0m 迪莫、10.2m 武斗酷喵——是家园宠与
剧情/活动 NPC,它们同样带身高体重,但不是野外刷出来的、下发规则未知,已按「可丢球捕捉」那道闸
一并排除,涂地不拿它们当凭据。)

安全带沿**这一段真实轨迹**涂(上一包位置 → 补报的 `move_seg_list` 轨迹点 → 本包位置),不是
在每个包位置点一个圆:客户端输入不变时退化成 2.5-3s 一次心跳,一跳能走出三四十米,点圆会断成
一截一截。仍存的理论缺口:高速飞行擦过一片时,整只宠可能压根没等到下发(见 3.7 的滞后统计),
安全带照涂——它是遍历辅助,不是「这儿绝对没有」的证明。

**怎么记:格子位图,不是轨迹/连线**。世界按 **8m**(`paintCell`)见方切格,涂到的格子记一位;
按**账号 + 场景(`scene_res`)+ 分层**各存一张(`paint` 表的 BLOB),分层地图与地表各涂各的
——两者是不同空间,AOI 不相通(同 3.4 的洞穴层守卫)。选位图而非存轨迹/连线,是因为:

| | 位图 | 轨迹/连线 |
| --- | --- | --- |
| 反复走同一片、同一只宠反复入视野 | 幂等,那几位而已 | 无限增长 |
| 整张体积 | 恒定:大陆 510×510 格 = 32.5KB(base64 43KB) | 随时长增长 |
| 增量 | 只发新格下标(多数时候一格都没有) | 每包/每只一条 |
| 前端渲染 | 直接当图用(canvas + CSS 缩放) | 要自己求并集 |

**开销**:每只宠一次「线段包围盒」扫描(80m 的走廊约 12×3 格,先看位、涂过就跳,没涂过的才算一次
点到线段的距离),视野里 26 只宠时一次涂地也就几百次格子判定,而**多数调用一个新格子都没有,
当场返回**——不广播、不落盘(实测一份 pcap 里 1160 次调用只落了 94 格)。落盘攒批:攒够 512 格
或距上次 10s 才写一次(离线回放几秒跑完一整份 pcap,按时间攒不到,故同时按格数计;回放结束
另补一次 `FlushPaint`)。位图由 `server` 独占持有,抓包管线写、HTTP 读同一份内存,页面拿到的
一定是最新的,SQLite 只是重启后的持久化。
管线侧另维护 `connState.wildSeen`(当前 AOI 里**全部**野生宠的位置,不只 3.5 那几类稀有的)
与 `connState.pos`(玩家坐标——实体通知里没有玩家位置),换场景/传送即清零。

**前端**:图层栏「涂地」一组,开关 + 重置(↺,确认后清当前场景当前层,别处扫过的保留)。
开关只管显示——**后端一直在记**,故打开时看到的是这一路以来的全部痕迹,而不是从此刻重新涂。
渲染是一张 `w*h` 的 canvas(大陆 510²)按格填色,再由 CSS 拉到底图尺寸:平移缩放全靠
`.map-world` 的 transform 与 canvas 的 CSS 尺寸,**不重绘、不逐帧算**;只有新格子到达时
`fillRect` 那几格,整张换了(换场景/重置)才 `putImageData` 铺一次。用 DOM 画 26 万个色块不可能,
canvas 则连缩放都是浏览器的事。
**配色是「淡填充 + 细亮边」**(`FILL_RGBA`/`EDGE`),不是单纯一层半透明色:底图是彩虹似的
彩绘图(沙、草、水、红顶、紫蘑菇林、金枫林),实机截图逐个试过——绿在草地上看不见、紫在
蘑菇林里看不见、压暗在深色山地上看不见,**任何单一色的填充都必然在某个地区糊掉**。而轮廓
与底色无关:内部只淡淡一层紫(32%),沿边界描一条近白的细线,涂到哪儿都看得出边界。

**描边走矢量、填充走位图**,各取所长:

- 描边是这层唯一要看清的东西,而位图描边一放大就成毛边(画布按地图分辨率开,放大到几倍时那条
  1 像素的线被拉宽再插值)。故描边是一条 **SVG 路径**(`viewBox` 即格子数,与 canvas 同尺寸叠放):
  浏览器在**当前缩放下**重新栅格化,配合 `vector-effect: non-scaling-stroke`,线宽恒为屏幕上的
  1.2px——缩放到上限(实测底图拉到 8100px,约 2px/米)描边仍是一条干净的细线。
  路径沿「已涂/未涂」的**格子交界**走,连续的一段合成一条 `H`/`V` 直线,故很短:实测 455 格的
  涂色只有 1.4KB 路径。
- 填充是一层淡色,糊不糊看不出来,继续用格子位图:一次 `putImageData` 铺完 26 万格,
  放大时被插值柔化,正好把方格化开。

增量不做局部补画(新格子会改变周围的边界走向,局部补画得连带擦掉旧描边),而是**整张重画并到
下一帧**(`requestAnimationFrame` 合并一批增量):几趟按格扫描,几毫秒,且只在真有新格子时跑;
平移缩放两层都一次不重绘(CSS 尺寸 + `.map-world` 的 transform)。

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
- **种类名**：合并 MONSTER_CONF+PET_CONF,自有解包提取 + `bin2json.py` 解码,全量
  543 只 0 空种类。**修正了 pak-public-kit 的 PET_CONF 名整体错位**:其 `ELocalizedString`
  本地化对彩蛋宠(PET_CONF)整体偏移一位(3011001 误为"恶魔叮",应为"恶魔狼"),
  累计 4787 个彩蛋宠名错误;经自有解包 + 两个独立 world-data 源三方比对确认后改用自有解码;
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
- **异色/炫彩**：`mutation_type` 为位标志,bit0=异色(`MDT_SHINING`)、bit3=炫彩(`MDT_GLASS`);
  全部位与炫彩外观的解读见 3.5(宠物列表这边不解析 `glass_value` 的具体外观,只记是否炫彩)。
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
