// Package pet 负责把宠物相关的应用层消息解析为业务模型，并检测宠物增减事件。
package pet

import (
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/whoisnian/rocom-capture/internal/pb"
)

// 宠物相关 opcode(来自 ZoneSvrCmd enum,见 names.json opcodes,源自 nrc/all.pb)。
const (
	OpGetPetInfoByPageRsp = 0x1346 // ZONE_GET_PET_INFO_BY_PAGE_RSP(4934), 分页宠物列表
	OpPetFreeRsp          = 0x01c5 // ZONE_PET_FREE_RSP(453), 放生(下行含 pet_gid 列表)
	OpCrackEggRsp         = 0x030c // ZONE_CRACK_EGG_RSP(780), 孵蛋(新宠物嵌在 goods_reward)
	OpPetCatchRsp         = 0x1983 // ZONE_SCENE_THROW_CATCH_FINISH_RSP(6531), 战斗外捕捉(赛季球/高级球)
	OpGoodsRewardNotify   = 0x0243 // ZONE_GOODS_REWARD_NOTIFY, 奖励通知(普通战斗内捕捉等新宠物)
	OpPlayerSyncNotify    = 0x0160 // ZONE_PLAYER_SYNC_NOTIFY, 玩家数据同步(花种战斗内捕捉走此通道)
)

// hasCJK 判断字节串是否含中日韩统一表意文字(宠物名为中文)。
func hasCJK(b []byte) bool {
	for _, r := range string(b) {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}

// FindNewPet 在响应 body 中递归查找新宠物 PetData。
// 孵蛋/捕捉获得的宠物作为奖励嵌套在 ret_info.goods_reward.rewards[].pet 里，
// 逐层路径随消息而异，这里递归尝试把每个 LEN 子字段反序列化为 PetData，
// 以 gid/conf_id/name 均有效作为命中判据。
func FindNewPet(body []byte) *pb.PetData {
	b := body
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			break
		}
		b = b[n:]
		if typ == protowire.BytesType {
			v, m := protowire.ConsumeBytes(b)
			if m < 0 {
				break
			}
			var pd pb.PetData
			if proto.Unmarshal(v, &pd) == nil &&
				pd.GetGid() > 0 && pd.GetConfId() > 1000 && hasCJK(pd.GetName()) {
				return &pd
			}
			if r := FindNewPet(v); r != nil {
				return r
			}
			b = b[m:]
		} else {
			m := protowire.ConsumeFieldValue(num, typ, b)
			if m < 0 {
				break
			}
			b = b[m:]
		}
	}
	return nil
}

// ParseFreeRsp 解析 ZonePetFreeRsp(放生)的 body，返回被放生的 gid 列表。
// 消息结构: { RetInfo ret_info=1; repeated uint32 pet_gid=2; }
func ParseFreeRsp(body []byte) []uint32 {
	var gids []uint32
	b := body
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			break
		}
		b = b[n:]
		var m int
		if num == 2 && typ == protowire.VarintType { // 非 packed
			var v uint64
			v, m = protowire.ConsumeVarint(b)
			if m >= 0 {
				gids = append(gids, uint32(v))
			}
		} else if num == 2 && typ == protowire.BytesType { // packed repeated
			var v []byte
			v, m = protowire.ConsumeBytes(b)
			for len(v) > 0 {
				x, k := protowire.ConsumeVarint(v)
				if k < 0 {
					break
				}
				gids = append(gids, uint32(x))
				v = v[k:]
			}
		} else {
			m = protowire.ConsumeFieldValue(num, typ, b)
		}
		if m < 0 {
			break
		}
		b = b[m:]
	}
	return gids
}

// PageResult 是一页宠物列表的解析结果。
type PageResult struct {
	TotalPage uint32
	ReqPage   uint32
	PageNum   uint32
	Pets      []*pb.PetData
}

// ParsePetListRsp 解析 ZoneGetPetInfoByPageRsp(opcode 0x1346)的 protobuf body。
// 只取需要的字段：total_page=2, req_page=3, pet_info=4(PetDataInfoList), page_num=5。
// 对 wire 解析容错：遇到无法识别的尾部即停止，返回已解析出的内容。
func ParsePetListRsp(body []byte) *PageResult {
	res := &PageResult{}
	b := body
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			break
		}
		b = b[n:]

		var m int
		switch {
		case num == 2 && typ == protowire.VarintType:
			var v uint64
			v, m = protowire.ConsumeVarint(b)
			res.TotalPage = uint32(v)
		case num == 3 && typ == protowire.VarintType:
			var v uint64
			v, m = protowire.ConsumeVarint(b)
			res.ReqPage = uint32(v)
		case num == 5 && typ == protowire.VarintType:
			var v uint64
			v, m = protowire.ConsumeVarint(b)
			res.PageNum = uint32(v)
		case num == 4 && typ == protowire.BytesType:
			var v []byte
			v, m = protowire.ConsumeBytes(b)
			if m >= 0 {
				var list pb.PetDataInfoList
				if proto.Unmarshal(v, &list) == nil {
					res.Pets = append(res.Pets, list.PetData...)
				}
			}
		default:
			m = protowire.ConsumeFieldValue(num, typ, b)
		}
		if m < 0 {
			break
		}
		b = b[m:]
	}
	return res
}
