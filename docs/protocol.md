# 协议说明：tsf4g / GCP

游戏客户端通过 TCP 连接远端服务器 **8195** 端口，使用腾讯 tsf4g 框架的 GCP 模式通信。
本文记录经真实流量逐字节验证的线上格式，是 `internal/gcp` 与 `internal/capture` 的实现依据。

> 概念性说明参见 tsf4g 文档(见 [AGENTS.md](../AGENTS.md) reference)。本文只记录**实测**结论。

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
| `0x0133` | c2s | `ZONE_SCENE_MOVE_REQ` | 自己移动:`to_pos`(field 2,Position 子消息)+ `scene_cfg_id`(17) |
| `0x0152` | s2c | `ZONE_ENTER_SCENE_RSP` | 进入场景:`scene_cfg_id`(2)、`scene_res_cfg_id`(3) |
| `0x015c` | s2c | `ZONE_SCENE_TELEPORT_NOTIFY` | 传送:`to_scene_cfg_id`(11)、`to_scene_res_cfg_id`(12) |
| `0x1838` | c2s | `ZONE_SCENE_CLIENT_CAVE_STATE_REQ` | 洞穴层:`cave_name`(1,string)+ `pos`(2) |
| `0x1505` | s2c | `ZONE_SCENE_CLIENT_CAVE_STATE_NOTIFY` | 同上(服务器侧下发) |

- **坐标**:`Position{x,y,z}` = UE 世界坐标,1 单位 = 1 厘米,取整。玩家 `to_pos.z` 是**脚底**高度
  (角色中心 +85);`to_rot`/`Point.dir` **不是坐标是旋转**(`FRotator×10`=0.1 度,x=Roll/y=Pitch/z=Yaw)。
- **当前场景 res 必须从 0x0152/0x015c 跟踪**,不能只看移动包的 `scene_cfg_id`——一个 scene_cfg_id
  可对应多个 scene_res(103 → 10003 卡洛西亚 或 10018 魔法学院)。
- 状态机:收 0x0152/0x015c 更新当前 scene_res;收 0x0133 取 `to_pos` 配当前 scene_res 投影;
  收 0x1838 取 `cave_name` 定洞穴层(见 data.md 3.2)。cave_name 解析同 move 包:需扫子头起点、
  容忍尾部 trailer。
- **重启恢复**:进入/传送只在切场景时下发,游戏中途不重发,故当前 scene_res 须像会话密钥一样落盘
  (`sessions.scene_res`),抓包服务重启后从缓存预热;否则重启后虽能解密移动包,却因不知 res 而无法
  定位底图。另有兜底:res 未知时(中途开抓/无缓存)用移动包 `scene_cfg_id` 经 SCENE_CONF 取默认 res
  (`DB.DefaultSceneRes`,同 cfg 多 res 时取主行,子场景仍以缓存/通知的精确 res 为准)。
