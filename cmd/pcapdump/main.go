// pcapdump 回放 pcap 并把解密后的应用层消息以「适合 AI 分析」的结构化文本输出,
// 免去每次为调试新协议从头写一次性程序。三种模式:
//
//	概览(默认):各 opcode 的出现次数/方向/名称,按次数倒序。
//	  go run ./cmd/pcapdump -pcap x.pcap00
//	消息转储(-op):打印匹配 opcode 的每条消息头 + 通用 protobuf 解码树(+可选 hex)。
//	  go run ./cmd/pcapdump -pcap x.pcap00 -op 0x1888,FREE
//	gid 扫描(-gid):某宠物 gid 出现在哪些 opcode 里(定位某编号的数据来源)。
//	  go run ./cmd/pcapdump -pcap x.pcap00 -gid 20508,15895
//
// opcode 过滤项可写十六进制(0x1888)、十进制(6280)或名称子串(大小写不敏感,如 FREE/BOX_CHANGE)。
// protobuf 解码为「通用 wire 级」:不依赖 .proto 定义(规避版本错位),自动跳过 c2s 子头、
// 在校验和/tsf4g 尾处停止,并把剩余字节作为 trailer 展示。
package main

import (
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/whoisnian/rocom-capture/internal/capture"
	"github.com/whoisnian/rocom-capture/internal/gamedata"
)

func main() {
	pcapPath := flag.String("pcap", "", "pcap 文件路径(必填)")
	opFilter := flag.String("op", "", "只转储这些 opcode(逗号分隔;支持 0x1888 / 6280 / 名称子串 FREE)")
	gidScan := flag.String("gid", "", "扫描这些宠物 gid 出现在哪些 opcode(逗号分隔)")
	showHex := flag.Bool("hex", false, "转储模式下附带 AppBody 十六进制")
	noProto := flag.Bool("no-proto", false, "转储模式下不做 protobuf 解码")
	limit := flag.Int("limit", 20, "转储模式下每个 opcode 最多打印多少条")
	maxBody := flag.Int("maxbody", 4096, "单条消息解码/转储的最大字节数(超出截断,避免登录包刷屏)")
	port := flag.Int("port", 8195, "游戏服务器端口")
	flag.Parse()

	if *pcapPath == "" {
		flag.Usage()
		os.Exit(2)
	}
	db, err := gamedata.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "加载名称库失败:", err)
		os.Exit(1)
	}
	names := db.OpcodeNames()
	nameOf := func(op uint16) string {
		if n, ok := names[op]; ok {
			return n
		}
		return "?"
	}

	switch {
	case *gidScan != "":
		runGidScan(*pcapPath, *port, parseGids(*gidScan), nameOf)
	case *opFilter != "":
		runDump(*pcapPath, *port, parseOpFilter(*opFilter, names), nameOf, *showHex, !*noProto, *limit, *maxBody)
	default:
		runSummary(*pcapPath, *port, nameOf)
	}
}

// ---- 模式实现 ----

type opStat struct {
	op       uint16
	count    int
	c2s, s2c int
	minLen   int
	maxLen   int
}

func runSummary(path string, port int, nameOf func(uint16) string) {
	stats := map[uint16]*opStat{}
	total := 0
	for m := range replay(path, port) {
		total++
		s := stats[m.Opcode]
		if s == nil {
			s = &opStat{op: m.Opcode, minLen: len(m.AppBody), maxLen: len(m.AppBody)}
			stats[m.Opcode] = s
		}
		s.count++
		if len(m.AppBody) < s.minLen {
			s.minLen = len(m.AppBody)
		}
		if len(m.AppBody) > s.maxLen {
			s.maxLen = len(m.AppBody)
		}
		if m.Direction.String() == "c2s" {
			s.c2s++
		} else {
			s.s2c++
		}
	}
	list := make([]*opStat, 0, len(stats))
	for _, s := range stats {
		list = append(list, s)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].count != list[j].count {
			return list[i].count > list[j].count
		}
		return list[i].op < list[j].op
	})
	fmt.Printf("# 概览 %s — 共 %d 条消息, %d 种 opcode\n\n", path, total, len(list))
	fmt.Printf("%-8s %6s  %-5s %-5s %-13s  %s\n", "opcode", "count", "c2s", "s2c", "len(min..max)", "name")
	for _, s := range list {
		fmt.Printf("0x%04x   %6d  %-5d %-5d %-13s  %s\n",
			s.op, s.count, s.c2s, s.s2c, fmt.Sprintf("%d..%d", s.minLen, s.maxLen), nameOf(s.op))
	}
}

