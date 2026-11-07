# 精灵蛋与孵化

精灵蛋页(`web/src/pages/eggs/`)与家园小窝图层背后的数据:蛋的协议字段、随机蛋区间、
下蛋亲本、孵蛋器与破壳、品类与排序、破壳前能算出的奖牌。名称/图标索引由
rocom-parse 的 `gen_gamedata.py` 与 `gen_icons.py` 产出(`make gamedata`),生成管线见其 docs/data.md;
解析与入库在 `internal/pet/egg.go`、`internal/store/egg.go`。

## 1. 协议与字段(`0x0312` + `PET_EGG_CONF`)

打开宠物盒的孵蛋器会发 `0x0311 ZONE_GET_ALL_HATCH_STATUS_REQ`(空消息),
`0x0312 RSP` 只有两组并列数组 `egg_gid[]` / `hatched_secs[]`(按下标配对);
**蛋的属性搭 `ret_info.goods_change_info.changes[].bag_item` 的便车下发**,
`bag_item.egg_data`(`PetEggBrief`)才是正主。背包全量(`0x1344`)里同样带 `egg_data`,
所以不开孵蛋器也能拿到全部蛋。

| 关心的东西 | 字段 | 说明 |
| --- | --- | --- |
| 唯一 id | `bag_item.gid` | 是**背包物品**的 gid,`egg_gid` 指的就是它;孵出的宠物是另一个 gid(`ZoneCrackEggRsp.hatched_pet_gid`,`0x030c`),两者只有破壳那一刻的响应能对上 |
| 获得时间 | `bag_item.update_time` | 蛋进包/最后变更的 unix 秒;`egg_data.start_hatch_time` 是放进孵蛋器的时刻 |
| 种类 | `egg_data.conf_id` | → 孵出的宠物名(`PET_EGG_CONF.name` 不发布,由 `pet_id` 重建,见下);`0` 表示随机蛋,另看 `random_egg_conf` |
| 身高/体重 | `egg_data.height`/100 米、`weight`/1000 千克 | **下蛋时就定死**,区间取 `PET_EGG_CONF.height_low/high`、`weight_low/high`(蛋自己的区间,与 `PETBASE_CONF` 里成体的区间不是一套数);**百分位孵化后原样保留**,见下 |
| 孵化进度 | `hatched_secs` / 上限 | 上限:`conf_id==0` 用 `egg_data.max_hatched_secs`,否则查 `PET_EGG_CONF.hatch_data`(两者实测一致)。百分比 = `floor(secs/上限*100)`,与客户端 `UMG_PetHatchingItem_C:OnUpdateHatchSecs` 同口径 |
| 来源 | `egg_data.src`(`EggAcquireWayType`) | `EAWT_HOME=6` 牧场、`EAWT_BLESSING=5` 好友赐福、`EAWT_NONE=0` 其他(如商人处买的随机蛋) |
| 赐福来源 | `from_player_name`/`from_pet_name`/`from_player_uin`/`from_pet_gid`/`from_pet_base_id`/`from_pet_conf_id` | 是**赐福/赠送**的来源玩家与其宠物(客户端文案「收到了来自{0}的精灵{1}的赐福」),**不是父母本**;牧场自产的蛋这些字段全空 |

**没有的东西**:`PetEggBrief` 里**没有声音(voice)字段**,也没有性格/个体值(两者都只能从双亲推,见「学院小窝」一节)——
`PET_EGG_CONF.voice_percent` 恒为 `[0,100]`(全范围),嗓音只能等破壳
(该字段连同 `name`/`form`/`pet_bond_name` 都不发布,见下)。
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

### 谁在孵蛋器里(别只看 `start_hatch_time`)

**蛋从孵蛋器里取出后 `start_hatch_time` 不清零**,服务器只把进度清掉
(`hatched_secs` 与 `last_hatch_update_sec` 一起归 0,与 `PET_GLOBAL_CONFIG.hatch_interrupt_text`
「精灵蛋被取出后,孵化进度将不会保留」对得上)。只看这个字段,曾经放进去过的蛋会一直算在
孵蛋器里 —— 2026-08-26 那份 pcap 就是:背包 63 颗蛋里 5 颗带 `start_hatch_time`,照此算会得出
5 颗都在孵,而当次游戏内只有 3 颗真正在孵(另外 2 颗早换出来了)。那 5 颗分两拨,一眼可辨:

