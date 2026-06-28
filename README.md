# rocom-capture

在 Linux 网关上被动抓取手机游戏《洛克王国：世界》的流量，解析 tsf4g/GCP 协议，
对**宠物信息**做自定义统计，并通过响应式 Web 页面展示。不读内存、不注入进程，
只解析 TCP 8195 端口的游戏流量。

## 功能

- **页面一 · 宠物列表**：种类/系别/昵称/等级/性格/特长/奖牌/声音/体重/身高/六维/捕捉时间等，支持多维筛选、排序、分页，实时更新。
- **页面二 · 实时事件**：捕捉/孵蛋等宠物增减事件，支持按条件(种类/性格/奖牌/系别/异色)高亮提醒。
- **页面三 · 宠物详情**：单只宠物完整信息，可一键保存为图片。
- **页面四 · 调试**：实时展示所有游戏应用层消息(opcode)。

## 架构

```
afpacket/pcap → TCP 重组 → GCP 分帧 → 0x1002 取密钥 → 0x4013 AES-CBC 解密
  → opcode 路由 → PetData(protobuf) 解析 → 名称本地化 → SQLite → REST/SSE → React 前端
```

| 目录 | 说明 |
| --- | --- |
| `internal/gcp` | GCP 分帧、密钥提取、AES 解密 |
| `internal/capture` | afpacket 实时抓包 / pcap 离线回放 + TCP 重组 |
| `internal/pb` | 由游戏描述符 `proto/all.pb` 生成的宠物消息结构(`scripts/gen_proto.sh`) |
| `internal/pet` | PetData 解析与业务模型 |
| `internal/gamedata` | id→中文名 查找表(`scripts/gen_gamedata.py` 生成，embed) |
| `internal/store` | SQLite 存储与筛选查询 |
| `internal/server` | REST API + SSE 推送 + embed 前端 |
| `web` | React + Vite 前端 |
| `scripts/capture.sh` | tcpdump 全量抓包脚本 |

## 文档

- [协议说明](docs/protocol.md) — tsf4g/GCP 字节布局、分帧、密钥与解密、opcode
- [数据来源与解析](docs/data.md) — all.pb / pak-public-kit 数据源、proto/名称表生成、宠物字段映射
- [服务架构](docs/architecture.md) — 数据流、模块、HTTP 接口、前端、部署

## 构建

```bash
# 1. (可选)重新生成 proto 与名称表，见 AGENTS.md / docs/data.md
#    proto 默认读仓库内 proto/all.pb;名称表需 pak-public-kit
bash scripts/gen_proto.sh
python3 scripts/gen_gamedata.py

# 2. 构建前端到 embed 目录
cd web && npm install && npm run build && cd ..

# 3. 构建单二进制
go build -o rocom-capture ./cmd/rocom-capture
```

## 运行

```bash
# 实时抓包(需 root；网卡需为手机流量的必经之路)
sudo ./rocom-capture -iface <网卡> -port 8195 -addr :4939

# 离线回放已抓的 pcap
./rocom-capture -pcap ./pcap/xxx.pcap -addr :4939
```

浏览器打开 `http://localhost:4939`。

> 进入游戏前先启动本工具，确保抓到 `0x1002 ACK` 中的会话密钥；
> 然后在游戏中打开宠物仓库以触发宠物列表下发。

## 已知限制

- 六维展示为基础面板值，最终面板值(含努力/奖牌加成)的精确公式待补。
- 部分名称(咕噜球/蛋组/技能名)本地化尚未完全梳理。
- 放生/赠送(宠物减少)事件依赖专门 opcode，暂以列表 diff 近似。
