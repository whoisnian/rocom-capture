package pet

import (
	"encoding/hex"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

// 2026-09-13 pcap 里连续三次改同一只宠物(gid 34052)伙伴标记的 0x0403 回包原始 AppBody:
// 先移除(PPMT_NONE)、再加回(PPMT_HOMELAND=4)、最后换样式(PPMT_DEFENCE=8)。
// 尾部 32 字节起是 AES 填充与 tsf4g 残留,解析须照常容忍。
var collectTagRspHex = []string{
	`0a1f0800221b0a1108271002180028007084 8a02 78d4b9b729 10c4de16 18b9be34` +
		`672b3d7d899c05 3425bf85383f6bea 74736634 6715`,
	`0a1f0800221b0a1108271002180428007084 8a02 78bee7b729 10c4de16 18b9be34` +
		`3bea9b572e54eaf9 95 8973 dad1 3d0c 74736634 6715`,
	`0a1f0800221b0a1108271002180828007084 8a02 78b799b829 10c4de16 18b9be34` +
		`bb6f4cca9613f246 a170cb3cec5815 74736634 6715`,
}

func TestParseCollectTagRsp(t *testing.T) {
	want := []int32{0, 4, 8} // 移除 / 家园标记 / 防守标记
	for i, h := range collectTagRspHex {
		body, err := hex.DecodeString(strings.ReplaceAll(h, " ", ""))
		if err != nil {
			t.Fatalf("第 %d 条 hex 无效: %v", i+1, err)
		}
		gid, mark, ok := ParseCollectTagRsp(body)
		if !ok {
			t.Fatalf("第 %d 条未解出伙伴标记变更", i+1)
		}
		if gid != 34052 || mark != want[i] {
			t.Errorf("第 %d 条 = (gid %d, mark %d), 期望 (34052, %d)", i+1, gid, mark, want[i])
		}
	}
}

// 不相关的回包(无 GT_PET_MARK 条目)不得误报,否则会把别的宠物标记改掉。
func TestParseCollectTagRspIgnoresOthers(t *testing.T) {
	if _, _, ok := ParseCollectTagRsp([]byte{0x0a, 0x02, 0x08, 0x00}); ok { // 只有 ret_code=0
		t.Error("空 goods_change_info 不应解出标记变更")
	}
	if _, _, ok := ParseCollectTagRsp(nil); ok {
		t.Error("空 body 不应解出标记变更")
	}
}

// 新获得宠物的 PetData 不带 name——2026-11-07 五份抓包实测获得通道(0x0243/0x030c/0x132c/0x0160)
// 与分页全量 0x1346 里都是 0 字节,只有老宠物保留 name,故靠 gid/conf_id/add_time 认宠物;
// 只有 gid/conf_id(或带名字却没有 add_time)的普通子消息不得误报,否则会往宠物库里塞假数据。
func TestFindNewPet(t *testing.T) {
	petData := func(gid, conf uint32, name string, addTime uint32) []byte {
		var b []byte
		b = protowire.AppendTag(b, 1, protowire.VarintType) // gid
		b = protowire.AppendVarint(b, uint64(gid))
		b = protowire.AppendTag(b, 2, protowire.VarintType) // conf_id
		b = protowire.AppendVarint(b, uint64(conf))
		b = protowire.AppendTag(b, 3, protowire.BytesType) // name
		b = protowire.AppendBytes(b, []byte(name))
		if addTime != 0 {
			b = protowire.AppendTag(b, 32, protowire.VarintType) // add_time
			b = protowire.AppendVarint(b, uint64(addTime))
		}
		// 包一层,让 Walk 把整段 PetData 当作一个 LEN 子字段访问(与实际嵌套一致)。
		out := protowire.AppendTag(nil, 1, protowire.BytesType)
		return protowire.AppendBytes(out, b)
	}

	if pd := FindNewPet(petData(52889, 410066, "", 1794031042)); pd == nil {
		t.Fatal("空 name + add_time 的新宠未识别(获得通知就是这种形态)")
	} else if pd.GetGid() != 52889 || pd.GetConfId() != 410066 {
		t.Errorf("解出 gid=%d conf_id=%d, 期望 52889/410066", pd.GetGid(), pd.GetConfId())
	}
	if pd := FindNewPet(petData(231, 1007, "罗隐", 1775238967)); pd == nil {
		t.Error("带 name 与 add_time 的老宠物未识别")
	}
	if pd := FindNewPet(petData(52889, 410066, "护主犬", 0)); pd != nil {
		t.Errorf("无 add_time 的子消息被误认成宠物: gid=%d", pd.GetGid())
	}
	if pd := FindNewPet(petData(52889, 410066, "", 0)); pd != nil {
		t.Errorf("只有 gid/conf_id 的子消息被误认成宠物: gid=%d", pd.GetGid())
	}
}
