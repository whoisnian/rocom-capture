# 宠物消息解析

解密后的应用层消息如何变成库里的宠物、事件与位置(`internal/pet` 解析,`internal/pipeline` 落库)。
字节层(GCP 分帧/密钥/重组)见 rocom-parse 的 docs/protocol.md;名称表与图片索引的生成见其 docs/data.md;
游戏消息各字段的含义见 [protocol.md](protocol.md)。

## 1. 宠物列表

```
s2c 0x1346 ZONE_GET_PET_INFO_BY_PAGE_RSP 明文 body
  → ParsePetListRsp: protowire 取 field 4(pet_info)
  → proto.Unmarshal 成 PetDataInfoList → []*PetData
  → ToPet(pd, gamedata): pb.PetData + 名称库 → 业务模型 Pet(已中文化)
```

`ToPet` 完成单位换算(身高/体重)、枚举翻译(系别/性格/天分/奖牌/标记/特长)、六维提取。
实际形态以 `PetData.base_conf_id`(当前 petbase)为准取名称/头像/图鉴/形态,缺失才回退 `conf_id`
(进化线一阶 base),否则已进化宠物会显示成基础形态。

**技能(`PetData.skill`)不解析**:本项目做「按宠物自身属性找宠物」的统计,技能是可换的配置、
不是个体属性,筛选/排序/展示都用不上。

### 种类名

合并 `MONSTER_CONF` + `PET_CONF`(彩蛋宠)两张表,由 rocom-parse 自有解码得到,全量无空种类。

### 六维 / 天分 / 性格

每项六维(`Stat`)含三部分:

- **最终面板值**(`value`):取自 `attribute_new_info`(已含等级/努力/奖牌加成)。
- **天分**(`talentLv`):取自 `attribute_info.*.talent_add_value`,即该维度的个体值 1–10
  (无天分则为 0)。宠物在 1–3 个维度上有天分。
- **性格影响**(`nature`):性格使一维 +10%、一维 −10%。增减维度由权威性格表(30 种,按性格名匹配,
  见 rocom-parse `gen_gamedata.py` 的 `NATURE_TABLE`)生成 `nature_effect`;若用道具改过性格,
  则以 `changed_nature_pos/neg_attr_type` 为准。(`NATURE_CONF` 推导对个别性格如"平和"的 id 错位,故用权威表。)

`talent_rank`(天分评级)由天分项数与是否和性格增益维度重合决定:一般般=1项、还不错=2项、
了不起=3项(1项与性格增益重合)、相当好=3项(不重合)。例:火神固执(+物攻−魔攻),天分在
生命/物攻/速度三项,物攻与性格增益重合 → 了不起。

### 特长

取 `PET_TALENT_CONF` 中 `filter_enum_value=PTFN_TALENT_*` 的 11 种固定特长(无/奇袭/亲密/灵巧/
疾行/同乘/无畏/爱分享/家里蹲/热心教/慈悲为怀);id=502 表内 name 为"勇敢",游戏显示为"无畏"。

### 异色 / 炫彩

`mutation_type` 为位标志,bit0=异色(`MDT_SHINING`)、bit3=炫彩(`MDT_GLASS`);炫彩外观由
`glass_info{glass_type, glass_value}` 原样入库(`glassType`/`glassValue`),色卡本身不入库,读取时查
gamedata 现算(`pet.FillDerived`),配置里查不到的款退回通用炫彩图标。全部位与外观解读见 [map.md](map.md) 5。

### 伙伴标记

`PetData.partner_mark`(`PetPartnerMarkType`:0=无,其余 9 种名称都叫「标记」、只有图标之分)。
玩家在宠物盒里加/换/去标记时,`ZONE_UPDATE_PET_COLLECT_TAG_RSP(0x0403)` **不带 PetData**(全长 54 字节),
改动只体现为 `ret_info.goods_change_info.changes[]` 里一条
`{type=GT_PET_MARK(39), op=OT_SET, num=改后的标记值, gid=pet_gid}`——加标记、换样式、移除(`num=0`)
都走这同一条。故 `ParseCollectTagRsp` 取出 (gid, num),`store.SetPetPartnerMark` 就地改那一只的
`partner_mark` 列与 data JSON(不动其余字段、不写位置),再广播 `pet` 让前端刷新。

