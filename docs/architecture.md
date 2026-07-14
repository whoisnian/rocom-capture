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
| `gcp` | GCP 分帧(`Deframe`)、密钥提取(`ExtractKey`)、AES 解密(`DecryptData`)、明文自检(`ValidPlain`)、opcode 提取 |
| `capture` | 数据源(`afpacket` 实时 / `pcapgo` 离线)+ `reassembly` TCP 重组 + 会话密钥管理(可选 `KeyStore` 持久化，见 §3)，输出 `Message` |
| `pb` | 游戏描述符 `nrc/all.pb` 生成的宠物消息结构(生成物) |
| `pet` | `ParsePetListRsp` 解析宠物列表；`ToPet` 转中文化业务模型；`ParseLoginAccount` 取登录 user_id/昵称 |
| `gamedata` | embed 的 id→中文名 查找库 |
| `store` | SQLite 持久化,按 `account` 分区(宠物/盒队/奖牌/事件 + `accounts` 表)与多维筛选查询;`For(account)` 返回绑定账号的 `*Scoped` 视图;另存 `sessions` 表(连接会话密钥+账号归属,供重启续解,见 §3) |
| `server` | REST API、SSE 广播(`Hub`)、embed 前端静态资源 |

`cmd/rocom-capture/main.go` 组装上述模块并启动抓包与 HTTP。

## 3. 抓包与重组要点

- **方向判定**：`reassembly` 每个 TCP 连接只创建一个 `Stream`，双向数据经同一
  `ReassembledSG`，用 `sg.Info()` 的方向 + 触发包端口映射为 c2s/s2c。
- **flush 用抓包时钟(实时中段接入必需)**：`reassembly` 在中段接入(未见 SYN)时会把起始
  数据当作"等待更早分段"缓冲,须 flush 才下推。`process()` 每 `flushEvery` 包调一次
  `FlushWithOptions{T: lastTS-flushLag, TC: lastTS-closeIdle}`,阈值取**最新包时间戳**
  `lastTS` 而非墙钟——实时流里墙钟-2min 永远追不上活跃连接的数据时间,起始 backlog 会一直
  卡住直到 EOF 的 `FlushAll`(而 `Ctrl-C` 会跳过它),表现为"重启后能恢复密钥却收不到任何
  消息"。`T` 促使跨间隙滞留数据近实时下推,`TC` 只关闭真正空闲的连接、不误关活跃连接。
- **会话密钥共享**：c2s/s2c 两个半连接归一化为同一 `session`，ACK(下行)提取的密钥
  供同会话的 DATA 解密。
- **会话密钥持久化(重启续解)**：密钥仅在连接建立时的 `0x1002 ACK` 明文下发一次;抓包
  服务若在密钥协商之后才启动/重启,拿不到密钥则整条连接的 DATA 全被当无密钥丢弃。
  为此 `Engine.Keys`(可选 `KeyStore`,由 `store` 实现)把 `connID→密钥` 落库:连接首次
  出现时预热密钥、收到 ACK 时落盘;`consume` 同步持久化 `connID→account` 归属并在启动时
  预热。因 AES-CBC 每个 DATA 包自带 IV(或固定零 IV)、解密无跨包状态,只要密钥在手,重启后
  从流中段接上的 DATA 即可独立解密并归属。**防误用**:四元组被新连接复用时可能套到陈旧
  缓存密钥,故解密后用 `gcp.ValidPlain` 校验 s2c 明文固定标记 `0x55aa`,不符即丢弃(新连接
  的 ACK 会重下发正确密钥覆盖);缓存另设 `store.SessionTTL`(24h)兜底过期。
- **实时 vs 离线**：二者共用 `process()`；`afpacket` 无需 libpcap(纯 AF_PACKET)。

## 4. 事件判定

宠物列表全量下发时，库中不存在的 `gid` 即"新增"。为区分**初始仓库快照**与
**运行期新捕捉**，仅当 `PetData.add_time ≥ 服务启动时间`(留 120s 余量)才记为
"获得"事件并推送。离线回放历史 pcap 因 `add_time` 是过去时间，不会误报事件。

## 5. 多账号隔离

局域网内多台设备(手机/平板/PC)可同时在线,各账号数据在**同一 SQLite 库**内隔离
(单库加 `account` 列,而非分库)。

- **身份 = 玩家 `user_id`**(账号键 `"UID:"+user_id`),取自 `ZONE_LOGIN_RSP(0x0102)`:
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
- **归属持久化**:`connID→account` 映射随 `sessions` 表落库(见 §3),抓包服务重启后预热恢复,
  配合缓存的会话密钥即可对仍存活的连接续解并正确归属,无需再等下次登录。
- **已知限制**:首次仍必须抓到某连接的 `LOGIN_RSP` 才能建立归属(从未见过登录、且无缓存的
  连接,其消息直接丢弃,不回退 IP)。

## 6. HTTP 接口

| 方法 路径 | 说明 |
| --- | --- |
| `GET /api/pets` | 宠物列表(筛选/排序/分页)，参数：`account,search,types,nature,gender,talentRank,medal,speciality,partnerMark,shiny,levelMin,levelMax,sort,order,page,pageSize` |
| `GET /api/pets/{gid}` | 单只宠物详情(`?account=`) |
| `GET /api/events` | 事件历史(`account,limit,beforeId`);仅获得宠物事件,减少不入库 |
| `GET /api/events/count` | 事件总数 `{count}`(`?account=`),即自上次清空以来获得的宠物数 |
| `DELETE /api/events` | 清空本账号事件历史(计数随之归零) |
| `GET /api/accounts` | 已知账号列表(`account,name,petCount`),供账号切换下拉 |
| `GET /api/filter-options` | 各筛选维度的可选值(`?account=`) |
| `GET /api/stats` | 统计(当前账号宠物总数,`?account=`) |
| `GET /api/position` | 当前账号最近一次位置(地图页初始回显);超过 4s 未更新则抹掉速度(不给前端外推) |
| `GET /api/pois` | 某场景(`?res=<scene_res_cfg_id>`)的大地图 POI:图层清单 + 已投影为底图归一化 u/v 的标记点;眠枭之星另带收集状态与按区域进度(见 docs/data.md 3.3/3.4) |
| `GET /api/stream` | SSE，实时推送 `{type: pet\|event\|debug\|position\|stars\|starzones, account, data}` |
| `GET /*` | 前端 SPA(未匹配路径回退 index.html) |

