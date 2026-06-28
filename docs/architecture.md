# 服务架构

单进程 Go 服务：一边被动抓包解析，一边对外提供 Web 界面。前端构建产物经 `embed`
打包进同一个二进制，部署时无外部文件依赖。

## 1. 数据流

```
                         ┌─────────────── capture.Engine ───────────────┐
 网卡(afpacket)/pcap ──→ │ 读包 → TCP重组 → GCP分帧 → 取密钥 → AES解密     │
                         │        → opcode 路由                          │
                         └───────────────────┬──────────────────────────┘
                                             │ chan Message{dir,opcode,appBody}
                                             ▼
                              ┌──────── consume(main) ────────┐
                              │ 0x1346 → pet.ParsePetListRsp   │
                              │       → pet.ToPet(+gamedata)   │
                              │       → store.UpsertPet        │
                              │       → 新增且实时 → 事件       │
                              │ 任意消息 → debug 广播           │
                              └───────┬───────────────┬────────┘
                                      ▼               ▼
                                 store(SQLite)      hub(SSE 广播)
                                      ▲               │
                              REST API│               │实时推送
                                      └──── server ───┘
                                             │
                                       embed React 前端
```

## 2. 模块(`internal/`)

| 包 | 职责 |
| --- | --- |
| `gcp` | GCP 分帧(`Deframe`)、密钥提取(`ExtractKey`)、AES 解密(`DecryptData`)、opcode 提取 |
| `capture` | 数据源(`afpacket` 实时 / `pcapgo` 离线)+ `reassembly` TCP 重组 + 会话密钥管理，输出 `Message` |
| `pb` | 游戏描述符 `proto/all.pb` 生成的宠物消息结构(生成物) |
| `pet` | `ParsePetListRsp` 解析宠物列表；`ToPet` 转中文化业务模型 |
| `gamedata` | embed 的 id→中文名 查找库 |
| `store` | SQLite 持久化(宠物当前状态表 + 事件历史表)与多维筛选查询 |
| `server` | REST API、SSE 广播(`Hub`)、embed 前端静态资源 |

`cmd/rocom-capture/main.go` 组装上述模块并启动抓包与 HTTP。

## 3. 抓包与重组要点

- **方向判定**：`reassembly` 每个 TCP 连接只创建一个 `Stream`，双向数据经同一
  `ReassembledSG`，用 `sg.Info()` 的方向 + 触发包端口映射为 c2s/s2c。
- **会话密钥共享**：c2s/s2c 两个半连接归一化为同一 `session`，ACK(下行)提取的密钥
  供同会话的 DATA 解密。
- **实时 vs 离线**：二者共用 `process()`；`afpacket` 无需 libpcap(纯 AF_PACKET)。

## 4. 事件判定

宠物列表全量下发时，库中不存在的 `gid` 即"新增"。为区分**初始仓库快照**与
**运行期新捕捉**，仅当 `PetData.add_time ≥ 服务启动时间`(留 120s 余量)才记为
"获得"事件并推送。离线回放历史 pcap 因 `add_time` 是过去时间，不会误报事件。

## 5. HTTP 接口

| 方法 路径 | 说明 |
| --- | --- |
| `GET /api/pets` | 宠物列表(筛选/排序/分页)，参数：`search,types,nature,gender,talentRank,medal,speciality,partnerMark,shiny,levelMin,levelMax,sort,order,page,pageSize` |
| `GET /api/pets/{gid}` | 单只宠物详情 |
| `GET /api/events` | 事件历史(`limit,beforeId`) |
| `GET /api/filter-options` | 各筛选维度的可选值 |
| `GET /api/stats` | 统计(宠物总数) |
| `GET /api/stream` | SSE，实时推送 `{type: pet\|event\|debug, data}` |
| `GET /*` | 前端 SPA(未匹配路径回退 index.html) |

## 6. 前端(`web/`，React + Vite)

- 路由(HashRouter)：`/pets` 列表、`/pets/:gid` 详情、`/events` 事件、`/debug` 调试。
- 实时：`EventSource('/api/stream')` 订阅，列表防抖刷新、事件流追加、调试流追加。
- 响应式：桌面表格 / 移动卡片，桌面顶栏 / 移动底部 tab(CSS media query)。
- 详情页用 `html-to-image` 导出 PNG。
- 构建输出到 `internal/server/web/`，由 Go `embed`。

## 7. 部署形态

单二进制 `rocom-capture`：
- 实时：`sudo ./rocom-capture -iface <网卡> -addr :4939`(需 root，网卡须为手机流量必经)
- 离线：`./rocom-capture -pcap <文件> -addr :4939`

数据库默认 `rocom.db`(SQLite 文件)。详见 [README](../README.md)。