## 2. 获得与减少

五个获得 opcode 统一走 `FindNewPet`(递归找 body 里 `gid>0`、`conf_id>1000` 且 `add_time` 是合理
秒级时间戳的 `PetData`,防误报),按 `catch_way` 区分子类型(1/4/5=捕捉、3=孵蛋),
`isNew` 去重(同宠物可能多 opcode 下发):

| 途径 | opcode | 说明 |
| --- | --- | --- |
| 孵蛋 | `ZONE_CRACK_EGG_RSP(780)` | 奖励 `ret_info.goods_reward.rewards[].pet` |
| 战斗外捕捉 | `0x1983` + `0x0243` | 投球结果 `0x1983` 只回球道具变更,新宠物经 `0x0243` 下发 |
| 普通战斗内捕捉 | `ZONE_GOODS_REWARD_NOTIFY(0x0243)` | |
| 花种(稀兽)战斗内捕捉 | `ZONE_PLAYER_SYNC_NOTIFY(0x0160)` | `catch_way=4`;通用同步通道,额外加 `add_time` 时近性守卫(相对本包时间 120s 内)防 PvP 对手/旧快照污染 |
| 传说精灵战后捕捉 | `ZONE_BATTLE_FINISH_NOTIFY(0x132c)` | `catch_way=5`;同为通用通道,同样加时近性守卫;普通/花种捕捉的战斗也带它(与 GOODS_REWARD 重复,靠 `isNew` 去重);该消息还要撤野生宠标记,分发时两处都要过 |

> **新获得宠物的 `PetData` 不带 `name`**(客户端按配置显示种类名),各通道一样,分页全量(`0x1346`)
> 里也是空的,只有早先创建的老宠物还保留 `name`;故 `FindNewPet` 不拿 `name` 作判据,改认每只宠物
> 都带的 `add_time`(创建时间)。

减少只记库、不记事件(捕获事件页只统计获得):
- **放生** `ZONE_PET_FREE_RSP(0x01c5)`:`pet_gid`(field2)→ 从库中移除;
- **赠送** `ZONE_TOGETHER_CATCH_PET_FOR_GIFTING_RSP(0x1808)`:见下。
游戏内没有「删除宠物」入口(协议里的 `DELETE_REQ(397)` 玩家不可触发),减少途径至此完整。

### 共同捕捉转赠(好友互赠)

