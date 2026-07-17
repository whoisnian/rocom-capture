// Package scene 解析场景移动与场景切换消息,供实时地图页跟踪登录账号自己的位置。
// 只解析「自己」:移动包是 c2s(0x0133),当前场景 res 从 s2c 的进入/传送通知跟踪。
// 不解析其他玩家/AOI。字段语义经当前版客户端 Scene luac 坐实,并已用真实 pcap 验证
// (卡洛西亚大陆→魔法学院,24/24 移动包解析成功,投影落点正确;见 docs/data.md 3.1、protocol.md)。
package scene

import (
	"bytes"

	"google.golang.org/protobuf/encoding/protowire"
)

// tsf4gMark 是应用层 protobuf body 之后的 tsf4g 尾标记;解码在其前停止(见 docs/protocol.md)。
var tsf4gMark = []byte("tsf4g")

// 场景相关 opcode(来自 ZoneSvrCmd,见 names.json opcodes,源自游戏描述符 all.pb)。
const (
	OpSceneMoveReq   = 0x0133 // ZONE_SCENE_MOVE_REQ(307), c2s,自己移动(to_pos/speed/scene_cfg_id)
	OpEnterSceneRsp  = 0x0152 // ZONE_ENTER_SCENE_RSP(338), s2c,进入场景(scene_cfg_id/scene_res_cfg_id)
	OpTeleportNotify = 0x015c // ZONE_SCENE_TELEPORT_NOTIFY(348), s2c,传送(to_scene_cfg_id/to_scene_res_cfg_id)
	OpPlayActsNotify = 0x0414 // ZONE_SCENE_PLAY_ACTS_NOTIFY(1044), s2c,区域进/出等动作(见 ParseAreaActs)
)

// Position 是场景世界坐标(UE 单位,1=1 厘米;玩家 z 为脚底高度,角色中心+85)。
type Position struct {
	X, Y, Z int32
}

// Seg 是 move_seg_list(field 12,MoveSegmentInfo{pos,time_stamp})里的一个路径点:客户端把
// **两次上报之间实际走过的轨迹**按约 0.3s 一个点补报上来,末点位置≈to_pos、时刻≈包时刻。
//
// 上报是按操作事件触发的:持续改方向/变速时约 0.1s 一包(Segs 基本为空),而推住摇杆不动、
// 直线巡航或坐骑自行盘旋时输入不变,就退化成约 2.5-3s 一次心跳——**那几秒里实际走的路(哪怕是
// 一个大转弯)只能靠 Segs 事后补报**。地面与飞行同理。前端据此把箭头沿真实曲线滑回正轨,
// 而不是直线跳过去(见 docs/architecture.md 7)。
type Seg struct {
	Pos       Position
	TimeStamp uint64 // 服务器时钟(毫秒)
}

// MoveReq 是一条自己移动上报(ZoneSceneMoveReq)里做地图定位所需的字段。
type MoveReq struct {
	Pos        Position // to_pos(field 2)
	Speed      Position // speed(field 4),速度向量(UE 单位/秒);停下时为零。见 ParseMoveReq 说明
	Yaw        int32    // to_rot.z(field 3 的 z),朝向角×10(0.1 度单位);朝向角(度)=Yaw/10
	MoveMode   int32    // move_mode(field 6),SceneMoveType 枚举(1/2/3 地面,6-9 飞行…)
	SceneCfgID int32    // scene_cfg_id(field 17);一个 cfg 可对应多个 res,故仅作校验,定位用当前 res
	StopMove   bool     // stop_move(field 8),停下时上报
	TimeStamp  uint64   // time_stamp(field 1),服务器时钟(毫秒)
	Segs       []Seg    // move_seg_list(field 12),本次上报覆盖时段内的真实轨迹点
}

// SegSpan 是本包携带的轨迹点覆盖的时长(秒),即「客户端上次上报到本次沉默了多久」。
// 密集上报(持续操作,约 0.1s 一包)时为 0 或很短,此时轨迹点没有意义;沉默较久(直线巡航/
// 推住摇杆盘旋的心跳,2.5-3s)时才是那段空窗里唯一的真实轨迹。调用方据此决定是否回放它。
func (mr MoveReq) SegSpan() float64 {
	if len(mr.Segs) < 2 {
		return 0
	}
	return float64(mr.Segs[len(mr.Segs)-1].TimeStamp-mr.Segs[0].TimeStamp) / 1000
}

