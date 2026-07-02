# 数据来源与解析

本项目的解包数据**全部随仓库提交**(均为 FModel 从 Windows 客户端提取),不依赖任何外部仓库;
编译期 `embed` 进二进制，运行时也不依赖原始解包目录。两份 vendored 源:

- **游戏二进制配置 `nrc/bin/`**:提供**中文名称表**。游戏自有的 `.bytes`(数据)+ `.non`
  (schema)+ `BinLocalize/dev_CN`(本地化),用 vendored 的 `scripts/decode_bin.py` 解码。
- **游戏描述符 `nrc/all.pb`**:游戏自带的 protobuf 描述符(`FileDescriptorSet`,即运行时
  `pb.loadufsfile` 加载的同一份),提供 `internal/pb` 的**字段号/类型**与 **opcode/枚举**。
  含字段号,可直接喂给 protoc 生成 Go,无需 .proto 文本。

字段号/枚举是**追加式**的(新版本只加不改号),故几乎无需跟版本更新;名称表随游戏内容变动。
要更新到新版本游戏:用 FModel 重新提取覆盖 `nrc/bin/` 与 `nrc/all.pb`,再跑两个生成脚本。
(历史上名称/opcode 曾取自 pak-public-kit、字段号曾取自 world-data;现都被自有提取替代,
且修正了 pak-public-kit 的 PET_CONF 名整体错位 bug,见第 5 节。)

## 1. 名称表数据来源(`nrc/bin/`)

`nrc/bin/` 下(只收录需要的 8 张表):

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
- `PET_FILTER_CONF` — 系别/天分/标记的 `filter_enum_value → filter_desc`(中文)
- `PETBASE_CONF` + `MODEL_CONF` — 宠物图片引用(`JL_res` 全身图、`model_conf→icon` 头像;见 3 节末)
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

图片本体(webp)**embed 进二进制**:在 FModel 里把 `Common/Icon` 的 `HeadIcon`/`BigHeadIcon256`/
`Pet256` 子目录以 **PNG** 导出(异色图 `*_1.png`/`JL_*_yise.png` 在同目录,一并导出即可),
`uv run python scripts/gen_images.py <PNG源>` 转成 webp 落到 `internal/gamedata/data/img/`
(`//go:embed all:data/img`),`internal/server` 经 `/img/` 提供。
35MB 的 `Pet1024` 全身大图暂不 embed(体积考量),需要时把 `Pet1024` 加进 `gen_images.py` 的 `DIRS`。

**可复现 / 防 git 噪音**:同一 libwebp 版本下 PNG→webp 转码是确定性的(webp 无时间戳,
实测同源字节一致)。为此 `pyproject.toml` 把 pillow **钉死精确版本**且 `requires-python>=3.10`
(避免 3.9/3.10 解析到不同 pillow → 不同 libwebp → 全量图片 diff)。`gen_images.py` 还**默认跳过
已存在的 webp**:常规重跑零改动,游戏更新只为新增宠编码,libwebp 万一漂移也不动老文件;
换了 quality 等需整体重编时用 `--force`。

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
- **天分等级**(`talentLv`)：取自 `attribute_info.*.talent_add_value`，即该维度的个体值 1–10
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
- **天分/性格**：`talent_add_value` 修正为天分等级(1–10)而非性格修正；性格 ±10% 维度
  改由 `NATURE_CONF` 推导(火神固执=+物攻−魔攻),天分评级逻辑实测吻合；
- **特长**：取 PET_TALENT_CONF 中 `filter_enum_value=PTFN_TALENT_*` 的 11 种固定特长
  (无/奇袭/亲密/灵巧/疾行/同乘/无畏/爱分享/家里蹲/热心教/慈悲为怀),id=502 按游戏
  显示为"无畏"(表内 name 为"勇敢"),覆盖率 100%；
- **放生事件**：接入 `ZONE_PET_FREE_RSP(453)`，解析 `pet_gid`(field2)→ 移除并推 lose 事件;
- **孵蛋事件**：`ZONE_CRACK_EGG_RSP(780)` 用 `FindNewPet` 递归提取奖励
  (`ret_info.goods_reward.rewards[].pet`)中的新宠物 → 入库 + obtain(孵蛋)事件;