| gid | start_hatch_time | hatched_secs | last_hatch_update_sec | 实际 |
| --- | --- | --- | --- | --- |
| 3259 / 3262 / 3264 | 739077 / 739170 / 739176 | 16456 / 16363 / 16357 | 都是 755508 | 在孵 |
| 3250 / 3252 | 739086 / 739082 | 0 | 0 | 早先放进去过,又换出来了 |

(时刻均省去前缀 1787;三颗在孵的 `last_hatch_update_sec` 相同 —— 进度是**查询时才算**的。)

权威口径是 **`PetBackpackInfo.egg_gid`**:客户端孵蛋器面板就是拿这串填的
(`PlayerDataModel:GetPlayerBackpackEggInfo`),它有两个下发点,本项目两个都收
(`pet.BackpackHatchSlots` / `pet.HatchSlots` → `store.SetHatchingEggs` 整体订正):

- `0x0102 ZoneLoginRsp` → `player_info.pet_info.backpack_info.egg_gid`:**登录就有**,
  上面那份 pcap 里正是 `3259/3262/3264` 三个,不必等玩家打开孵蛋器;
- `0x0312` 的 `egg_gid[]`:开孵蛋器时的全量快照(**空孵蛋器该字段整个不下发**,故
  「解出 0 个」也是有效快照,照样清标记)。

拿不到这两份时(抓包从半途开始)退回蛋自己的字段判断:`start_hatch_time > 0` 且
**进度非零**(`hatched_secs` 或 `last_hatch_update_sec` 有一个不为 0),即 `pet.Egg.Hatching`。
唯一含糊的是「刚放进去、进度还是 0」的那一刻 —— 客户端放完蛋会紧跟一次 `0x0311/0x0312`
刷新面板,权威快照随即到货,故只差这一瞬。

蛋的显示名不在配置里成品供着,要拼:物品 `BAG_ITEM_CONF[bag_item.id]` 的 `known_name`
是模板 `"{0}的蛋"`,`{0}` 填**种类名**;随机蛋(`conf_id=0`)没得填,直接用物品 `name`
(如 `310049` = 神奇的蛋)。

### 物种名怎么来

`PET_EGG_CONF` 的 `name`/`form`/`voice_percent`/`pet_bond_name` 四个字段是策划专用、不随包发布
(schema `.non` 里没有),`egg_conf.n` 只能用仍发布的字段重建:`pet_id` → `MONSTER_CONF`/`PET_CONF`
的种类名(即 `EggConf.Name` → API 的 `species`,页面上的「孵出 X」)。「异色」由
`precious_egg_type`/`egg_types` 角标单独表达,不进名字。

**孵出的血脉无从得知**:`PET_INFO_CONF` 不发布 `blood_id`,背包里蛋的 `PetEggBrief` 也不带血脉
(只有孵化用的 `PetEggCore` 有),客户端的蛋界面同样不显示血脉。

`egg_items` 的模板同样填种类名,与客户端一致——`HandbookModuleData` 用 `GetPetConf(petId).name`
填 `known_name`。

### 随机蛋(神奇的蛋)的区间藏在哪

随机蛋 `conf_id = 0`,查不到 `PET_EGG_CONF` 行,所以**客户端自己也没有区间可显示**
(`GetPetEggConf(0)` 为 nil,孵蛋器只把 `height`/`weight` 原样打出来;只有自定义炫彩蛋
`PET_CUSTOM_GLASS` 才显示 `???`)。但区间是存在的 —— **物种在下蛋时就已确定,只是对客户端隐藏**:

- `max_hatched_secs` 逐蛋不同(实测 14 个随机蛋里 28800/43200/57600 都有)。这个值只能来自
  `PET_EGG_CONF[隐藏 conf_id].hatch_data`(普通蛋客户端自己查表,随机蛋只能由服务端下发)。
