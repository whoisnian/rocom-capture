# rocom-capture

在 Linux 网关上被动抓取手机游戏《洛克王国：世界》的流量，解析 tsf4g/GCP 协议，
对**宠物信息**做自定义统计，并通过响应式 Web 页面展示。不读内存、不注入进程，
只解析 TCP 8195 端口的游戏流量。

## 功能

- **页面一 · 宠物列表**：种类/系别/昵称/等级/性格/特长/奖牌/声音/体重/身高/六维/捕捉时间等，支持多维筛选、排序、分页，实时更新。
- **页面二 · 捕获事件**：捕捉/孵蛋等宠物获得事件，支持按条件(种类/性格/奖牌/系别/异色)高亮提醒。
- **页面三 · 实时地图**：登录账号自己在大地图上的实时位置与朝向，进入洞穴/家园楼层时叠加分层地图，支持缩放平移。
- **页面四 · 宠物详情**：单只宠物完整信息，可一键保存为图片。
- **页面五 · 调试**：实时展示所有游戏应用层消息(opcode)。

## 效果预览

| 宠物列表 | 宠物详情 |
| --- | --- |
| ![宠物列表](docs/images/pet-list.webp) | ![宠物详情](docs/images/pet-detail.webp) |

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
| `internal/scene` | 移动/场景消息解析(自己实时位置,实时地图页;详见 docs/data.md 3.1/3.2) |
| `internal/gamedata` | id→中文名 查找表 + 场景/大地图投影(`scripts/gen_gamedata.py` 生成，embed) |
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
# 1. (可选)重新生成 proto / 名称表 / 图片,见「更新游戏数据」与 docs/data.md
#    配置数据源(nrc/all.pb + nrc/bin)已随仓库提交;脚本依赖经 uv 管理
uv sync
uv run python scripts/gen_proto.py     # nrc/all.pb → internal/pb
uv run python scripts/gen_gamedata.py  # nrc/bin + all.pb → names.json(含图标索引)
# 图片(可选,需先按「更新游戏数据」用 FModel 导出到 ~/Downloads/NRC):
uv run python scripts/gen_images.py    # 宠物头像/全身图 → img/{HeadIcon,BigHeadIcon256,Pet256} webp
uv run python scripts/gen_icons.py     # 属性/血脉/奖牌等 UI 图标 → img/{filter,blood,static,medal} webp
uv run python scripts/gen_bigmap.py    # 大地图/分层切片 → img/bigmap{,/layer} webp(实时地图页)

# 2. 构建前端到 embed 目录
cd web && npm install && npm run build && cd ..

# 3. 构建单二进制
go build -o rocom-capture ./cmd/rocom-capture
```

### 发布构建(amd64 + arm64)

抓包依赖 `gopacket/afpacket`(cgo),无法用 `CGO_ENABLED=0` 直接交叉编译。用 [zig](https://ziglang.org)
作交叉 C 编译器即可一键出两版**静态**二进制到 `dist/`——zig 自带各架构 musl libc 与 Linux 头,
**只需装 zig,无需 arm64 库/sysroot**:

```bash
# 装 zig (以本机 Arch Linux 为例)
sudo pacman -S zig

make release   # → dist/rocom-capture-linux-amd64、dist/rocom-capture-linux-arm64(均静态、已 strip)
make clean     # 清理 dist/
```

## 更新游戏数据

游戏更新后,用 [FModel](https://fmodel.app) 从 Windows 客户端重新提取,按下列**目录 + 导出格式**
导到一个下载根(默认 `~/Downloads/NRC`,可用环境变量 `IMG_SRC` 覆盖),再跑上面「构建」步骤 1 的
生成脚本。FModel 里先把 **Texture Export Format 设为 PNG**。配置类随仓库提交进 `nrc/`,图片类转成
webp 后 embed(详见 [docs/data.md](docs/data.md))。

**Export Raw Data**(原样文件 → `nrc/`)
```
Content/ScriptC/Data/Bin/    # 配置表(.bytes 数据 + .non schema + BinLocalize/dev_CN 本地化):
                             #   名称表(MONSTER/PET/MEDAL_CONF…)+ 场景/大地图
                             #   (SCENE_CONF、SCENE_RES_CONF、WORLD_MAP_BLOCK_CONF、LAYERED_WORLD_MAP_CONF)
Content/ScriptC/Data/PB/     # 描述符 all.pb(字段号 / opcode / 枚举)
```

**Save Texture**(整张贴图 → `gen_images.py` / `gen_icons.py` 的 medal 组)
```
Content/NewRoco/Modules/System/Common/Icon/BagItem/         # 奖牌小图
Content/NewRoco/Modules/System/Common/Icon/HeadIcon/        # 宠物小头像
Content/NewRoco/Modules/System/Common/Icon/BigHeadIcon256/  # 宠物大头像
Content/NewRoco/Modules/System/Common/Icon/Pet256/          # 全身缩略
Content/NewRoco/Modules/System/Common/Icon/Pet1024/         # 全身大图(暂不 embed)
```

**Export Raw Data + Save Texture + Save Properties**(图集精灵 → `gen_icons.py` 按 UV 裁切)
```
Content/NewRoco/Modules/System/Common/CommonStatic/         # 搭档标记 + 杂项静态图标
Content/NewRoco/Modules/System/Common/Icon/Species/         # 系别(属性)图标
Content/NewRoco/Modules/System/Common/Icon/XueMai/          # 血脉主图标
Content/NewRoco/Modules/System/PetUI/Raw/Atlas/PetUI/       # 六维属性图标
Content/NewRoco/Modules/System/BigMap/Raw/Atlas/WorldMapNpc/ # 大地图 POI 图标(炼金釜/矿石/植物/眠枭等)
```

**Save Texture**(大地图/分层切片 → `gen_bigmap.py`,实时地图页)
```
Content/NewRoco/Modules/System/BigMap/Raw/Texture/Maps/      # 大地图底图 4x4 瓦片(每场景 16 张,拼合)
Content/NewRoco/Modules/System/BigMap/Raw/Texture/LayerMap/  # 洞穴/地下层切片(单张,含透明通道)
```

> 分层切片 PNG 带透明背景(洞穴轮廓),PNG 导出与 `gen_bigmap.py` 均保留 alpha(存 RGBA webp);
> 底图瓦片不透明。投影参数取自上面的 `WORLD_MAP_BLOCK_CONF` / `LAYERED_WORLD_MAP_CONF`(见 docs/data.md 3.1/3.2)。

> 图集精灵(Paper2D PaperSprite)不含像素,需 **Save Texture** 出图集 PNG + **Save Properties** 出精灵
> UV(`.json`),脚本据此从图集裁出各图标。生成的 webp 保持原始解包文件名,语义键→原名映射写入
> `names.json`(见 docs/data.md)。

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

> **屏幕常亮 / HTTPS**:捕获事件页有「屏幕常亮」开关(阻止手机熄屏,方便盯着高亮提醒),
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
