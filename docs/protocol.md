# 协议说明：tsf4g / GCP

游戏客户端通过 TCP 连接远端服务器 **8195** 端口，使用腾讯 tsf4g 框架的 GCP 模式通信。
本文记录经真实流量逐字节验证的线上格式，是 `internal/gcp` 与 `internal/capture` 的实现依据。

> 概念性说明参见 tsf4g 文档(见 [reference.md](reference.md))。本文只记录**实测**结论。

## 1. 分层结构

```
TCP 流(需重组)
  └─ GCP 包(可跨多个 TCP 段，也可能一个段含多个包)
       ├─ HEAD.base  定长 21 字节，明文
       ├─ HEAD.extend 变长，明文(本项目不解析)
       └─ BODY        变长，DATA 包为 AES 密文
```

一个 GCP 包的总长度 = `hdr_len + body_len`，二者都在 HEAD.base 里。
因此接收方必须先 TCP 重组成连续字节流，再按 `magic` 同步、按长度切包。

## 2. HEAD.base 字节布局(定长 21 字节，多字节大端)

| 偏移 | 字段 | 类型 | 说明 |
| --- | --- | --- | --- |
| 0–1 | magic | u16 BE | 固定 `0x3366`，每个包头都有，用于流内同步 |
| 2–3 | head_version | u16 BE | 实测 `0x000b` |
| 4–5 | body_version | u16 BE | 实测 `0x000b` |
| 6–7 | command | u16 BE | 包类型，见下表 |
| 8 | flag | u8 | 方向/加密标志：上行 `0x00`，下行 `0x01` |
| 9–12 | sequence | u32 BE | 序列号，上行/下行各自独立递增 |
| 13–16 | hdr_len | u32 BE | HEAD 总长度(含 extend) |
| 17–20 | body_len | u32 BE | BODY 长度 |

`HEAD.extend = [21, hdr_len)`，`BODY = [hdr_len, hdr_len + body_len)`。

### command 类型(部分)

| 值 | 名称 | 方向 | 说明 |
| --- | --- | --- | --- |
| `0x1001` | SYN | 上行 | 握手 |
| `0x1002` | ACK | 下行 | **明文下发 16 字节会话密钥** |
| `0x2001/0x2002` | AUTH_REQ/RSP | 双向 | 鉴权 |
| `0x6002` | BINGO | 下行 | 连接就绪 |
| `0x4013` | DATA | 双向 | 应用数据，BODY 为密文 |
| `0x9001` | HEARTBEAT | 双向 | 心跳 |
| `0x5002` | SSTOP | 下行 | 服务端断开 |

## 3. 密钥与解密

本游戏采用**服务器明文下发密钥**(非 DH 交换)：

- `0x1002 ACK` 包的 `HEAD.extend[2:18]` 即 16 字节 AES-128 会话密钥(每次连接不同)。
- `0x4013 DATA` 的 BODY 用 **AES-CBC** 解密，两种模式：
  - `embedded_iv`：`BODY[:16]` 为 IV，`BODY[16:]` 为密文；
  - `fixed_iv`：整个 BODY 为密文，IV 固定(全零)。

> 必须先抓到该连接的 ACK 才能解密其后的 DATA。若从连接中途开始抓包则无密钥。

## 4. 应用层 internal header(解密后明文)

DATA 解密后是应用层消息，前缀一个 internal header，**上下行格式不同**：

```
c2s(上行)：  [0:6]?  [6:8] opcode(BE u16)  [8:] ...        body 从约 8 偏移起
s2c(下行)：  [0:2]?  [2:4] opcode(BE u16)  [4:6]=0x55aa  [6:10] seq  [10:] protobuf body
```

- **opcode** 是应用层命令号，对应 `ZoneSvrCmd` 枚举(如 `257=ZONE_LOGIN_REQ`、
  `4934=ZONE_GET_PET_INFO_BY_PAGE_RSP`)。名称表见 [data.md](data.md)。
- s2c 的 protobuf body 从偏移 10 开始(剥离 `0x55aa` 与 seq)。

实测命中率：c2s opcode@6 ≈ 99%，s2c opcode@2 = 100%。

