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
| `internal/pb` | 由游戏描述符 `nrc/all.pb` 生成的宠物消息结构(`scripts/gen_proto.py`) |
| `internal/pet` | PetData 解析与业务模型 |
| `internal/gamedata` | id→中文名 查找表(`scripts/gen_gamedata.py` 生成，embed) |
| `internal/store` | SQLite 存储与筛选查询 |
| `internal/server` | REST API + SSE 推送 + embed 前端 |
| `web` | React + Vite 前端 |
| `scripts/capture.sh` | tcpdump 全量抓包脚本 |

## 文档

- [协议说明](docs/protocol.md) — tsf4g/GCP 字节布局、分帧、密钥与解密、opcode
- [数据来源与解析](docs/data.md) — nrc/all.pb + nrc/bin 自有数据源、proto 与名称表生成、宠物字段映射
- [服务架构](docs/architecture.md) — 数据流、模块、HTTP 接口、前端、部署

## 构建

```bash
# 1. (可选)重新生成 proto 与名称表，见 AGENTS.md / docs/data.md
#    数据源(nrc/all.pb + nrc/bin)已随仓库提交;脚本的 protobuf 依赖经 uv 管理
uv sync
uv run python scripts/gen_proto.py     # nrc/all.pb → internal/pb
uv run python scripts/gen_gamedata.py  # nrc/bin + all.pb → names.json
# 宠物图片(可选):在 FModel 里把 Common/Icon 的 HeadIcon/BigHeadIcon256/Pet256 以 PNG 导出后
uv run python scripts/gen_images.py    # FModel PNG → internal/gamedata/data/img 的 webp(embed)

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

# 启用 HTTPS(自签证书;手机经局域网访问时用)
sudo ./rocom-capture -iface <网卡> -tls
```

浏览器打开 `http://localhost:4939`。

> **屏幕常亮 / HTTPS**:实时事件页有「屏幕常亮」开关(阻止手机熄屏,方便盯着高亮提醒),
> 但浏览器仅在 secure context(HTTPS 或 localhost)下提供该能力。手机经 `http://内网IP`
> 访问时开关会禁用,需加 `-tls`:首次不存在证书时自动生成自签证书(`-cert`/`-key` 指定路径,
> 默认 `rocom-cert.pem`/`rocom-key.pem`),SAN 覆盖 localhost 与本机所有 IP。手机打开
> `https://<内网IP>:4939` 点过安全警告后即为 secure context,开关可用。证书会持久化,
> 信任一次后重启服务仍复用;**网关 IP 变动后删除证书文件让其重新生成**即可。

> 进入游戏前先启动本工具，确保抓到 `0x1002 ACK` 中的会话密钥；
> 然后在游戏中打开宠物仓库以触发宠物列表下发。
> 密钥会随连接落库缓存,抓包服务异常重启后可对仍在线的连接自动恢复密钥继续解析(有效期 24h),
> 无需重登游戏重新协商。

## 已知限制

- 宠物位置(仓库盒子 / 大世界队伍)已支持,含运行期移动的实时增量更新。
- 咕噜球/蛋组/技能名本地化尚未梳理。
- 性格(`nature_id`)个别可能与游戏显示略有偏差。