- `height`/`weight` 也就是按那个隐藏物种的蛋区间滚出来的。

于是可以**反推候选物种**:`hatch_data == max_hatched_secs` 且 `height`/`weight` 落在
该行区间内的所有 `PET_EGG_CONF` 行。1119 行的表能收得很窄 —— 实测 14 个随机蛋里最窄的只剩
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

### 学院小窝:性格必定继承(2026-09-15 pcap)

精灵学分院的特权家具「**学院小窝**」(`FURNITURE_ITEM_CONF` 1001072,游戏里是个绿色的窝,
物品描述「据说对产出的精灵蛋有好的效果」)与普通精灵小窝(1001071)同样 `interact_type==3`、
同样能住一只宠物、同样按摆放位置配对;区别只有一条(玩家告知):**与它配对产的蛋必定继承
窝里那只的性格**,不再是普通窝的「有概率继承双亲之一、没中就另滚」。

哪件家具是它不靠名字,取 `ACADEMY_PRIVILEGE_CONF` 里 `academy_id == AID_PET(1)` 且
`academy_privilege_type == ACADEMY_PRIV_ADD_HOME_PETBED(1)` 那行的 `privilege_para1`
(客户端 `AcademicPrivilegeModuleData:IsPetAcademyHomePetBed` 同一口径;每个家园最多摆一件,
`Error_Code_3564`「家园室内摆放了学院小窝,需要先收回,才能转到其他学院」)。
`gen_gamedata.py` 据此在 `nest_furniture` 里给它标 `academy: true`。

协议上它与普通窝**毫无区别**:进场景快照里就是 `room_layout` 里 `config_id` 不同的一件家具,
住户实体、`lay_egg_couple` 配对、蛋 NPC 的挂接一律照旧。这份 pcap 里的家园 11 个窝(10 精灵小窝
+ 1 学院小窝),学院小窝里住着一只 **幽星光♂(gid 40932,胆小)**,配对表里它同时是两只幽星光♀
(40927 胆小、36429 急躁)的父本 —— 一只公的配两只母的,协议上就是两条 `female_couple` 指向
同一个 `male_obj_id`,不算串窝(串窝是**一条**里 `male_obj_id` 重复多次)。
这两窝产的蛋按规则都该是胆小;本份 pcap 里收的两颗蛋(矮脚爬爬、矿晶虫)都不来自这两窝,
真实样本待补。

页面上的落法:亲本快照多一个 `academy` 标记(收蛋那一刻按母本/父本各自所在的窝判),
蛋卡片加一行「性格」,按能确定到什么程度分三种写法:

| 情形 | 显示 | 例 |
| --- | --- | --- |
| 双亲之一住学院小窝 | 那只的性格,**不带**「其他」 | `胆小` |
| 双亲已知、性格相同 | `AA/其他` | `固执/其他`(矿晶虫♀ 固执 + 嘟嘟煲♂ 固执) |
| 双亲已知、性格不同 | `AA/BB/其他` | `固执/胆小/其他`(矮脚爬爬♀ 固执 + ♂ 胆小) |
| 串窝 | 母本 + 各候选父本的性格去重后 + 其他 | |
| 非家园蛋 / 没记下双亲 | `—` | |

(`pet.parentNatures`;母本在前;学院小窝那只没记下性格时——收蛋时它还没入库——退回概率口径。)
地图上学院小窝的描边换成绿色,精灵蛋页上住学院小窝的亲本头像也加同色绿圈。

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
  该 `egg_gid` 才出现(`hatched_secs: 0`)。孵蛋器 9 格(3x3 九宫格),`egg_gid[]` 就是当前占用的格子。
- **取出**是 `0x02ff ZoneStopHatchReq{egg_gid}` → `0x0300 RSP`(回包只有 `ret_info`,
  更新后的 `bag_item` 搭 `goods_change_info` 的便车)。取出**不清** `start_hatch_time`,
  只清进度,故「在孵与否」得另有口径,见 1 的「谁在孵蛋器里」。