**c2s protobuf body 与 trailer**(实测 `ZONE_SCENE_MOVE_REQ` 0x0133,`internal/scene` 依此解析)：
opcode 之后还有 **6 字节子头**(前 2 字节随包变化、余 4 字节为 0),故 protobuf 从偏移 `8+6=14`
起(`gcp.AppBody` 只剥到 8,子头留在 AppBody 头 6 字节)。protobuf **之后、`tsf4g` 尾之前**还有
一段**变长 trailer**(路由/校验),长度不定。因此解析 c2s body 不能要求「消费到 tsf4g」,应
**贪婪解析已知字段、遇到不属于该消息的字段即停**(trailer 起始处 wire type/字段号必然不符)。
注:c2s 字段**不保证按字段号升序**(实测 move 包顺序为 1,4,7,2,15,3,6,8,5,17)。

## 5. 宠物列表消息

- 客户端打开宠物仓库时发 `ZONE_GET_PET_INFO_BY_PAGE_REQ(0x1345)`；
- 服务器分页回 `ZONE_GET_PET_INFO_BY_PAGE_RSP(0x1346)`，每页约 40KB(常跨多 TCP 段)；
- RSP body 是 `ZoneGetPetInfoByPageRsp`：`total_page=2`、`req_page=3`、
  `pet_info=4`(`PetDataInfoList`，含 `repeated PetData`)、`page_num=5`。

本项目只手动取 field 4 再 `proto.Unmarshal` 成 `PetDataInfoList`，无需编译庞大的 zonesvr 消息。
解析细节见 [data.md](data.md)。

## 6. 实时位置与场景消息(`internal/scene`,实时地图页)

只跟踪**登录账号自己**的位置(不解析其他玩家/AOI)。字段语义经当前版客户端 Scene luac 坐实、
真实 pcap 验证(见 [data.md](data.md) 3.1/3.2)。

| opcode | 方向 | 消息 | 用途 |
| --- | --- | --- | --- |
| `0x0133` | c2s | `ZONE_SCENE_MOVE_REQ` | 自己移动:`to_pos`(2)、`speed`(4)、`to_rot`(3)、`move_mode`(6)、`stop_move`(8)、`move_seg_list`(12)、`scene_cfg_id`(17) |
| `0x0152` | s2c | `ZONE_ENTER_SCENE_RSP` | 进入场景:`scene_cfg_id`(2)、`scene_res_cfg_id`(3) |
| `0x015c` | s2c | `ZONE_SCENE_TELEPORT_NOTIFY` | 传送:`to_scene_cfg_id`(11)、`to_scene_res_cfg_id`(12)、**落点 `to_pt`(14,`Point{pos,dir}`)**、`home_room_level`(31) |
| `0x0414` | s2c | `ZONE_SCENE_PLAY_ACTS_NOTIFY` | 动作集合 `acts`(1);其中 `enterted_catcher`(61)/`left_catcher`(62)= 区域进/出:`{actor_id, area_id, area_func_conf_id}` |
| `0x1838` | c2s | `ZONE_SCENE_CLIENT_CAVE_STATE_REQ` | 洞穴层:`cave_name`(1,string)+ `pos`(2);**只在传送进流送洞穴时才发,不可靠,未用** |
| `0x1505` | s2c | `ZONE_SCENE_CLIENT_CAVE_STATE_NOTIFY` | 同上(服务器侧下发) |

- **坐标**:`Position{x,y,z}` = UE 世界坐标,1 单位 = 1 厘米,取整。玩家 `to_pos.z` 是**脚底**高度
  (角色中心 +85);`to_rot`/`Point.dir` **不是坐标是旋转**(`FRotator×10`=0.1 度,x=Roll/y=Pitch/z=Yaw)。
- **移动包是事件驱动的,不是定频**(pcap 实测):**操作有变化**(改方向/变速)时约 **0.1s** 一包
  (0.08–0.16s,≈8 次/秒);**输入不变**时退化成 **2.5–3s** 一次心跳(实测密集落在 2.4–3.06s);
  停下补一个 `stop_move=true`(常连发 2–3 条同坐标)。故**收到才画会一顿一顿跳**(心跳期定住数秒)。
  地面与飞行同理——飞行时快速打方向同样是 0.1s 一包(实测绕圈 226 个包,间隔中位 0.108s)。