> 除 `/api/accounts` 与静态数据(`/api/medals`、`/api/evolution`)外,读接口均按 `?account=`
> 收窄;缺省(不传)回退到最近活跃账号。

## 7. 前端(`web/`，React + Vite)

- 路由(HashRouter)：`/pets` 列表、`/pets/:gid` 详情、`/events` 捕获事件、`/map` 实时地图、`/debug` 调试。
- **账号切换**:顶栏下拉(`/api/accounts` 填充),`AccountContext` 下发当前账号;切换时经
  `<main key={account}>` 轻量重挂各页(不整页 reload),`api.js` 各请求自动带 `?account=`,
  各页对 SSE 按 `msg.account` 过滤(调试页不过滤)。
- 实时：`EventSource('/api/stream')` 订阅，列表防抖刷新、事件流追加、调试流追加、地图位置(`position`)更新。
- **实时地图的平滑移动**:移动包逐包推送(不节流,峰值约 8 条/秒),每包带位置 + 速度向量
  (`vu/vv`,归一化底图坐标/秒)。前端不「收到才画」——游戏直线段 3s 才发一包,那样箭头会定住再硬跳
  (见 [protocol.md](protocol.md) 6)。而是按 rAF 逐帧外推 `pos + v×Δt`(航位推算,同客户端给其他玩家
  的做法),新包到达时把落差按 `e^(-Δt/τ)`(τ=0.12s)平滑收敛而非硬跳;落差过大(>底图 0.5%)判为
  传送/换场景则直接跳过去;超过 3.5s 没有新包(抓包中断/掉线)就停住不再外推。位置与朝向逐帧直接写
  `transform`(不经 React state,避免 60fps 重渲染);跟随模式下视口中心同样逐帧跟着走。
- **心跳空窗里的真实轨迹回放**:输入不变时客户端只发 2.5–3s 一次心跳,而那几秒里玩家可能其实在
  转弯(推住摇杆盘旋、直线巡航微调),外推必然偏出去。这段路会随下一包的 `move_seg_list` 补报,
  后端把它投影成 `path`(末点补 `to_pos`)推给前端,箭头届时沿这条**真实曲线**在 0.45s 内滑到最新
  位置(而非直线跳过去)。
  - 只在 `SegSpan ≥ 0.6s`(客户端确实沉默过)时才带 `path`:密集上报(0.1s 一包)时轨迹点为空或
    极短,回放反而会让 0.45s 的滑行跨过好几个新包,箭头落后打颤。**判据是轨迹跨度,不是"是否飞行"**
    ——飞行快速打方向同样是 0.1s 一包(见 [protocol.md](protocol.md) 6)。
  - 空窗期**照常直线外推**:实测(以补报轨迹为真值)直线外推在「直线巡航」与「静默转弯」两种空窗里
    都是最准的,阻尼/圆弧/定住均更差。**静默转弯最多晚一个心跳(~3s)才可见,这是游戏上报节奏所限,
    任何画法都提前不了。**

  实测(直线飞行 + 传送 + 空中绕圈那份抓包,以补报轨迹为真值):离真实轨迹的偏差均值 16.2→15.0px
  (底图 4000px),每帧最大跳动 14.9px→0.8px,>10px 的硬跳帧 8→0。
- **地图平移量必须对齐设备像素**(`applyFrame` 的 `snap()`,箭头同):底图与洞穴层图是两个元素,
  浏览器绘制时各自把位置吸附到整像素;地图逐帧按小数像素平移的话,两者的吸附时机错开,看起来就是
  层图与底图**错位抖动**。把平移量钉在设备像素网格上,层图的小数偏移就成了常量,每帧吸附结果相同,
  相对位置锁死。**浏览器实测**(同一 DOM 结构的受控实验,逐帧截图量两图实际绘制偏移):Firefox 152
  小数平移抖 **1.00px**、对齐后 **0.00px**;Chromium 两者都是 0.00px(它把整个地图合成为一张纹理、
  平移是纯合成器操作、不重绘,故几乎看不出——但不能指望)。代价是地图以 1 设备像素为步进移动,
  跟随时地图本就只有几 px/s,肉眼无感。
  > 曾试过把底图与层图合并为同一元素的两层 CSS 背景,以为「一次绘制就不会不同步」——**实测无效**:
  > Firefox 对两层背景照样各自吸附整像素,仍抖 1.00px。故仍是两个 `<img>`,靠对齐设备像素解决。
- 响应式：桌面表格 / 移动卡片，桌面顶栏 / 移动底部 tab(CSS media query)。
- 详情页用 `html-to-image` 导出 PNG。
- 构建输出到 `internal/server/web/`，由 Go `embed`。

## 8. 部署形态

单二进制 `rocom-capture`：
- 实时：`sudo ./rocom-capture -iface <网卡> -addr :4939`(需 root，网卡须为客户端设备流量必经)
- 离线：`./rocom-capture -pcap <文件> -addr :4939`

数据库默认 `rocom.db`(SQLite 文件)。详见 [README](../README.md)。