- **孵满**后 `hatched_secs` 停在上限(`28800/28800`;实测有一例 `57620/57600` 溢出 20 秒)。
- **槽位按入孵时刻升序**,与背包次序无关。客户端
  `UMG_PetHatching_C:UpdatePanel` 取 `PlayerDataModel:GetPlayerBackpackEggInfo()`
  (即 `PetBackpackInfo.egg_gid` 那串)后先 `table.sort(…, a.start_hatch_time < b.start_hatch_time)`,
  再依次填 1..9 号槽。实测本机三颗在孵的蛋(3104/3109/3110)背包次序恰是入孵次序的**倒序**,
  页面早先照背包顺序摆,看上去就与游戏内整个反了;后端 `pet.SortHatchingEggs` 照客户端重排。
- **破壳**:`0x030b ZoneCrackEggReq{egg_gid, select_ball_gid}`(要选一个精灵球道具的 gid)
  → `0x030c RSP` 里 `ret_info.goods_reward.rewards{type: GT_PET, first_get, pet_data{…}}`
  是**完整 PetData**,末尾 `hatched_pet_gid` 给出新宠物 gid。新宠物 `catch_way: 3`(孵化)、
  `add_time` = 破壳时刻、`level: 1`、`ball_id` 取自所选球。蛋这件背包物品同时离包
  (早先此处记作 `OT_DEL`,是笔误:`OpType` 只有 ADD/SUB/SET,离包一律是 `OT_SET` + 数量归零,见下)。

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

### 赠送与领取(2026-09-10 pcap)

在大世界当面把一颗蛋送给别人,走通用的**玩家互动**(`InteractInviteType.IIT_GIFTING_EGG = 4`),
没有蛋专用的 opcode,两端各一半:

```
送出方 c2s 0x1597 ZoneSceneRelationInteractInviteReq{
          interact_type: IIT_GIFTING_EGG, broadcast: true,
          param{action_id: 8, picked_egg_gid: 3498, picked_bagitem_conf_id: 107226}}
       s2c 0x1598 RSP(只有 ret_info/ban_info)   ← 摆出「等人来领」的姿势
领取方 c2s 0x159b ZoneSceneRelationInteractAcceptReq{target_uin: 送出方 uin, 同一份 interact_type/param}
       s2c 0x159c RSP(原样回显)
       s2c 0x0243 ZoneGoodsRewardNotify         ← 蛋进包
两边   c2s 0x15ed ZoneSceneRelationInteractEndReq{interact_type} → 0x15ee(动作收尾)
```

`param.picked_egg_gid` 是**送出方背包里**那颗蛋的 gid,领取方原样带回去指认要领哪颗
(本次:先从陌生人处领了 `2988`,转手送出时报的是它在自己包里的新号 `3498` ——
背包物品 gid 每个玩家各自一套,同一颗蛋换手就换号,别拿它跨账号对齐)。

两边的 `0x0243` 都是 `reward_source: 13`、`flow_reason: 133`(`FLOW_REASON_GIFT_GIVING`),
区别只在那件 `bag_item` 的数量:

| | `changes[].op` | `changes[].num` | `bag_item.num` | 含义 |
| --- | --- | --- | --- | --- |
| 领到 | `OT_SET` | 1 | 1 | 新 gid 进包;`goods_reward.rewards` 另有一条,`gids` 给出新 gid |
| 送出 | `OT_SET` | 0 | 0 | 那颗蛋**离包**(`goods_reward` 是空壳) |

**离包没有专门的 op**:`dataconfig.OpType` 只有 `OT_ADD`/`OT_SUB`/`OT_SET`,东西没了就是
把数量 `OT_SET` 成 0,而 `bag_item` 照旧带着完整的 `egg_data` 一起发下来 —— 只认 `bag_item`
就会把它当成一次普通更新原样写回库里,送出去的蛋于是一直挂在精灵蛋页上,直到玩家再开一次
背包(`0x1344` 全量对账)才消失(这份 pcap 的两次 `0x1344` 都在赠送之前,故整份回放下来那颗
蛋始终在)。`pet.ParseChangedEggs` 因此把「`op == OT_SET` 且两处 `num` 都为 0」的那条判成离包,
gid 单独返回,交给 `pipeline.removeEggs` 当场删行。三个条件一起要,是防着哪条载体
(入孵/取出/进度更新)不发 `num` 时把在包的蛋误删 —— 漏判最多晚到下次开背包才对账,误判则是当场丢一颗。

