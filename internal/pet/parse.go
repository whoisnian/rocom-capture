// Package pet 负责把宠物相关的应用层消息解析为业务模型，并检测宠物增减事件。
package pet

import (
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/whoisnian/rocom-capture/internal/pb"
)

// 宠物相关 opcode(来自 c2s_cmd.proto 的 ZoneSvrCmd enum)。
const (
	OpGetPetInfoByPageRsp = 0x1346 // ZONE_GET_PET_INFO_BY_PAGE_RSP(4934), 分页宠物列表
	OpPetFreeRsp          = 0x01c5 // ZONE_PET_FREE_RSP(453), 放生(下行含 pet_gid 列表)
)

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
