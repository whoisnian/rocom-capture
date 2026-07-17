#!/usr/bin/env bash
#
# unpack.sh — 在 Linux 上从游戏 pak 批量解包(复刻 FModel 手动导出流程,见 docs/data.md)
#
# 封装 scripts/unpack/(C#,基于 ~/Git/gh/CUE4Parse 的 GAME_RocoKingdomWorld 支持):
# 输出布局与 FModel 完全一致(默认 ~/Downloads/NRC/Content/...),gen_gamedata/gen_images/
# gen_icons/gen_bigmap/dump_bin 等下游脚本零改动。并行解码,默认跳过已存在文件(增量)。
#
# 用法:
#   ./scripts/unpack.sh --paks <游戏Paks目录|.apk> --aes <64位hex|@密钥文件>
#   ./scripts/unpack.sh --paks ... --aes ... --list          # 只列清单不导出
#   ./scripts/unpack.sh --paks ... --aes ... --only bin,pb   # 只导名称表与描述符
#   其余参数(--out/-j/--pet1024/--raw/--force/...)见 --help,原样透传给 C# 工具。
#
# AES 主密钥默认用下方 DEFAULT_AES(与 Windows FModel AppSettings.json → AesKeys 同一把,
# 换密钥的版本传 --aes 覆盖)。
# 依赖:dotnet SDK 10+(pacman -S dotnet-sdk);CUE4Parse 仓库(默认 ~/Git/gh/CUE4Parse,
# 环境变量 CUE4PARSE_DIR 覆盖);首次运行会往 ~/.cache/nrc-unpack 下载 oodle/zlib-ng 原生库。
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJ="$SCRIPT_DIR/unpack"

command -v dotnet >/dev/null 2>&1 || {
    echo "错误: 未找到 dotnet,请先安装 .NET SDK 10+(Arch: pacman -S dotnet-sdk)" >&2
    exit 1
}

CUE4PARSE_DIR="${CUE4PARSE_DIR:-$HOME/Git/gh/CUE4Parse}"
[[ -f "$CUE4PARSE_DIR/CUE4Parse/CUE4Parse.csproj" ]] || {
    echo "错误: 未找到 CUE4Parse 仓库: $CUE4PARSE_DIR" >&2
    echo "  git clone https://github.com/FabianFG/CUE4Parse ~/Git/gh/CUE4Parse" >&2
    echo "  或设置环境变量 CUE4PARSE_DIR 指向已有克隆" >&2
    exit 1
}
export CUE4PARSE_DIR

# 未显式传 --aes/--aes-file 时,用内置默认密钥(2026-07 版实测)
DEFAULT_AES="0x34254D23E47299B3B7F6C4CFDE9BD0688703446D9D8F37B2EBDDDE5B06ED5ADF"
has_aes=0
for a in "$@"; do
    [[ "$a" == "--aes" || "$a" == "--aes-file" ]] && has_aes=1
done
extra=()
[[ $has_aes -eq 0 ]] && extra=(--aes "$DEFAULT_AES")

# -c Release:纹理解码是 CPU 密集,Debug 构建慢一倍以上
# 先静默构建(CUE4Parse 自身有几百个 CS86xx 警告,只留错误),再直接跑产物
dotnet build "$PROJ" -c Release --nologo -v q -clp:ErrorsOnly
exec dotnet "$PROJ/bin/Release/net10.0/NrcUnpack.dll" "${extra[@]}" "$@"
