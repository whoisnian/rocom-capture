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
                              │ connID→account 归属(见 §5)    │
                              │ 0x1346 → pet.ParsePetListRsp   │
                              │       → pet.ToPet(+gamedata)   │
                              │       → store.For(acc).UpsertPet│
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
| `pb` | 游戏描述符 `nrc/all.pb` 生成的宠物消息结构(生成物) |
| `pet` | `ParsePetListRsp` 解析宠物列表；`ToPet` 转中文化业务模型；`ParseLoginAccount` 取登录 user_id/昵称 |
| `gamedata` | embed 的 id→中文名 查找库 |
| `store` | SQLite 持久化,按 `account` 分区(宠物/盒队/奖牌/事件 + `accounts` 表)与多维筛选查询;`For(account)` 返回绑定账号的 `*Scoped` 视图 |
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

## 5. 多账号隔离

局域网内多台设备(手机/平板/PC)可同时在线,各账号数据在**同一 SQLite 库**内隔离
(单库加 `account` 列,而非分库)。

- **身份 = 玩家 `user_id`**(账号键 `"role:"+user_id`),取自 `ZONE_LOGIN_RSP(0x0102)`:
  wire 三层下钻 `AppBody → #2(LoginData) → #1(base) → {#1=user_id(varint), #3=nickname}`
  (`pet.ParseLoginAccount`)。**不用客户端 IP**——多台设备常经 NAT 共用同一 IP(实测两设备
  同为 `10.0.3.201`,仅连不同游戏服),会把不同用户合并;`user_id` 全局唯一、跨设备/跨服/
  换 IP 稳定。(旁证:s2c internal header `[6:10]` 是响应序号 seq 而非 uid,不能用作身份。)
- **归属**:`consume` 维护 `connID→account` 映射。遇 `LOGIN_RSP` **先**解析 user_id 写映射、
  **再**据 connID 求 account(登录回包自带背包/队伍/奖牌快照,须先登记否则错归到旧账号);
  未登记连接(登录前/漏抓登录)的消息直接丢弃,不回退 IP。
- **存储**:`pets/pet_box/pet_team/pet_medal` 均加 `account` 列 + 复合主键 `(account,gid)`
  (`events` 保留自增主键 + `account` 列);`accounts` 表存 `user_id→昵称`。
  `store.Store.For(account)` 返回绑定该账号的 `*Scoped` 视图,所有按账号读写只经它、SQL 一律带
  `account=?`——漏传即编译错误。复合主键使同一 `gid` 可在不同账号并存,互不覆盖。
- **实时/接口**:SSE 每条广播带 `account` 字段(调试消息为空);前端各页按当前账号过滤
  (调试页不过滤,并显示来源账号)。REST 用 `?account=` 选账号(缺省回退最近活跃)。
- **已知限制**:必须抓到某连接的 `LOGIN_RSP` 才能归属它(中途接入抓包会漏到该连接下次登录)。

## 6. HTTP 接口

| 方法 路径 | 说明 |
| --- | --- |
| `GET /api/pets` | 宠物列表(筛选/排序/分页)，参数：`account,search,types,nature,gender,talentRank,medal,speciality,partnerMark,shiny,levelMin,levelMax,sort,order,page,pageSize` |
| `GET /api/pets/{gid}` | 单只宠物详情(`?account=`) |
| `GET /api/events` | 事件历史(`account,limit,beforeId`) |
| `GET /api/accounts` | 已知账号列表(`account,name,petCount`),供账号切换下拉 |
| `GET /api/filter-options` | 各筛选维度的可选值(`?account=`) |
| `GET /api/stats` | 统计(当前账号宠物总数,`?account=`) |
| `GET /api/stream` | SSE，实时推送 `{type: pet\|event\|debug, account, data}` |
| `GET /*` | 前端 SPA(未匹配路径回退 index.html) |

> 除 `/api/accounts` 与静态数据(`/api/medals`、`/api/evolution`)外,读接口均按 `?account=`
> 收窄;缺省(不传)回退到最近活跃账号。

## 7. 前端(`web/`，React + Vite)

- 路由(HashRouter)：`/pets` 列表、`/pets/:gid` 详情、`/events` 事件、`/debug` 调试。
- **账号切换**:顶栏下拉(`/api/accounts` 填充),`AccountContext` 下发当前账号;切换时经
  `<main key={account}>` 轻量重挂各页(不整页 reload),`api.js` 各请求自动带 `?account=`,
  各页对 SSE 按 `msg.account` 过滤(调试页不过滤)。
- 实时：`EventSource('/api/stream')` 订阅，列表防抖刷新、事件流追加、调试流追加。
- 响应式：桌面表格 / 移动卡片，桌面顶栏 / 移动底部 tab(CSS media query)。
- 详情页用 `html-to-image` 导出 PNG。
- 构建输出到 `internal/server/web/`，由 Go `embed`。

## 8. 部署形态

单二进制 `rocom-capture`：
- 实时：`sudo ./rocom-capture -iface <网卡> -addr :4939`(需 root，网卡须为客户端设备流量必经)
- 离线：`./rocom-capture -pcap <文件> -addr :4939`

数据库默认 `rocom.db`(SQLite 文件)。详见 [README](../README.md)。
