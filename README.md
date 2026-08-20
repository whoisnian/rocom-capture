# rocom-capture

在 Linux 网关上被动抓取手机游戏《洛克王国：世界》的流量，解析 tsf4g/GCP 协议，
对**宠物信息**做自定义统计，并通过响应式 Web 页面展示。不读内存、不注入进程，
只解析 TCP 8195 端口的游戏流量。

## 功能

- **页面一 · 宠物列表**：种类/系别/昵称/等级/性格/特长/奖牌/声音/体重/身高/六维/捕捉时间等，支持多维筛选、排序、分页，实时更新。
- **页面二 · 捕获事件**：捕捉/孵蛋等宠物获得事件，支持按条件(种类/性格/奖牌/系别/异色)高亮提醒。
- **页面三 · 实时地图**：登录账号自己在大地图上的实时位置与朝向，进入洞穴/家园楼层时叠加分层地图，支持缩放平移；
  可开关叠加 POI 图标(魔力之源、炼金釜默认开；守护地、大/小型眠枭庇护所、蓝/黄眠枭之星默认关)，选择本地记住；
  眠枭之星有「收集模式」：隐藏已收集的(区域收满的整片隐藏，其余随角色移动逐点确认)，只留还没拿的；
  另有「野生宠物」图层：把周围刷出的稀有个体(异色/炫彩、污染、大块头/小不点、
  婉转声/粗嗓门及各自 MAX)标在地图上,支持 OR 及「同属性组内 OR、不同组间 AND」筛选——
  这些属性捕捉前后一致，丢球之前就能筛(见 docs/data.md 3.5)。
  还有「涂地」图层:每见到一只野生宠,就把你和它之间那条带子涂上色——那条线上的宠物确实下发过,
  稀有的若有早就标出来了;一只没刷的方向不涂、首领不算数(它下发得远得多),故涂色跟着实际
  下发情况走而不是固定距离;此外走过的路两侧 15m 一律算扫过(不刷宠的城镇/峭壁否则永远空着,
  15m 这个数取自历史流量统计),照着没涂的地方走即可遍历。
  可开关、可按场景重置(见 docs/data.md 3.8)。
  在**家园**里另有「精灵小窝」标记(始终显示，无需开关)：10 个小窝的位置与入住的宠物(空窝也画出来)，
  悬浮看住户简要信息(`点点 ♀ Lv.1 · W 90% V -50 急躁`)，点头像看宠物详情；
  窝上还没收的蛋会挂个蛋图标。
- **页面四 · 精灵蛋**：左栏孵蛋器(在孵的那几颗，带进度)、右栏背包(每行六个，同游戏内布局)，
  排序复刻游戏内的「品质」与「获取时间」两种。每张卡片：蛋图、名称、品类角标(异色/炫彩/珍贵…)、
  孵出物种头像、重量/声音/高度/时间，以及**破壳前就能定的奖牌**(大块头/小不点：蛋的百分位
  孵化后原样保留)；蛋上没有的信息(声音、嗓音奖牌)留占位，卡片等高。
  家园小窝收的蛋会记下**推测的双亲**(母本 = 蛋所在窝的宠物，父本取服务器下发的配对候选)，
  按收蛋当时的快照存库，亲本日后放生/赠送也不影响。
- **页面五 · 宠物详情**：单只宠物完整信息，可一键保存为图片。
- **页面六 · 调试**：实时展示所有游戏应用层消息(opcode)。

## 效果预览

### 宠物列表
![宠物列表](docs/images/pet-list.webp)

### 实时地图
![实时地图](docs/images/live-map.webp)

## 架构

```
afpacket/pcap → TCP 重组 → GCP 分帧 → 0x1002 取密钥 → 0x4013 AES-CBC 解密
  → opcode 路由 → PetData(protobuf) 解析 → 名称本地化 → SQLite → REST/SSE → React 前端
```

| 目录 | 说明 |
| --- | --- |
| `internal/gcp` | GCP 分帧、密钥提取、AES 解密 |
| `internal/capture` | afpacket 实时抓包 / pcap 离线回放 + TCP 重组 |
| `internal/pb` | 由游戏描述符 all.pb 生成的宠物消息结构(`scripts/gen_proto.py`) |
| `internal/pet` | PetData 解析与业务模型 |
| `internal/scene` | 移动/场景/实体消息解析(实时位置、分层、野生宠物、捕捉结果;详见 docs/data.md 3.1/3.2/3.5) |
| `internal/gamedata` | id→中文名 查找表 + 场景/大地图投影(`scripts/gen_gamedata.py` 生成，embed) |
| `internal/store` | SQLite 存储与筛选查询 |
| `internal/server` | REST API + SSE 推送 + embed 前端 |
| `web` | React + Vite 前端 |
| `scripts/capture.sh` | tcpdump 全量抓包脚本 |

## 文档

- [协议说明](docs/protocol.md) — tsf4g/GCP 字节布局、分帧、密钥与解密、opcode
- [数据来源与解析](docs/data.md) — 解包数据源(all.pb + Bin 配置)、proto 与名称表生成、宠物字段映射
- [服务架构](docs/architecture.md) — 数据流、模块、HTTP 接口、前端、部署
- [参考资料](docs/reference.md) — 相关工具与开源项目

## 构建