// ParseMoveReq 从 c2s 移动包的 AppBody 解析 MoveReq。
//
// 实测(见 docs/protocol.md):c2s AppBody 结构为 6 字节子头 + protobuf + 变长 trailer + tsf4g 尾。
// 子头前 2 字节随包变化、余 4 字节为 0;protobuf 之后、tsf4g 之前还有一段变长 trailer(路由/校验)。
// 故不能要求「消费到 tsf4g」,而是贪婪解析已知字段、遇到非该消息的字段(即 trailer 起始)即停。
//
// 起点仍用扫描定位(子头虽实测恒为 6,仍容错):对每个候选起点贪婪解析,取「消费字节最多且解出
// field 2(Position)」者。错位起点会因字段号越界/wire type 不符而很快停下,消费远少于真起点。
func ParseMoveReq(appBody []byte) (MoveReq, bool) {
	body := appBody
	if i := bytes.Index(body, tsf4gMark); i >= 0 {
		body = body[:i] // tsf4g 之前即上限(trailer 仍在其内,靠贪婪解析在 protobuf 末尾停下)
	}
	var best MoveReq
	bestConsumed := -1
	for start := 0; start <= len(body) && start <= 16; start++ {
		mr, consumed, ok := decodeMoveReq(body[start:])
		if ok && consumed > bestConsumed {
			best, bestConsumed = mr, consumed
		}
	}
	return best, bestConsumed >= 0
}

// moveReqWire 是 ZoneSceneMoveReq 各字段的期望 wire type(据 all.pb 消息定义)。
// 用于锚定 protobuf 起点:错位起点几乎必然在某字段号上撞出不符的 wire type 而被否决。
var moveReqWire = map[protowire.Number]protowire.Type{
	1:  protowire.VarintType, // time_stamp
	2:  protowire.BytesType,  // to_pos
	3:  protowire.BytesType,  // to_rot
	4:  protowire.BytesType,  // speed
	5:  protowire.BytesType,  // acceleration
	6:  protowire.VarintType, // move_mode
	7:  protowire.VarintType, // custom_mode
	8:  protowire.VarintType, // stop_move
	12: protowire.BytesType,  // move_seg_list
	14: protowire.VarintType, // platform_actor_id
	15: protowire.BytesType,  // ctrl_rot
	17: protowire.VarintType, // scene_cfg_id
	18: protowire.VarintType, // ride_move
	19: protowire.BytesType,  // mate_point
	20: protowire.VarintType, // mate_move_mode
}

// decodeMoveReq 从 b 起贪婪解析 ZoneSceneMoveReq 字段,遇到不属于该消息的字段号或 wire type
// 不符即停(此处即 protobuf 末尾/trailer 起始)。返回解析结果、已消费字节数、以及是否解出
// field 2(to_pos,合法 Position)。consumed 供 ParseMoveReq 在多个候选起点间择优。
func decodeMoveReq(b []byte) (mr MoveReq, consumed int, ok bool) {
	var gotPos bool
	rest := b
loop:
	for len(rest) > 0 {
		num, typ, n := protowire.ConsumeTag(rest)
		if n < 0 {
			break // tag 解不出:到达 trailer
		}
		want, known := moveReqWire[num]
		if !known || want != typ {
			break // 字段号越界或 wire type 不符:到达 trailer
		}
		next := rest[n:]
		switch num {
		case 1:
			v, m := protowire.ConsumeVarint(next)
			if m < 0 {
				return mr, consumed, gotPos
			}
			mr.TimeStamp = v
			next = next[m:]
		case 2:
			sub, m := protowire.ConsumeBytes(next)
			if m < 0 {
				return mr, consumed, gotPos
			}
			p, pok := parsePosition(sub)
			if !pok {
				break loop // field 2 不是 Position:起点错位,停止(交由更优起点)
			}
			mr.Pos, gotPos = p, true
			next = next[m:]
		case 3: // to_rot(旋转,Position 形状:x=Roll y=Pitch z=Yaw);只取 z=朝向角×10
			sub, m := protowire.ConsumeBytes(next)
			if m < 0 {
				return mr, consumed, gotPos
			}
			if p, ok := parsePosition(sub); ok {
				mr.Yaw = p.Z
			}
			next = next[m:]
		case 4: // speed(速度向量,Position 形状);移动中非零,停下为零/缺省
			sub, m := protowire.ConsumeBytes(next)
			if m < 0 {
				return mr, consumed, gotPos
			}
			if p, ok := parsePosition(sub); ok {
				mr.Speed = p
			}
			next = next[m:]
		case 6:
			v, m := protowire.ConsumeVarint(next)
			if m < 0 {
				return mr, consumed, gotPos
			}
			mr.MoveMode = int32(v)
			next = next[m:]
		case 8:
			v, m := protowire.ConsumeVarint(next)
			if m < 0 {
				return mr, consumed, gotPos
			}
			mr.StopMove = v != 0
			next = next[m:]
		case 12: // move_seg_list(repeated MoveSegmentInfo);解不动的点跳过,不影响其余字段
			sub, m := protowire.ConsumeBytes(next)
			if m < 0 {
				return mr, consumed, gotPos
			}
			if s, ok := parseSeg(sub); ok {
				mr.Segs = append(mr.Segs, s)
			}
			next = next[m:]
		case 17:
			v, m := protowire.ConsumeVarint(next)
			if m < 0 {
				return mr, consumed, gotPos
			}
			mr.SceneCfgID = int32(v)
			next = next[m:]
		default:
			m := protowire.ConsumeFieldValue(num, typ, next)
			if m < 0 {
				return mr, consumed, gotPos
			}
			next = next[m:]
		}
		consumed += len(rest) - len(next)
		rest = next
	}
	return mr, consumed, gotPos
}

