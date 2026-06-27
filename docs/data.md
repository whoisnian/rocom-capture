# 数据来源与解析

宠物字段的 protobuf 定义与中文名称均来自游戏解包数据 **Roco-Kingdom-World-Data**
(见 [AGENTS.md](../AGENTS.md) reference，下称 *world-data*)。本项目把其中需要的部分
预处理成 Go 代码与精简 JSON，编译期 `embed` 进二进制，运行时不依赖原始解包目录。

## 1. 数据来源

world-data 主包 `pakchunk4-WindowsNoEditor/` 下：

| 路径 | 内容 | 本项目用途 |
| --- | --- | --- |
| `PB/proto_out/*.proto` | 210 个游戏原版 protobuf 定义 | 生成宠物消息 Go 结构体 |
| `Bin/BinDataCompressed/*.json` | 配置表(`{"RocoDataRows":{id:{...}}}`) | 提取 id→中文名 |
| `Bin/BinLocalize/zh_CN/*.json` | 中文本地化文本 | 补充部分名称 |

关键表：

- `MONSTER_CONF.json` + `PET_CONF.json` — 宠物种类名(`conf_id → name`)。
  常规宠物在 MONSTER_CONF，彩蛋/特殊宠物在 PET_CONF，两表 id 不重叠，合并取用。
- `AUDIO_NATURE_CONF.json` — 性格名(`nature_id → name`)
- `MEDAL_CONF.json` — 奖牌名与描述
- `PET_TALENT_CONF.json` — 特长名(`speciality_id → name`)
- `PET_FILTER_CONF.json` — 一站式筛选维度配置：系别/天分/标记的
  `filter_enum_value → filter_desc`(中文)
- `c2s_cmd.proto` 的 `enum ZoneSvrCmd` — opcode → 名称(供调试页)
- `xls_enum.proto` 的 `enum SkillDamType / PetTalentRate / ...` — 枚举值名 → 整数

## 2. proto → Go(`scripts/gen_proto.sh`)

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

系别/天分/标记的整数值，通过解析 `xls_enum.proto` 枚举(名→整数)再 join
`PET_FILTER_CONF` 的(枚举名→中文)得到。种类合并 MONSTER_CONF+PET_CONF，
特长直接取 PET_TALENT_CONF，性别为硬编码。

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
- **特长**：改用 PET_TALENT_CONF，覆盖率 100%；
- **放生事件**：接入 `ZONE_PET_FREE_RSP(453)`，解析 `pet_gid` 列表 → 移除并推 lose 事件。

待校准(多数需含相应事件/宠物的新样本)：
- **异色/炫彩**：原 `mutation_type!=0` 误判严重，已暂置 false；正确字段(`glass_info`/`hide_shine` 等)需含异色宠物的样本确认；
- **获得事件细分**：`catch_way`(捕捉/孵蛋/赠送)取值需事件样本校准；
- **删除/赠送减少事件**：`DELETE_REQ(397)`/赠送相关 opcode 待接入(需确认 c2s body 偏移);
- **咕噜球/蛋组/技能名**本地化尚未梳理；**盒子位置**字段待定位；
- **性格** `nature_id` 用 `AUDIO_NATURE_CONF`，个别可能与游戏显示略有偏差。
