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

## 5. 宠物列表消息

- 客户端打开宠物仓库时发 `ZONE_GET_PET_INFO_BY_PAGE_REQ(0x1345)`；
- 服务器分页回 `ZONE_GET_PET_INFO_BY_PAGE_RSP(0x1346)`，每页约 40KB(常跨多 TCP 段)；
- RSP body 是 `ZoneGetPetInfoByPageRsp`：`total_page=2`、`req_page=3`、
  `pet_info=4`(`PetDataInfoList`，含 `repeated PetData`)、`page_num=5`。

本项目只手动取 field 4 再 `proto.Unmarshal` 成 `PetDataInfoList`，无需编译庞大的 zonesvr 消息。
解析细节见 [data.md](data.md)。
