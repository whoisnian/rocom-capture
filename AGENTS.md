# AGENTS.md

## 项目概述

`rocom-capture`：在 Linux 网关上被动抓取手机游戏《洛克王国：世界》(进程 `com.tencent.nrc`)
的 TCP 8195 流量，解析 tsf4g/GCP 协议，对**宠物信息**做自定义统计，并提供响应式 Web 页面。
不读内存、不注入进程，只解析网络流量。Go 后端 + React 前端，构建为单二进制(前端经 embed)。

面向使用者的说明见 [README.md](README.md)；设计细节见 `docs/`：
[协议](docs/protocol.md)、[数据来源与解析](docs/data.md)、[服务架构](docs/architecture.md)。

## 约定

- Go：`go build ./...`；proto/名称表生成见 `scripts/gen_proto.sh`、`scripts/gen_gamedata.py`。
- 数据来源:名称表/opcode 用 pak-public-kit(当前 NRC 版本,持续更新);`internal/pb`
  的 protobuf 字段号仍由 world-data 的 `.proto` 生成(字段号追加式稳定,见 docs/data.md)。
- 前端：`web/` 下 `npm run build`，产物输出到 `internal/server/web/`(已提交，便于 `go build` 开箱即用)。
- Python 脚本依赖用 uv 管理(项目内 `.venv`)，勿用系统级 pip。
- `internal/pb/*.pb.go` 与 `internal/gamedata/data/names.json` 为生成物，改动应改生成脚本而非手改。

## reference
| source                                                       | directory                                   | description                          |
| ------------------------------------------------------------ | ------------------------------------------- | ------------------------------------ |
| https://github.com/phainia/pak-public-kit                    | ~/Git/gh/pak-public-kit                     | **名称表/opcode 主数据源**(当前 NRC 解包,output 已提交,git pull 跟新) |
| https://github.com/kikozz/Roco-Kingdom-World-Data-2026-05-21 | ~/Git/gh/Roco-Kingdom-World-Data-2026-05-21 | `internal/pb` 字段号来源(`.proto`;pak-public-kit 不导出描述符) |
| https://github.com/h3110w0r1d-y/rocom-helper                 | ~/Git/gh/rocom-helper                       | 闭源洛克王国世界助手，本项目受其启发 |
| https://github.com/lsj9383/blog                              | ~/Git/gh/blog                               | tsf4g 通信协议说明                   |
| https://github.com/yuzeis/Roco-Kingdom-Protocol-Parser       | ~/Git/gh/Roco-Kingdom-Protocol-Parser       | 开源洛克王国协议解析器，简称 RKPP    |
