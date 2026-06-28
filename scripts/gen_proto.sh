#!/usr/bin/env bash
# 从游戏自带的 protobuf 描述符 all.pb 生成宠物相关 Go 结构体。
#
# all.pb 是用 FModel 从 Windows 客户端 Content/ScriptC/Data/PB/ 提取的
# google.protobuf.FileDescriptorSet(游戏运行时 pb.loadufsfile 加载的同一份),
# 直接含字段号/类型,无需 .proto 文本,也无需 fix_proto 修补。该描述符已随仓库提交在
# proto/all.pb(默认数据源);要更新到新版本游戏,用 FModel 重新提取 all.pb 覆盖 proto/all.pb
# 再跑本脚本即可(或设 NRC_PB_DIR 指向别处的 all.pb 所在目录)。
#
# 描述符里游戏文件无 go_package,用 M 映射把 com_pet.proto 的依赖闭包(下面动态求取)
# 全部指向同一 Go 包 internal/pb;well-known 的 descriptor.proto 映射到 descriptorpb。
set -euo pipefail

NRC_PB_DIR="${NRC_PB_DIR:-proto}"
PKG="github.com/whoisnian/rocom-capture/internal/pb"
OUT="internal/pb"
export PATH="$(go env GOPATH)/bin:$PATH"

# com_pet.proto 的依赖闭包由描述符动态求取(随 all.pb 版本而变,硬编码会失配),
# 不含 well-known 的 google/protobuf/descriptor.proto(下面单独拼接)。
mapfile -t FILES < <(
  protoc --decode=google.protobuf.FileDescriptorSet google/protobuf/descriptor.proto \
    -I/usr/include < "$NRC_PB_DIR/all.pb" 2>/dev/null \
  | python3 -c '
import sys, re
files, cur = {}, None
for line in sys.stdin:
    m = re.match(r"^  name: \"([^\"]+\.proto)\"$", line)
    if m: cur = m.group(1); files.setdefault(cur, []); continue
    d = re.match(r"^  dependency: \"([^\"]+)\"$", line)
    if d and cur: files[cur].append(d.group(1))
seen = []
def visit(f):
    if f in seen or f not in files: return
    seen.append(f)
    for dep in files[f]: visit(dep)
visit("com_pet.proto")
print("\n".join(seen))
'
)
[ "${#FILES[@]}" -gt 0 ] || { echo "无法从 $NRC_PB_DIR/all.pb 求取 com_pet.proto 闭包" >&2; exit 1; }
echo "闭包(${#FILES[@]} 文件): ${FILES[*]}"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# all.pb 不含 well-known 的 descriptor.proto(rpc_options.proto 依赖它),
# 单独导出其描述符并拼接(FileDescriptorSet 是 repeated,拼接即合并)。
protoc -I/usr/include --descriptor_set_out="$TMP/desc.pb" google/protobuf/descriptor.proto
cat "$TMP/desc.pb" "$NRC_PB_DIR/all.pb" > "$TMP/combined.pb"

MAPPINGS=()
for f in "${FILES[@]}"; do MAPPINGS+=("--go_opt=M${f}=${PKG}"); done
MAPPINGS+=("--go_opt=Mgoogle/protobuf/descriptor.proto=google.golang.org/protobuf/types/descriptorpb")

rm -f "$OUT"/*.pb.go
mkdir -p "$OUT"
protoc --descriptor_set_in="$TMP/combined.pb" \
  --go_out="$OUT" --go_opt=paths=source_relative \
  "${MAPPINGS[@]}" \
  "${FILES[@]}"

echo "生成完成:"
ls -la "$OUT"/*.pb.go
