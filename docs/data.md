# 数据来源与解析

本项目用到两个解包数据源(见 [AGENTS.md](../AGENTS.md) reference)，各负责一半，编译期
`embed` 进二进制，运行时不依赖原始解包目录：

- **pak-public-kit**(下称 *kit*)：当前 NRC 版本解包，`output/` 已提交、`git pull` 即跟新。
  提供**中文名称表与 opcode 表**(高频变动，正好随上游更新)。
- **world-data**：提供 `internal/pb` 所需的 **protobuf 字段号**(`.proto`)。kit 不导出
  protobuf 描述符(其网络协议字段号只在不被提交的二进制 `FileDescriptorSet` 里)，故此项
  仍取自 world-data。好在字段号是**追加式**的(新版本只加不改号)，旧 `.proto` 解码始终有效，
  几乎无需跟版本更新——这也是不强行把 `internal/pb` 也切到 kit 的原因。

## 1. 名称表数据来源(kit)

kit `output/` 下：

| 路径 | 内容 | 本项目用途 |
| --- | --- | --- |
| `data/BinData/*.json` | 配置表(`{"RocoDataRows":{id:{...}}}`) | 提取 id→中文名 |
| `scripts/lua/Data/PB/ProtoCMD.lua` | `ZoneSvrCmd` 完整命令枚举(1211 条) | opcode → 名称(供调试页) |
| `scripts/lua/Data/PB/ProtoEnum.lua` | 反编译 Lua 的全部 protobuf 枚举 | 枚举值名 → 整数 |

关键表：

- `MONSTER_CONF.json` + `PET_CONF.json` — 宠物种类名(`conf_id → name`)。
  常规宠物在 MONSTER_CONF，彩蛋/特殊宠物在 PET_CONF，两表 id 不重叠，合并取用。
- `AUDIO_NATURE_CONF.json` — 性格名(`nature_id → name`)
- `MEDAL_CONF.json` — 奖牌名与描述
- `PET_TALENT_CONF.json` — 特长名(`speciality_id → name`)
- `PET_FILTER_CONF.json` — 一站式筛选维度配置：系别/天分/标记的
  `filter_enum_value → filter_desc`(中文)
- `ProtoCMD.lua` 的 `ZoneSvrCmd` — opcode → 名称(完整表，原生含 6531=
  `ZONE_SCENE_THROW_CATCH_FINISH_RSP`，无需再手工补丁)
- `ProtoEnum.lua` 的 `SkillDamType / PetTalentRate / PetPartnerMarkType` — 枚举值名 → 整数

## 2. proto → Go(`scripts/gen_proto.sh`，数据源:world-data)

游戏 `.proto` 有两个坑，脚本 `scripts/fix_proto.py` 自动修复：

1. **缺 `syntax` 声明** → 补 `syntax = "proto3";`；
2. **大量 enum 非 0 起始**(proto3 要求首值为 0)→ 为每个 enum 插入 0 占位值，
   并对含重复/0 值的 enum 加 `option allow_alias`。

只编译 `com_pet.proto` 的依赖闭包(6 个文件)，用 `--go_opt=M...` 映射到单一 Go 包
`internal/pb`。产物为 `internal/pb/*.pb.go`(已提交)。

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
partner_mark  speciality  medal  opcodes
```

系别/天分/标记的整数值，通过解析 `ProtoEnum.lua` 枚举(名→整数)再 join
`PET_FILTER_CONF` 的(枚举名→中文)得到。种类合并 MONSTER_CONF+PET_CONF，
特长直接取 PET_TALENT_CONF，opcode 取自 `ProtoCMD.lua` 的 `ZoneSvrCmd` 全集，性别为硬编码。

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
- **种类名**：合并 MONSTER_CONF+PET_CONF 后覆盖率 ~94%(剩余少量冷门 conf_id)；
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
  `isNew` 去重(同宠物可能多 opcode 下发);
- **异色/炫彩**：`mutation_type` 为位标志,bit0=异色、bit3=炫彩(9 样本实测验证);
  炫彩的颜色/粒子细节(`glass_value`)不解析,仅记录是否炫彩。

待校准(多数需含相应事件/宠物的新样本)：
- **删除/赠送减少事件**：`DELETE_REQ(397)`/赠送相关 opcode 待接入;
- **咕噜球/蛋组/技能名**本地化尚未梳理；**盒子位置**字段待定位；
- **性格** `nature_id` 用 `AUDIO_NATURE_CONF`，个别可能与游戏显示略有偏差。