- **战斗外捕捉**：`0x1983`(赛季球/高级球，大 body 含新宠物)同样用 FindNewPet
  → 入库 + obtain(捕捉);与放生形成完整链(实测 #2 捕5放5、#3 捕3放3 全为菊花梨);
- **(普通)战斗内捕捉**：经 `ZONE_GOODS_REWARD_NOTIFY(0x0243)` 下发新宠物;
- **花种(稀兽)战斗内捕捉**：不走 `GOODS_REWARD`,新宠物经通用的 `ZONE_PLAYER_SYNC_NOTIFY(0x0160)`
  下发(实测 20497,`catch_way=4`)。该 opcode 通用(玩家数据同步),除 `FindNewPet` 严格判据外
  额外加 `add_time` 时近性守卫(相对本包时间,默认 120s 内)以防 PvP 对手/旧快照污染;
  实测 6 个样本里 0x0160 仅花种捕捉那一条携带有效新宠物,全量同步 543 只 0 误报;
- 上述四个获得 opcode(孵蛋/战斗外/普通战斗内/花种战斗内)统一处理:`FindNewPet` 加严格判据
  (conf_id>1000 且名称含中文)防误报,按 `catch_way` 区分子类型(1/4=捕捉、3=孵蛋),
  `isNew` 去重(同宠物可能多 opcode 下发);受赠宠物 `catch_way` 仍为 1 但应记「赠送获得」,
  见下「共同捕捉转赠」;
- **共同捕捉转赠(好友互赠)**:世界主人与到访好友「一起捕捉」一只宠物再转赠(4 小时转赠窗口),
  宠物 `PetData.together_catch_info`(#90)记录双方——`related_uin`=接收方、`catched_uin`=捕捉方
  (另有 `transfer_deadline` 等)。**捕捉与赠送是相互独立的事件**,两端分别处理:
  - **送出方**:先由捕捉回包 `0x1983` 照常记「捕捉」入库;之后开盒子手动赠送,经
    `ZONE_TOGETHER_CATCH_PET_FOR_GIFTING_RSP(0x1808)` 确认 → `ParseTogetherCatchGiftRsp`
    取 `pet_gid`(顶层 field3)移除并记 lose「赠送」。该 opcode 有两种回包且都在顶层带 gid:
    内嵌完整 PetData 的宠物详情(赠送前预览/同步)与紧凑 ack(仅 `ret_info`+gid);**只认后者**
    (内嵌 PetData 的返回 0),避免预览误删 + 两种回包重复记;
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
  `box_id`(盒号)、`mark_type`(WarehouseMarkType:1首领/2污染/4奇异/8炫彩/16闪光)、`box_name`
  (玩家命名)、`pet_gid[]`(**有序数组,每盒 30 格,空格=0**)。**位置 =(box_id, pet_gid[] 下标)**。
  `ParseBackpack` 取非零 gid 数最多的候选(排除误解析),展开为 gid→位置存入 `pet_box` 表,
  读取宠物时 JOIN 注入 `Pet.Box`;实测 0x0102 解出 ~525 只(27 盒),`/api/pets/21`→污染1 第11格。
- **队伍位置**：在队宠物**不在盒子里**,位置由 `PlayerPetInfo.team_infos`(同在 0x0102 登录数据)
  里 `team_type==PTT_BIG_WORLD(1)` 的 `PetTeamInfo` 表达——`teams[]`(最多 3 队,**队号取数组下标**,
  实测 `PetTeam.team_idx` 恒 0),每队 `pet_infos[]`(6 位)的 `pet_gid`。`ParseTeams`(取宠物数最多的
  大世界候选)→ gid→(队,位)存 `pet_team` 表,JOIN 注入 `Pet.Team`(与 `Box` 互斥);实测 3 队 18 只全命中。
  为此 `gen_proto.py` 把 `com_pet_team.proto` 加为第二根(闭包 +1 文件)。
- **位置移动增量**(运行期实时刷新):
  - **盒位**:`ZONE_PET_BOX_CHANGE_PET_RSP(0x1888)` 携带 `GoodsChangeItem.box_pet_change`
    (`PetBoxPetChange`:`pet_gid`/`is_in_team`/`id`=盒/`pos`=格,**pos 1 起**)。`ParseBoxMoves` 抽出
    非在队、gid 非 0 的落位项(`slot=pos-1`),`ApplyBoxMoves` 增量 upsert `pet_box`(盒名/标记沿用该盒)
    并清其队位。**仅在 0x1888 解析**(其他 opcode 的子消息易误判为 PetBoxPetChange)。
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
- **多账号身份**:`ZONE_LOGIN_RSP(0x0102)` 取玩家 `user_id` 作账号键(`"role:"+id`)——wire 三层
  下钻 `body → #2(LoginData) → #1(base) → {#1=user_id(varint), #3=nickname(bytes)}`
  (`pet.ParseLoginAccount`,实测两用户 839694713/873234858)。按 user_id 而非客户端 IP 归属
  (多台设备常经 NAT 共用同一 IP,无法区分);各账号数据在同库内按 `account` 列隔离,
  详见 [服务架构](architecture.md) 第 5 节「多账号隔离」。
- **宠物减少途径已覆盖**:游戏内无「删除宠物」操作入口,玩家能主动减少宠物的途径只有放生
  (`ZONE_PET_FREE_RSP(0x01c5)`)与赠送(共同捕捉转赠 `0x1808`),二者均已接入(见上)。
  协议里虽存在 `DELETE_REQ(397)`,但无对应 UI 入口、玩家不可触发,故无需接入。

待校准(多数需含相应事件/宠物的新样本)：
- **咕噜球/蛋组/技能名**本地化尚未梳理;
- **性格** `nature_id` 用 `AUDIO_NATURE_CONF`，个别可能与游戏显示略有偏差。