func runDump(path string, port int, ops map[uint16]bool, nameOf func(uint16) string, showHex, doProto bool, limit, maxBody int) {
	if len(ops) == 0 {
		fmt.Fprintln(os.Stderr, "未匹配到任何 opcode")
		os.Exit(1)
	}
	fmt.Printf("# 转储 %s — opcode:", path)
	for op := range ops {
		fmt.Printf(" 0x%04x", op)
	}
	fmt.Println()
	seen := map[uint16]int{}
	for m := range replay(path, port) {
		if !ops[m.Opcode] {
			continue
		}
		if seen[m.Opcode] >= limit {
			continue
		}
		seen[m.Opcode]++
		body := m.AppBody
		trunc := false
		if len(body) > maxBody {
			body = body[:maxBody]
			trunc = true
		}
		fmt.Printf("\n== op=0x%04x %s [%s] t=%s len=%d#%d %s\n",
			m.Opcode, nameOf(m.Opcode), m.Direction.String(), m.Time.Format("15:04:05"),
			len(m.AppBody), seen[m.Opcode], ifStr(trunc, "(截断)", ""))
		if showHex {
			fmt.Print(indentBlock(hex.Dump(body), "  "))
		}
		if doProto {
			start, fields, consumed := decodeAuto(body)
			fmt.Printf("  proto (起始偏移 %d, 已解码 %d/%dB):\n", start, consumed, len(body))
			var sb strings.Builder
			renderFields(fields, 2, &sb)
			fmt.Print(sb.String())
			if tr := body[start+consumed:]; len(tr) > 0 {
				fmt.Printf("  trailer(%dB): %s\n", len(tr), hexPreview(tr, 48))
			}
		}
	}
}

