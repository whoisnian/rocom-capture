# AGENTS.md

## 项目概述

`rocom-capture`：在 Linux 网关上被动抓取手机游戏《洛克王国：世界》(进程 `com.tencent.nrc`)
的 TCP 8195 流量，解析 tsf4g/GCP 协议，对**宠物信息**做自定义统计，并提供响应式 Web 页面。
支持局域网多设备同时在线,按登录 `user_id` 隔离多账号(单库加 `account` 列,见 docs/architecture.md)。
不读内存、不注入进程，只解析网络流量。Go 后端 + React 前端，构建为单二进制(前端经 embed)。

面向使用者的说明见 [README.md](README.md)；设计细节见 `docs/`：
[协议](docs/protocol.md)、[数据来源与解析](docs/data.md)、[服务架构](docs/architecture.md)。

## 约定

- Go：`go build ./...`。代码生成:`uv run python scripts/gen_proto.py`(→ internal/pb)、
  `uv run python scripts/gen_gamedata.py`(→ names.json)、`uv run python scripts/gen_images.py`
  (FModel PNG → internal/gamedata/data/img 的 webp,需先在 FModel 里 PNG 导出 Icon 目录)、
  `uv run python scripts/gen_icons.py`(UI 图标 → img/{filter,blood,static,worldmap,medal}:属性/
  六维/搭档标记、血脉主图标、手挑杂项、手挑大地图 POI、奖牌小图;图集精灵从 FModel PaperSprite JSON + 图集 PNG 裁切,
  奖牌等整张贴图直接转码;webp 保持原始解包文件名,语义键→原名索引写入 names.json;详见
  docs/data.md)、`uv run python scripts/gen_bigmap.py`(大地图瓦片 → img/bigmap 整图 webp,4x4
  行主序拼合;另转分层地图切片 LayerMap → img/bigmap/layer;坐标单位/投影见 docs/data.md 3.1/3.2);
  抓包脚本 `scripts/capture.sh`(bash)。
- pcap 调试:`go run ./cmd/pcapdump -pcap <文件>` 把回放消息输出为「适合 AI 分析」的结构化文本,
  免去为调试新协议临时写一次性程序。三种模式:无参=opcode 概览(次数/方向/名称);
  `-op 0x1888,FREE`=转储匹配 opcode 的消息头 + 通用 protobuf 解码树(opcode 支持 hex/十进制/名称子串,
  `-hex` 附原始字节);`-gid 20508,15895`=扫描某宠物编号出现在哪些 opcode。解码为 wire 级、
  不依赖 .proto(规避版本错位),自动跳过 c2s 子头并在 tsf4g 尾前停止。
- 数据来源**全部随仓库提交、均为 FModel 自行提取**,不依赖外部仓库:中文名称表来自
  `nrc/bin/`(游戏二进制配置,用 vendored 的 `scripts/decode_bin.py` 解码);`internal/pb`
  结构、opcode、枚举同出游戏描述符 `nrc/all.pb`(前者经 protoc `--descriptor_set_in` 生成 Go,
  后者经 `scripts/pbdesc.py` 读描述符);宠物图片索引(conf_id→头像/全身图)取自 `nrc/bin/`
  的 `PETBASE_CONF`/`MODEL_CONF`,图片本体(webp)经 FModel PNG 导出 + `gen_images.py` 转码后
  embed。更新游戏版本:FModel 重新提取覆盖 `nrc/bin/` 与 `nrc/all.pb` 再跑生成脚本(详见 docs/data.md)。
- 前端：`web/` 下 `npm run build`，产物输出到 `internal/server/web/`(已提交，便于 `go build` 开箱即用)。
- Python 脚本依赖用 uv 管理(项目内 `.venv`)，勿用系统级 pip。
- `internal/pb/*.pb.go` 与 `internal/gamedata/data/names.json` 为生成物，改动应改生成脚本而非手改。

## reference
| source                                                       | directory                                   | description                                                           |
| ------------------------------------------------------------ | ------------------------------------------- | --------------------------------------------------------------------- |
| FModel 自行提取数据                                          | `~/Downloads/NRC` 挑选得到 `./nrc`          | **当前唯一数据源**:描述符(字段号/opcode/枚举)+ 二进制配置(中文名称表) |
| https://github.com/MIXUULS/Roco-Kingdom-World-Data           | `./scripts/decode_bin.py`                   | 解 `nrc/bin/` 的 `.bytes`                                             |
| https://github.com/phainia/pak-public-kit                    | ~/Git/gh/pak-public-kit                     | 已弃用(曾为名称表源,其 PET_CONF 本地化错位;仅留作对照)                |
| https://github.com/kikozz/Roco-Kingdom-World-Data-2026-05-21 | ~/Git/gh/Roco-Kingdom-World-Data-2026-05-21 | 已弃用(曾为 `.proto` 源;其 Bin JSON 可作名称表三方对照)               |
| https://github.com/h3110w0r1d-y/rocom-helper                 | ~/Git/gh/rocom-helper                       | 闭源洛克王国世界助手，本项目受其启发                                  |
| https://github.com/lsj9383/blog                              | ~/Git/gh/blog                               | tsf4g 通信协议说明                                                    |
| https://github.com/yuzeis/Roco-Kingdom-Protocol-Parser       | ~/Git/gh/Roco-Kingdom-Protocol-Parser       | 开源洛克王国协议解析器，简称 RKPP                                     |