世界主人与到访好友「一起捕捉」再转赠(4 小时窗口),`PetData.together_catch_info`(#90)记录双方:
`related_uin`=接收方、`catched_uin`=捕捉方。捕捉与赠送是相互独立的事件:

- **送出方**:捕捉照常记「捕捉」(新宠物随 `0x0243` 下发);之后开盒子赠送,经 `0x1808` 确认 →
  `ParseTogetherCatchGiftRsp` 取顶层 field3 的 `pet_gid` 移除。该 opcode 有两种回包:内嵌完整 PetData
  的预览与紧凑 ack(仅 `ret_info`+gid),**只认后者**,避免预览误删。
- **接收方**:受赠宠物经 `0x0243` 下发(走 `FindNewPet`),`catch_way` 仍为 1,靠
  `related_uin`==本账号 且 `catched_uin`≠本账号 记 obtain「赠送获得」。
- 判据对称、不依赖 opcode:送出方 `catched_uin`==本账号仍记「捕捉」(`catchWayName` 据 `uidFromAcc(acc)` 判定)。

### 别处放生的对账清除

放生/赠送回包只在抓包在线时才能捕获;玩家在别的环境放生后重登,那批宠物只是从登录快照里消失。
登录快照只做增改、从不删,残留的旧宠会以「⏳位置待同步」滞留。故对**连续一轮分页快照**
(`req_page` 依次 1..`total_page`;`page_num` 字段实为每页容量 50、非页序)累积全部 gid,在末页据完整快照
`PruneMissingPets` 清除库中缺席者;仅在 1..total 连续到达时触发(乱序/单独翻某页不触发),且只删对账开始前
就存在(`updated_at` 早于本轮起始)的宠物,放过对账期间刚入库的新宠。

## 3. 位置:盒子与队伍

`PetData` 无位置字段,位置由仓库布局 `PetBackpackInfo` 表达。

- **盒子**:`ZONE_LOGIN_RSP(0x0102)` 登录数据(或盒子操作回包 6272-6292)携带 `boxes[]`,每个 `PetBox` 有
  `box_id`(**盒号即展示位置,1 起**)、`mark_type`(WarehouseMarkType:1首领/2污染/4奇异/8炫彩/16闪光)、
  `box_name`、`lock`、`pet_gid[]`(**有序数组,每盒 30 格,空格=0**)。**位置 =(box_id, 下标)**。
  `ParseBackpack` 取非零 gid 数最多的候选,展开为 gid→位置存 `pet_box`(占用),同时把全量盒子元数据
  (含空盒)存 `pet_boxes`;读取时 JOIN 注入 `Pet.Box`(盒名/标记以 `pet_boxes` 为权威)。
- **盒子元数据**独立于是否有宠物,故空盒也可见、盒数/盒名/换位都能表达。三条来源:
  - 全量:登录/整理(`PetBackpackInfo`)与整理排列(改名/换位)的 `ZONE_PET_BOX_SETTING_UP_RSP(0x1891)`
    整体 `ReplacePetBoxMetas`+`ReplacePetBoxes`。后者是裸的 `repeated PetBox`(顶层 field2),
    `ParseBackpack` 解不出时按 `ParseBoxSettingUp` 再试。换位即 `box_id` 重排,整体替换让盒内宠物随盒换位。
  - 增量(单盒):解锁 `ZONE_PET_BOX_UNLOCK_RSP(0x1883)`(新盒挂 field2)、设标记/改名
    `ZONE_PET_BOX_SET_MARK_TYPE_RSP(0x1893)`(`{ret=1,box_id=2,mark=3,name=4,lock=5}`),`UpsertPetBoxMeta` 只动单盒。
- **队伍**:在队宠物不在盒子里,位置由 `PlayerPetInfo.team_infos`(同在 0x0102)里
  `team_type==PTT_BIG_WORLD(1)` 的 `PetTeamInfo` 表达——`teams[]`(最多 3 队,**队号取数组下标**,
  `PetTeam.team_idx` 恒 0),每队 `pet_infos[]`(6 位)的 `pet_gid`。`ParseTeams` 取宠物数最多的大世界候选
  → gid→(队,位)存 `pet_team`,JOIN 注入 `Pet.Team`(与 `Box` 互斥)。
- **移动增量**:
  - 盒位:`ZONE_PET_BOX_CHANGE_PET_RSP(0x1888)` 携带 `GoodsChangeItem.box_pet_change`
    (`PetBoxPetChange`:`pet_gid`/`is_in_team`/`id`=盒/`pos`=格,**pos 1 起**)。`ParseBoxMoves` 抽出
    非在队、gid 非 0 的落位项(`slot=pos-1`),`ApplyBoxMoves` 增量 upsert `pet_box` 并清其队位。
    **仅在 0x1888 解析**(其他 opcode 的子消息易误判为 PetBoxPetChange)。
  - 队位:队伍变更/盒子操作回包(`CarriesTeam`:登录/6272-6292/524-527)一并刷新完整队伍快照,
    复用 `ParseTeams` 整体 `ReplacePetTeams`。

## 4. 奖牌墙

每只宠物拥有的全部奖牌在登录 `0x0102` 的 `PlayerSvrDataInfo.pet_medal_info` →
`PlayerPetMedalInfo.medal_infos[]`(`PetMedalInfo`:#1 medal_conf_id / #2 medal_type / #3 owner 组[]),
组内 #2 记录里宠物 gid = `#8(obtain_pet_gid) ?? #6 ?? #2`。该消息线上 wire 格式与 all.pb 的
`PetMedalOwnerInfo` 定义不一致,故 `pet.ParsePetMedals` 纯按 wire 经验解码、不走 pb。
解出 gid↔medal 存 `pet_medal`,读取时注入 `Pet.MedalIDs`(覆盖 `ToPet` 里仅佩戴的那枚);
前端 `/api/medals` 全量奖牌 + `medalIds` 过滤出该宠物拥有的渲染奖牌墙。奖牌数据仅完整登录携带
(普通/快速登录可能不含)。

## 5. 多账号身份

`ZONE_LOGIN_RSP(0x0102)` 取玩家 `user_id` 作账号键(`"UID:"+id`)——wire 三层下钻
`body → #2(LoginData) → #1(base) → {#1=user_id(varint), #3=nickname(bytes)}`(`pet.ParseLoginAccount`)。
按 user_id 而非客户端 IP 归属(多台设备常经 NAT 共用同一 IP);各账号数据在同库内按 `account` 列隔离,
见 [architecture.md](architecture.md) 5。

## 6. 战斗信息(捕获事件页)

事件页右上角的「战斗信息」开关开启后,列表顶部预留一行,展示当前战斗的对手宠物;数据只在
前端 localStorage,后端只广播 SSE(`type=battle`),不落库。只展示与捕捉后一致的字段:

| 阶段 | 消息 | 内容 |
| --- | --- | --- |
| enter | `0x1316` | 普通对战直接取敌方队伍第一只宠物的 `battle_common_pet_info`,性格不能提前确定、显示未知;处于污染状态(`mutation_type` 的 MDT_CHAOS 位)时血脉也是临时的(捕捉后变回种族血脉),不展示;若敌方是龙棋士(conf 1646/base 9006),`init_info.battle_cfg_id` → `BATTLE_CONF.name`/`npc_battle_list[].pos1_1st[]` 得**种类**,等级/性别/身高/体重/声音/性格取龙棋士的 `battle_common_pet_info`(与最终宠物一致) |
| reveal | `0x131a` | 龙棋士护盾打破后,`data_update.other.pets` 里 401 号位的 `battle_common_pet_info` 从龙棋士(1646/9006)变成真宠物,带 `conf/base`、`mutation_type`(异色=1/污染=32)、性格等;推送完整 Pet,前端补上血脉徽标与异色/炫彩标记。普通污染对战在污染位清零时也推一次(阶段变已打破),但血脉仍不展示——结算里才知道种族血脉 |
| end | `0x132c` | 只推 `stage=end`,前端置灰保留,等待下一场;不主动清空 |

页面在事件序号的位置显示等级,并展示种类/性别/体重/声音/性格、名称行的血脉徽标与异色/炫彩
标记;普通对战 enter 即可见(污染战斗的血脉不显示),龙棋士要等 reveal。不展示身高,副行也不
显示血脉文字。

实测(2026-11-08 的污染小电企鹅):进战斗 `blood_id=23`(污染)/`nature=30`/`height=71`,破污染后
`mutation_type=0`/`height=51`,捕捉后 `blood_id=9`(电)/`nature=21`/`height=51`。故污染战斗里
只提前展示种类/等级/性别/体重/声音——性格与血脉都要等捕捉才算数。

注意:棱镜球等特殊球会在**捕捉时**附加炫彩(`mutation_type` bit3/`glass_info`),不能拿结算里的
`caught_monster` 反推野生状态;异色看 `0x131a` 的 `mutation_type=1`(或 `0x132c` 的
`dice_monster_info.is_shiny`)。
