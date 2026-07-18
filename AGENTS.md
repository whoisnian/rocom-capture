# AGENTS.md

## 项目概述

`rocom-capture`：在 Linux 网关上被动抓取手机游戏《洛克王国：世界》(进程 `com.tencent.nrc`)
的 TCP 8195 流量，解析 tsf4g/GCP 协议，对**宠物信息**做自定义统计，并提供响应式 Web 页面。
支持局域网多设备同时在线,按登录 `user_id` 隔离多账号(单库加 `account` 列,见 docs/architecture.md)。
不读内存、不注入进程，只解析网络流量。Go 后端 + React 前端，构建为单二进制(前端经 embed)。

面向使用者的说明见 [README.md](README.md)；设计细节见 `docs/`：
[协议](docs/protocol.md)、[数据来源与解析](docs/data.md)、[服务架构](docs/architecture.md)、
[参考资料](docs/reference.md)。

## 解包数据流程

原始解包数据**不进仓库**,仓库只提交精炼后的生成物(详见 docs/data.md):

1. 从游戏目录原样复制 pak 到 `~/Downloads/rocom/Paks/`(或直接用安卓 .apk)。
2. `scripts/unpack.sh` 用 CUE4Parse 全量解包到 `~/Downloads/rocom/parsed/`,按虚拟路径镜像
   (顶层 `NRC/Content/...`):uasset/umap → 属性 json(纹理另出 png),其余(.bytes/.non/.pb/
   .lua 等)原样字节。并行、增量,`--filter <前缀>` 选导、`--list` 预览;默认排除三维美术/
   视频/音频等纯客户端运行时资源(`--exclude` 追加、`--no-exclude` 全量,清单见 --help)。C# 实现在
   `scripts/unpack/`,依赖 dotnet-sdk 与 CUE4Parse 克隆(默认 `~/Git/gh/CUE4Parse`,
   `CUE4PARSE_DIR` 覆盖;内置 `GAME_RocoKingdomWorld` 支持)。
   导出后自动跑两个后置步骤(增量,`--no-post` 跳过):全树 RocoBinData `.bytes` → 紧邻 `.json`
   (`scripts/bin2json.py`,需 uv;既供查数据也是 gen_* 输入)、`.luac` → `.lua` 反编译
   (`scripts/decompile_luac.sh`,需 unluac,单文件超时兜住死循环、真失败打 `.lua.nodecomp` 标记免重试)。
3. 生成脚本直接读 `parsed/`(解包根统一用环境变量 `ROCOM_PARSED` 覆盖,默认
   `~/Downloads/rocom/parsed`),产出随仓库提交的生成物。

## 约定

- Go：`go build ./...`。代码生成:`uv run python scripts/gen_proto.py`(all.pb → internal/pb)、
  `uv run python scripts/gen_gamedata.py`(Bin 配置 + all.pb → names.json)、
  `uv run python scripts/gen_images.py`(解包 PNG → internal/gamedata/data/img 的宠物图 webp)、
  `uv run python scripts/gen_icons.py`(UI 图标 → img/{filter,blood,static,worldmap,medal}:属性/
  六维/搭档标记、血脉主图标、手挑杂项、手挑大地图 POI、奖牌小图;图集精灵从解包属性 JSON + 图集 PNG 裁切,
  奖牌等整张贴图直接转码;webp 保持原始解包文件名,语义键→原名索引写入 names.json;详见
  docs/data.md)、`uv run python scripts/gen_bigmap.py`(大地图瓦片 → img/bigmap 整图 webp,4x4
  行主序拼合;另转分层地图切片 LayerMap → img/bigmap/layer;坐标单位/投影见 docs/data.md 3.1/3.2);
  抓包脚本 `scripts/capture.sh`(bash)。`.bytes` 配置解码用 `uv run python scripts/bin2json.py`
  (unpack.sh 已自动调):全树 RocoBinData `.bytes` → 紧邻 `.json`(增量,秒级),既供 grep/jq
  查数据、也是 gen_gamedata/gen_icons 的输入(它们直接读这些 JSON,不再自行解 .bytes)。
- pcap 调试:`go run ./cmd/pcapdump -pcap <文件>` 把回放消息输出为「适合 AI 分析」的结构化文本,
  免去为调试新协议临时写一次性程序。三种模式:无参=opcode 概览(次数/方向/名称);
  `-op 0x1888,FREE`=转储匹配 opcode 的消息头 + 通用 protobuf 解码树(opcode 支持 hex/十进制/名称子串,
  `-hex` 附原始字节);`-gid 20508,15895`=扫描某宠物编号出现在哪些 opcode。解码为 wire 级、
  不依赖 .proto(规避版本错位),自动跳过 c2s 子头并在 tsf4g 尾前停止。
- 数据来源**均为自行解包提取**,不依赖外部数据仓库:中文名称表来自解包目录的 Bin 配置
  (由 `scripts/bin2json.py` 按 CUE4Parse 的 FRocoBinData 算法解为 JSON);`internal/pb` 结构、opcode、枚举同出
  游戏描述符 all.pb(前者经 protoc `--descriptor_set_in` 生成 Go,后者经 `scripts/pbdesc.py`
  读描述符);宠物图片索引(conf_id→头像/全身图)取自 `PETBASE_CONF`/`MODEL_CONF`,
  图片本体(webp)经解包 PNG 转码后 embed。更新游戏版本:重新复制 pak、重跑 unpack.sh、
  重跑生成脚本(详见 docs/data.md)。
- 前端：`web/` 下 `npm run build`，产物输出到 `internal/server/web/`(已提交，便于 `go build` 开箱即用)。
- Python 脚本依赖用 uv 管理(项目内 `.venv`)，勿用系统级 pip。
- `internal/pb/*.pb.go` 与 `internal/gamedata/data/names.json` 为生成物，改动应改生成脚本而非手改。
- 相关工具与开源项目清单见 [docs/reference.md](docs/reference.md)。