- **`speed`(field 4)是速度向量**(厘米/秒,跑动约 |v|≈416),客户端据此给其他玩家做平滑。本项目同法:
  后端把它按同一投影换算成「归一化底图坐标/秒」随位置一起推给前端,前端在两包之间逐帧外推
  `pos + speed×Δt`(航位推算)。pcap 回放验证:用上一包外推到下一包实际位置,误差中位 **3cm**
  (原地不动则 43cm),3s 的直线心跳段也仅几米。
- **心跳期里玩家可能其实在转弯**,轨迹全靠 `move_seg_list` 补报(这是实时地图平滑的关键):
  - 触发上报的是**操作变化**,不是位置变化。**推住摇杆让坐骑自行盘旋、或直线巡航**时输入不变,
    客户端就只发 2.5–3s 的心跳——**那几秒里哪怕转了 175°,中途一个包都没有**(实测飞行绕圈:
    两个心跳包之间 `to_rot` 差 175°/154°)。而快速连续打方向时,飞行同样是 0.1s 一包。
    所以「静默转弯最多晚一个心跳(~3s)才可见」是游戏的上报节奏决定的,与本项目链路无关。
  - **`move_seg_list`(field 12,`MoveSegmentInfo{pos, time_stamp}`)补报那段空窗的真实轨迹**:
    按约 0.3s 一个点回传(3s 心跳里通常 7–9 个点),末点时刻≈包时刻、位置≈`to_pos`(实测 `to_pos`
    略新 0.2–0.6 个采样步长,故后端把 `to_pos` 补作轨迹终点)。密集上报时它为空或只有一两个点
    (故以 `SegSpan` 而非「是否飞行」判断值不值得回放,见 [architecture.md](architecture.md) 7)。
  - `speed` 的方向是**机头朝向**(等于 `to_rot`),盘旋时与实际行进方向能差几十度。即便如此,
    **直线外推仍是空窗期各策略中最准的**(实测以补报轨迹为真值:直线巡航均值 247cm、静默转弯
    616cm;而阻尼外推 533/809、圆弧外推 484/660、定住不动 769/1052 都更差)。
- `move_mode`(field 6)= `SceneMoveType`:1/2/3 地面(骑乘地面跑仍算地面)、6-9 飞行
  (`SMT_FLY_UP/DOWN/GLIDING/STATIC`)、11-13 游泳、16-19 攀爬。**上报节奏与该模式无关**(同上)。
- **当前场景 res 必须从 0x0152/0x015c 跟踪**,不能只看移动包的 `scene_cfg_id`——一个 scene_cfg_id
  可对应多个 scene_res(103 → 10003 卡洛西亚 或 10018 魔法学院)。
- **传送落点 `to_pt` 要用起来**:传送通知一下发就带着目的地坐标/朝向,而客户端要过几秒(加载)才落地
  并开始发移动包(实测 3-5s)。据此可立刻把地图切到目的地;否则地图会停在原地干等,玩家落地后若站着
  不动更是一直不更新。实测(3 份 pcap)`to_pt` 与落地后首个移动包的坐标/朝向一致(误差几厘米)。
- 状态机:收 0x0152/0x015c 更新当前 scene_res(并清空区域集合);收 0x0133 取 `to_pos` 配当前
  scene_res 投影;收 0x0414 的区域进/出事件维护「玩家当前所在区域」,据其 `area_func_conf_id`
  选洞穴/楼层分层地图(见 data.md 3.2)。**区域进/出是服务器在玩家真正踩进/离开触发体(3D 体积)时
  才下发的,是选层的唯一权威依据**——按位置点做 2D 多边形判定会在洞穴正上方的地表误叠洞穴图。
- **重启恢复**:进入/传送只在切场景时下发,区域进/出也只在跨越触发体时下发,游戏中途都不重发,故
  当前 scene_res 与区域集合须像会话密钥一样落盘(`sessions.scene_res` / `sessions.areas`),
  抓包服务重启后从缓存预热;否则重启后虽能解密移动包,却因不知 res 而无法
  定位底图。另有兜底:res 未知时(中途开抓/无缓存)用移动包 `scene_cfg_id` 经 SCENE_CONF 取默认 res
  (`DB.DefaultSceneRes`,同 cfg 多 res 时取主行,子场景仍以缓存/通知的精确 res 为准)。