领到的蛋 `src` 是 `EAWT_FRIEND_EX(3)`(页面显示「好友交换」),**与对方是不是好友无关**,
本次就是商店街上的路人。`from_player_name`/`from_pet_name` 那组仍旧全空 ——
它们只在赐福时才填(见 1)。

## 2. 在本项目里落地成了什么

| 面向 | 落点 |
| --- | --- |
| 品类角标 | `gen_icons.py` 的 egg 组另收 `EGG_TYPE_CONF.small_icon`(图集精灵,8 张:异色/炫彩/珍贵/唯一…) |
| 蛋图 | `gen_icons.py` 的 **egg 组**:`BAG_ITEM_CONF` 里 `type==8` 的 `icon`(整张贴图)→ `img/egg/<原名>.webp`,326 个唯一图标转出 307(19 个未上线物种的贴图没随包解出,Go 侧回退 `egg_tongyong`) |
| 索引 | `gen_gamedata.py` 五张表:`egg_conf`(物种蛋区间 + 孵化秒数 + 蛋品类)、`egg_items`(蛋物品 → 显示名/物种/图标/窝上 NPC id/品质/排序号)、`egg_types`(蛋品类 → 名称/排序号/角标)、`size_medals`(按百分位自动授予的四枚奖牌)、`nest_furniture`(小窝家具,按 `interact_type==3` 取,当前两件:精灵小窝 1001071、学院小窝 1001072,后者按 `ACADEMY_PRIVILEGE_CONF` 标 `academy`) |
| 解析 | `internal/pet/egg.go`(BagItem+PetEggBrief、孵蛋器占用列表、破壳请求/回包、flow_reason、离包判定)、`internal/scene/home.go`(home_info 的家具与配对、home_pet 实体、蛋 NPC 的 attach_item) |
| 入库 | `internal/store/egg.go` 的 `eggs` 表 = **背包现状**:蛋一行,`parents` 单列存**收蛋那一刻**的双亲快照(亲本被放生也不受影响);破壳/送人/背包对账不到的直接删行(页面只看背包,不留历史) |
| 管线 | `internal/pipeline/eggs.go`(背包分页对账 + 收蛋/买蛋入库 + 孵蛋器占用订正 + 认领双亲 + 破壳/送人删行)、`internal/pipeline/home.go`(小窝图层的实时状态与推送) |
| 页面 | 精灵蛋页(`web/src/pages/eggs/`)与实时地图的小窝图层(`web/src/pages/map/useHomeNests.js`) |

### 蛋的品类与「品质排序」(复刻客户端)

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
  故 `handleEggs` 只对非孵化那部分调 `SortEggs`(`can_see` 那道过滤对蛋恒为真,1051 件全是 1)。
**在孵的蛋不出现在背包格子里**:客户端 `IsRemoveEggItem` 把孵蛋器里的蛋从背包列表里摘掉,
本页照此分两栏(左孵蛋器九宫格、右背包),因而不需要「背包中/孵化中」这类过滤。
分栏依据是 `eggs.hatching` 那一列(权威列表订正过它,见 1 的「谁在孵蛋器里」),
`ListEggs` 单取该列覆盖 `data` 里写入当时的判断,`RefreshEggView` 也不再照 `start_hatch_time` 重推。
破壳后的蛋也不留:`0x030b` 记下 egg_gid、`0x030c` 一到就删行(库里只有背包现状)。

### 破壳前就能算出的奖牌

`MEDAL_TASK_CONF` 里的四枚是按百分位自动授予的:
大块头 `[98,100]`、小不点 `[0,2]`、婉转声 `[98,100]`、粗嗓门 `[0,2]`。
(`MEDAL_TASK_CONF` 只发布 id/desc/count,判定字段 `get_condition`/`condition_data1`/`condition_data2`
不随包,故 `gen_gamedata.py` 按 task id 固定维度 + 从 `desc` 文本读窗口。)
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