// parseSeg 把 MoveSegmentInfo 子消息解为 Seg(pos=field 1 的 Position,time_stamp=field 2)。
// 缺 pos 即判为不是路径点(起点错位时的误命中),返回 false。
func parseSeg(b []byte) (Seg, bool) {
	var s Seg
	var got bool
	rest := b
	for len(rest) > 0 {
		num, typ, n := protowire.ConsumeTag(rest)
		if n < 0 {
			return s, false
		}
		rest = rest[n:]
		switch {
		case num == 1 && typ == protowire.BytesType:
			sub, m := protowire.ConsumeBytes(rest)
			if m < 0 {
				return s, false
			}
			p, ok := parsePosition(sub)
			if !ok {
				return s, false
			}
			s.Pos, got = p, true
			rest = rest[m:]
		case num == 2 && typ == protowire.VarintType:
			v, m := protowire.ConsumeVarint(rest)
			if m < 0 {
				return s, false
			}
			s.TimeStamp = v
			rest = rest[m:]
		default:
			m := protowire.ConsumeFieldValue(num, typ, rest)
			if m < 0 {
				return s, false
			}
			rest = rest[m:]
		}
	}
	return s, got
}

// parsePosition 把子消息字节解为 Position。Position 仅含 x/y/z(field 1-3,int32/varint),
// 出现其他字段号或非 varint 即判为「不是 Position」,用于锚定移动包起点。
func parsePosition(b []byte) (Position, bool) {
	var p Position
	rest := b
	for len(rest) > 0 {
		num, typ, n := protowire.ConsumeTag(rest)
		if n < 0 || typ != protowire.VarintType || num < 1 || num > 3 {
			return Position{}, false
		}
		rest = rest[n:]
		v, m := protowire.ConsumeVarint(rest)
		if m < 0 {
			return Position{}, false
		}
		switch num {
		case 1:
			p.X = int32(v)
		case 2:
			p.Y = int32(v)
		case 3:
			p.Z = int32(v)
		}
		rest = rest[m:]
	}
	return p, true
}

// ParseEnterScene 从 s2c ZoneEnterSceneRsp 的 protobuf body 取 scene_cfg_id(field 2)、
// scene_res_cfg_id(field 3)与 home_room_level(field 5,家园室内底图按此分层选图)。
// ret_info(field 1)非零表示失败,则返回 ok=false。
func ParseEnterScene(body []byte) (cfgID, resID, room int32, ok bool) {
	fail := false
	scanFields(body, func(num protowire.Number, typ protowire.Type, val []byte, v uint64) {
		switch {
		case num == 1 && typ == protowire.BytesType:
			if retFailed(val) {
				fail = true
			}
		case num == 2 && typ == protowire.VarintType:
			cfgID = int32(v)
		case num == 3 && typ == protowire.VarintType:
			resID = int32(v)
		case num == 5 && typ == protowire.VarintType:
			room = int32(v)
		}
	})
	return cfgID, resID, room, !fail && resID != 0
}

// AreaAct 是一次区域进/出事件(玩家自己踩进/离开某个区域触发体)。
type AreaAct struct {
	AreaID uint32 // entered_area_id / left_area_id(AREA_CONF.id)
	FuncID uint32 // area_func_conf_id(AREA_FUNC_CONF.id);分层地图据此选层
	Enter  bool   // true=进入,false=离开
}

// ParseAreaActs 从 s2c ZoneScenePlayActsNotify(0x0414)取区域进/出事件:
// acts(field 1,SpaceActionCollection)里的 enterted_catcher(61,注意游戏里就是这个拼写)与
// left_catcher(62),各为 {actor_id(1), area_id(2), area_func_conf_id(3)}。
//
// **这是「玩家当前在哪一层」的权威依据**:服务器只在玩家真正进入区域触发体(3D 体积)时才下发,
// 客户端也正是据此选层(AreaAndZoneModule 维护 zone 集合 → BigMapModuleData:GetCurMapLayerId
// 取其中命中分层表的 area_func_id)。用位置点对区域多边形做 2D 判定则会在洞穴正上方的地表误命中
// ——多边形只有 x/y,分不清人在洞里还是在洞顶。见 docs/data.md 3.2。
func ParseAreaActs(body []byte) []AreaAct {
	var acts []AreaAct
	scanFields(body, func(num protowire.Number, typ protowire.Type, val []byte, _ uint64) {
		if num != 1 || typ != protowire.BytesType { // acts
			return
		}
		scanFields(val, func(n2 protowire.Number, t2 protowire.Type, sub []byte, _ uint64) {
			if t2 != protowire.BytesType || (n2 != 61 && n2 != 62) {
				return
			}
			if a, ok := parseCatcher(sub); ok {
				a.Enter = n2 == 61
				acts = append(acts, a)
			}
		})
	})
	return acts
}