```bash
# 1. (可选)重新生成 proto / 名称表 / 图片,见「更新游戏数据」与 docs/data.md
#    生成物(internal/pb、names.json、img webp)已随仓库提交,不更新游戏数据可跳过;
#    重新生成需先按「更新游戏数据」解包到 ~/Downloads/rocom/parsed;脚本依赖经 uv 管理
uv sync
uv run python scripts/gen_proto.py     # all.pb → internal/pb
uv run python scripts/gen_gamedata.py  # Bin 配置 + all.pb → names.json(含图标索引)
uv run python scripts/gen_images.py    # 宠物头像/全身图 → img/{HeadIcon,BigHeadIcon256,Pet256} webp
uv run python scripts/gen_icons.py     # 属性/血脉/奖牌/POI 等 UI 图标 → img/{filter,blood,static,worldmap,medal} webp
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

游戏更新后三步(详见 [docs/data.md](docs/data.md)):

```bash
# 1. 从游戏目录原样复制 pak(Windows 客户端 <安装目录>\Win64\NRC\Content\Paks)
cp -r <游戏Paks目录>/* ~/Downloads/rocom/Paks/

# 2. 解包到 ~/Downloads/rocom/parsed/(增量,产物不比来源 pak 旧才跳过;需 dotnet SDK 与 CUE4Parse 克隆;
#    默认排除三维美术/视频/音频等与数据链无关的大目录,--no-exclude 可真·全量;
#    导出后自动 .bytes→JSON、luac→lua 反编译(需 unluac,--no-post 跳过))
./scripts/unpack.sh

# 3. 重跑「构建」步骤 1 的生成脚本
```

解包按虚拟路径镜像导出:`.uasset`/`.umap` → 属性 `.json`(纹理另出 `.png`),其余
(`.bytes`/`.non`/`.pb`/`.lua` 等)原样字节。生成脚本直接读 `parsed/`(解包根可用环境变量
`ROCOM_PARSED` 覆盖),仓库只提交精炼后的生成物(`internal/pb`、`names.json`、webp 图片)。

## 运行

```bash
# 实时抓包(需 root；网卡需为手机流量的必经之路)
sudo ./rocom-capture -iface <网卡>

# 离线回放已抓的 pcap
./rocom-capture -pcap ./pcap/xxx.pcap

# 启用 HTTPS(自签证书;手机经局域网访问时用)
sudo ./rocom-capture -iface <网卡> -tls

# 不需要内置 SOCKS5 时显式关闭
sudo ./rocom-capture -iface <网卡> -socks5 ''
```

浏览器打开 `http://localhost:4949`。`-pcap` 与 `-iface` 至少传一个;若同时传入,
按离线回放处理。离线回放完成后 Web 服务会继续运行,直到 `Ctrl-C` 退出。

### 命令行参数

| 参数 | 默认 | 说明 |
| --- | --- | --- |
| `-iface <name>` | 空 | 实时抓包网卡;需 root,且该网卡必须能看到手机的 TCP 8195 流量 |
| `-pcap <path>` | 空 | 离线回放 pcap;与 `-iface` 同时给出时优先 |
| `-port <port>` | `8195` | 游戏服务器 TCP 端口 |
| `-ignore-ip <ip,...>` | 空 | 额外忽略的 IP,源或目的任一端命中即丢包;支持逗号分隔多个地址 |
| `-socks5 <addr>` | `:4948` | 内置无认证 SOCKS5 监听地址;仅允许 `CONNECT` 到 `-port`;传空字符串关闭 |
| `-addr <addr>` | `:4949` | Web HTTP/HTTPS 监听地址 |
| `-db <path>` | `rocom.db` | SQLite 数据库路径(也保存会话密钥与账号归属) |
| `-tls` | `false` | 启用 HTTPS |
| `-cert <path>` | `rocom-cert.pem` | TLS 证书;启用 TLS 且文件不存在时自动生成 |
| `-key <path>` | `rocom-key.pem` | TLS 私钥;启用 TLS 且文件不存在时自动生成 |

> **SOCKS5 抓包与安全**:默认 `:4948` 会在所有本机地址上开启**无认证**代理,但只接受
> `CONNECT` 到 `-port` 指定的游戏端口(默认 TCP 8195),其他目标端口及其他 SOCKS 命令会被拒绝。
> 端口规则产生的预期拒绝不会写入标准错误输出或 systemd journal,真正的代理错误仍会记录。
> 手机使用该代理后,服务会把每条代理出站连接的本地 `IP:临时端口` 精确加入抓包例外,
> 使其不被网卡本机 IP 去重规则过滤;连接关闭后撤销例外。仍须同时传 `-iface` 并确保代理
> 出站流量经过该网卡,SOCKS5 本身不代替抓包数据源。只应在可信局域网中开启并用防火墙
> 限制访问;不需要时使用 `-socks5 ''`,或用 `-socks5 <网关局域网IP>:4948` 收窄监听地址。

> **屏幕常亮 / HTTPS**:捕获事件页有「屏幕常亮」开关(阻止手机熄屏,方便盯着高亮提醒),
> 但浏览器仅在 secure context(HTTPS 或 localhost)下提供该能力。手机经 `http://内网IP`
> 访问时开关会禁用,需加 `-tls`:首次不存在证书时自动生成自签证书(`-cert`/`-key` 指定路径,
> 默认 `rocom-cert.pem`/`rocom-key.pem`),SAN 覆盖 localhost 与本机所有 IP。手机打开
> `https://<内网IP>:4949` 点过安全警告后即为 secure context,开关可用。证书会持久化,
> 信任一次后重启服务仍复用;**网关 IP 变动后删除证书文件让其重新生成**即可。

> 进入游戏前先启动本工具，确保抓到 `0x1002 ACK` 中的会话密钥；
> 然后在游戏中打开宠物仓库以触发宠物列表下发。
> 密钥会随连接落库缓存,抓包服务异常重启后可对仍在线的连接自动恢复密钥继续解析(有效期 24h),
> 无需重登游戏重新协商。