func runGidScan(path string, port int, gids []uint64, nameOf func(uint16) string) {
	// gid -> opcode -> 计数; 另记方向
	hit := map[uint64]map[uint16]int{}
	for _, g := range gids {
		hit[g] = map[uint16]int{}
	}
	for m := range replay(path, port) {
		for _, g := range gids {
			pat := uvarint(g)
			if len(pat) >= 2 && contains(m.AppBody, pat) {
				hit[g][m.Opcode]++
			}
		}
	}
	fmt.Printf("# gid 扫描 %s\n", path)
	for _, g := range gids {
		fmt.Printf("\n## gid %d (varint %s)\n", g, hex.EncodeToString(uvarint(g)))
		ops := hit[g]
		if len(ops) == 0 {
			fmt.Println("  (未出现在任何消息)")
			continue
		}
		keys := make([]uint16, 0, len(ops))
		for op := range ops {
			keys = append(keys, op)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		for _, op := range keys {
			fmt.Printf("  0x%04x x%-3d %s\n", op, ops[op], nameOf(op))
		}
	}
	fmt.Println("\n注:为子串匹配,极少数情况下可能命中非 gid 字节,请结合 -op 转储核对。")
}

// ---- 通用 protobuf wire 解码 ----

type wireField struct {
	num  int
	wire int
	v    uint64 // varint / fixed
	data []byte // len-delimited 原始字节
}

// scanProto 从 b[0] 顺序解析字段,遇非法 wire 或截断即停,返回字段与已消费字节数。
func scanProto(b []byte) ([]wireField, int) {
	var out []wireField
	i := 0
	for i < len(b) {
		key, n := binary.Uvarint(b[i:])
		if n <= 0 {
			break
		}
		num, wire := int(key>>3), int(key&7)
		if num == 0 {
			break
		}
		j := i + n
		switch wire {
		case 0:
			v, m := binary.Uvarint(b[j:])
			if m <= 0 {
				return out, i
			}
			out = append(out, wireField{num, wire, v, nil})
			j += m
		case 1:
			if j+8 > len(b) {
				return out, i
			}
			out = append(out, wireField{num, wire, binary.LittleEndian.Uint64(b[j:]), nil})
			j += 8
		case 2:
			ln, m := binary.Uvarint(b[j:])
			if m <= 0 || j+m+int(ln) > len(b) {
				return out, i
			}
			j += m
			out = append(out, wireField{num, wire, 0, b[j : j+int(ln)]})
			j += int(ln)
		case 5:
			if j+4 > len(b) {
				return out, i
			}
			out = append(out, wireField{num, wire, uint64(binary.LittleEndian.Uint32(b[j:])), nil})
			j += 4
		default: // 3/4(group)/7 视为非 protobuf 边界
			return out, i
		}
		i = j
	}
	return out, i
}

// decodeAuto 在前 16 字节内寻找最佳起始偏移(跳过 c2s 子头),返回偏移/字段/消费字节数。
// 评分基于「解码质量」:真实起始解出的都是干净字段(标量、子消息、可见字符串),
// 而错位起始往往把一长段当作无法再解的 bytes blob —— 据此打分,blob 重罚,
// 平手时取「终点更接近 tsf4g 尾、字段更多」者。这比单纯比消费字节数稳健得多。
func decodeAuto(b []byte) (int, []wireField, int) {
	limit := min(16, len(b))
	tail := indexOf(b, []byte("tsf4g"))
	bestStart, bestConsumed := 0, 0
	var bestFields []wireField
	bestQ, bestEnd, bestNF := -1<<30, -1, -1
	for s := 0; s <= limit; s++ {
		f, c := scanProto(b[s:])
		if len(f) == 0 {
			continue
		}
		q := quality(f)
		end := s + c
		nearTail := tail >= 0 && end <= tail // 不越过尾标记者更可信
		better := q > bestQ ||
			(q == bestQ && nearTail && end > bestEnd) ||
			(q == bestQ && end == bestEnd && len(f) > bestNF)
		if better {
			bestStart, bestConsumed, bestFields, bestQ, bestEnd, bestNF = s, c, f, q, end, len(f)
		}
	}
	return bestStart, bestFields, bestConsumed
}

// quality 统计干净字段数减去 blob(无法解为子消息又不可见的 bytes)罚分。
func quality(fields []wireField) int {
	clean, blob := 0, 0
	for _, f := range fields {
		if f.wire != 2 {
			clean++
			continue
		}
		if sub, c := scanProto(f.data); c == len(f.data) && len(sub) > 0 {
			clean++
		} else if printable(f.data) {
			clean++
		} else {
			blob++
		}
	}
	return clean - 2*blob
}

// renderFields 把字段渲染成缩进树;len 字段优先尝试嵌套消息,其次可见字符串,否则 hex。
func renderFields(fields []wireField, depth int, sb *strings.Builder) {
	ind := strings.Repeat("  ", depth)
	for _, f := range fields {
		switch f.wire {
		case 0:
			fmt.Fprintf(sb, "%s#%d: %d\n", ind, f.num, f.v)
		case 1:
			fmt.Fprintf(sb, "%s#%d: 0x%016x (64bit)\n", ind, f.num, f.v)
		case 5:
			fmt.Fprintf(sb, "%s#%d: 0x%08x (32bit)\n", ind, f.num, uint32(f.v))
		case 2:
			if sub, c := scanProto(f.data); c == len(f.data) && len(sub) > 0 {
				fmt.Fprintf(sb, "%s#%d: {  (msg %dB)\n", ind, f.num, len(f.data))
				renderFields(sub, depth+1, sb)
				fmt.Fprintf(sb, "%s}\n", ind)
			} else if printable(f.data) {
				fmt.Fprintf(sb, "%s#%d: %q\n", ind, f.num, string(f.data))
			} else {
				fmt.Fprintf(sb, "%s#%d: %s (%dB)\n", ind, f.num, hexPreview(f.data, 48), len(f.data))
			}
		}
	}
}

// ---- 工具 ----

func replay(path string, port int) <-chan capture.Message {
	eng := capture.NewEngine(port)
	go func() {
		if err := eng.RunOffline(path); err != nil {
			fmt.Fprintln(os.Stderr, "回放失败:", err)
		}
	}()
	return eng.Out
}

func parseOpFilter(s string, names map[uint16]string) map[uint16]bool {
	out := map[uint16]bool{}
	for _, tok := range strings.Split(s, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		// 十六进制 / 十进制
		if v, err := strconv.ParseUint(strings.TrimPrefix(tok, "0x"), pick(strings.HasPrefix(tok, "0x"), 16, 10), 16); err == nil {
			out[uint16(v)] = true
			continue
		}
		// 名称子串
		up := strings.ToUpper(tok)
		for op, n := range names {
			if strings.Contains(strings.ToUpper(n), up) {
				out[op] = true
			}
		}
	}
	return out
}

func parseGids(s string) []uint64 {
	var out []uint64
	for _, tok := range strings.Split(s, ",") {
		if v, err := strconv.ParseUint(strings.TrimSpace(tok), 10, 64); err == nil {
			out = append(out, v)
		}
	}
	return out
}

func uvarint(v uint64) []byte {
	var b []byte
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func contains(b, sub []byte) bool { return indexOf(b, sub) >= 0 }

func indexOf(b, sub []byte) int {
	if len(sub) == 0 || len(sub) > len(b) {
		return -1
	}
	for i := 0; i+len(sub) <= len(b); i++ {
		if string(b[i:i+len(sub)]) == string(sub) {
			return i
		}
	}
	return -1
}

func printable(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

func hexPreview(b []byte, max int) string {
	s := hex.EncodeToString(b)
	if len(s) > max {
		return "0x" + s[:max] + "…"
	}
	return "0x" + s
}

func indentBlock(s, ind string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := range lines {
		lines[i] = ind + lines[i]
	}
	return strings.Join(lines, "\n") + "\n"
}

func ifStr(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

func pick(cond bool, a, b int) int {
	if cond {
		return a
	}
	return b
}