// parseCatcher 解 SpaceAct_Entered/LeftCatcher{actor_id(1), area_id(2), area_func_conf_id(3), ...}。
// 服务器只对玩家自己下发(客户端同样不校验 actor_id),故不按 actor 过滤。
func parseCatcher(b []byte) (AreaAct, bool) {
	var a AreaAct
	scanFields(b, func(num protowire.Number, typ protowire.Type, _ []byte, v uint64) {
		if typ != protowire.VarintType {
			return
		}
		switch num {
		case 2:
			a.AreaID = uint32(v)
		case 3:
			a.FuncID = uint32(v)
		}
	})
	return a, a.AreaID != 0 && a.FuncID != 0
}

// Teleport 是一次传送通知(ZoneSceneTeleportNotify)里做地图定位所需的字段。
type Teleport struct {
	CfgID int32    // to_scene_cfg_id(field 11)
	ResID int32    // to_scene_res_cfg_id(field 12)
	Room  int32    // home_room_level(field 31),家园室内选分层底图
	Pos   Position // to_pt.pos(field 14 的 Point.pos):**落点**世界坐标
	Yaw   int32    // to_pt.dir.z:落点朝向角×10
}

// ParseTeleport 从 s2c ZoneSceneTeleportNotify 的 protobuf body 解析 Teleport。
//
// 落点(to_pt)在传送**刚下发时**就已知,而客户端要过几秒(加载)才落地并开始发移动包。据此可立刻把
// 地图切到目的地,不必干等第一个移动包——否则传送后地图会停在原地好几秒,甚至玩家落地不动就一直不更新。
// 实测(3 份 pcap)to_pt 与落地后首个移动包的坐标/朝向一致(误差几厘米)。
func ParseTeleport(body []byte) (Teleport, bool) {
	var t Teleport
	scanFields(body, func(num protowire.Number, typ protowire.Type, val []byte, v uint64) {
		switch {
		case num == 11 && typ == protowire.VarintType:
			t.CfgID = int32(v)
		case num == 12 && typ == protowire.VarintType:
			t.ResID = int32(v)
		case num == 31 && typ == protowire.VarintType:
			t.Room = int32(v)
		case num == 14 && typ == protowire.BytesType: // to_pt = Point{pos(1), dir(2)}
			scanFields(val, func(n2 protowire.Number, t2 protowire.Type, sub []byte, _ uint64) {
				if t2 != protowire.BytesType {
					return
				}
				p, ok := parsePosition(sub)
				if !ok {
					return
				}
				switch n2 {
				case 1:
					t.Pos = p
				case 2:
					t.Yaw = p.Z // dir 是旋转(FRotator×10),只有 z=Yaw 有意义
				}
			})
		}
	})
	return t, t.ResID != 0
}

// retFailed 判断 RetInfo 子消息是否表示失败(field 1 = ret_code,非 0 即失败)。
func retFailed(b []byte) bool {
	failed := false
	scanFields(b, func(num protowire.Number, typ protowire.Type, _ []byte, v uint64) {
		if num == 1 && typ == protowire.VarintType && v != 0 {
			failed = true
		}
	})
	return failed
}

// scanFields 遍历顶层 protobuf 字段;对 varint 传 v,对 bytes 传 val(子消息/字符串原始字节)。
// 解码出错即静默停止(容忍尾部 tsf4g 等非 protobuf 残留)。
func scanFields(b []byte, fn func(num protowire.Number, typ protowire.Type, val []byte, v uint64)) {
	rest := b
	for len(rest) > 0 {
		num, typ, n := protowire.ConsumeTag(rest)
		if n < 0 {
			return
		}
		rest = rest[n:]
		switch typ {
		case protowire.VarintType:
			v, m := protowire.ConsumeVarint(rest)
			if m < 0 {
				return
			}
			fn(num, typ, nil, v)
			rest = rest[m:]
		case protowire.BytesType:
			val, m := protowire.ConsumeBytes(rest)
			if m < 0 {
				return
			}
			fn(num, typ, val, 0)
			rest = rest[m:]
		default:
			m := protowire.ConsumeFieldValue(num, typ, rest)
			if m < 0 {
				return
			}
			rest = rest[m:]
		}
	}
}
