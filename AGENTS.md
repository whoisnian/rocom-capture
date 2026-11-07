# AGENTS.md

## 项目概述

`rocom-capture`:在 Linux 网关上被动抓取手机游戏《洛克王国:世界》(进程 `com.tencent.nrc`)
的 TCP 8195 流量,解析 tsf4g/GCP 协议,对**宠物信息**做自定义统计,并提供响应式 Web 页面。
支持局域网多设备同时在线,按登录 `user_id` 隔离多账号(单库加 `account` 列,见 docs/architecture.md)。
不读内存、不注入进程,只解析网络流量。Go 后端 + React 前端,构建为单二进制(前端经 embed)。

面向使用者的说明见 [README.md](README.md);设计细节见 `docs/`:
[协议(游戏消息)](docs/protocol.md)、[宠物消息解析](docs/parsing.md)、[大地图](docs/map.md)、
[精灵蛋](docs/eggs.md)、[服务架构](docs/architecture.md)。

## 数据与协议层来自 rocom-parse

字节层(GCP 分帧/密钥/AES 解密/TCP 重组)是姊妹仓库 [rocom-parse](https://github.com/whoisnian/rocom-parse)
的 Go 包 `capture`/`gcp`(go.mod 引用;本仓库只留 afpacket 实时源 `internal/livecap`)。
解包与生成脚本也都在那边;本仓库**不含任何解包数据**:

- `internal/gamedata/data/`(names.json + img webp)与 `internal/pb/`(宠物消息 Go 结构体)都是
  `make gamedata` 调 `$(ROCOM_PARSE_DIR)/scripts/gen.sh` 生成的,gitignore;直接 `go build` 前
  必须先有生成物(`make build` 在缺失/脚本更新时自动补)。
- 更新游戏版本:重新 rsync pak → `make gamedata` → `make build`;字段变动的排查法见 rocom-parse docs/data.md。
- pcap 分析用 rocom-parse 的 `cmd/pcapdump`;录 pcap 用其 `scripts/capture.sh`。
- 本机联调 rocom-parse 未 push 的改动:`go work init . ../rocom-parse`(go.work 已 gitignore)。

## 约定

- Go:`make build`(生成物缺失或生成脚本有更新时先补 `make gamedata`,再 `go build`);
  `make release` 用 zig 交叉编译 amd64/arm64 静态二进制。游戏更新后手动跑 `make gamedata` 刷新。
- 前端:`web/` 下 `npm run build`(`make web`),产物输出到 `internal/server/web/`(已提交,便于 `go build` 开箱即用)。
- 页面:宠物列表/详情、捕获事件、精灵蛋、实时地图,前端路由与 SSE 类型见 docs/architecture.md 6/7。
- 文档与注释只描述当前版本的做法,不记录版本变迁;不引用私有仓库。
