#!/usr/bin/env bash
#
# capture.sh — 在 Linux 网关上抓取手机游戏 tsf4g/GCP 流量(协议见 docs/protocol.md)
#
# GCP 是变长协议:每包 = HEAD.base(定长 21 字节,明文,magic 0x3366)+ HEAD.extend(变长)
# + BODY(变长),DATA 包的 BODY 为 AES 密文。整包长度 = hdr_len + body_len(都在 HEAD.base 里),
# 故抓包必须用全长 snaplen(-s 0),否则后续无法按长度正确分帧。
# 本脚本只负责把 8195/TCP 双向流量完整存成 pcap,解析交给后处理。
#
# 密钥不走 DH 交换:服务器在 0x1002 ACK 包里明文下发 16 字节 AES 会话密钥(每连接不同)。
# 因此务必【进游戏前先启动抓包】,抓到该连接的 ACK 才能解密其后的 DATA;中途开抓则无密钥。
#
# 用法:
#   sudo ./capture.sh                      # 自动选网卡,抓默认 8195 端口
#   sudo ./capture.sh -i wlan0 -p 8195     # 指定网卡和端口
#   sudo ./capture.sh -o /data/pcap        # 指定输出目录
#   sudo ./capture.sh --forward            # 抓包前开启 ip_forward(ARP 引流场景需要)
#
set -euo pipefail

# ---- 默认参数 ----
IFACE=""
PORT=8195
OUTDIR="./pcap"
ROTATE_SECONDS=600     # 每 600s 切一个文件
ROTATE_SIZE_MB=100     # 单文件超过 100MB 再切
KEEP_FILES=50          # 环形保留文件数,防止爆盘
ENABLE_FORWARD=0

usage() {
    sed -n '2,17p' "$0" | sed 's/^# \{0,1\}//'
    exit "${1:-0}"
}

# ---- 解析参数 ----
while [[ $# -gt 0 ]]; do
    case "$1" in
        -i|--iface)   IFACE="$2"; shift 2 ;;
        -p|--port)    PORT="$2"; shift 2 ;;
        -o|--outdir)  OUTDIR="$2"; shift 2 ;;
        -G|--rotate-seconds) ROTATE_SECONDS="$2"; shift 2 ;;
        -C|--rotate-size)    ROTATE_SIZE_MB="$2"; shift 2 ;;
        -W|--keep)    KEEP_FILES="$2"; shift 2 ;;
        --forward)    ENABLE_FORWARD=1; shift ;;
        -h|--help)    usage 0 ;;
        *) echo "未知参数: $1" >&2; usage 1 ;;
    esac
done

# ---- 必须 root(抓包 + 写 /proc) ----
if [[ "$(id -u)" -ne 0 ]]; then
    echo "错误: 需要 root 权限抓包,请用 sudo 运行。" >&2
    exit 1
fi

if ! command -v tcpdump >/dev/null 2>&1; then
    echo "错误: 未找到 tcpdump,请先安装。" >&2
    exit 1
fi

# ---- 自动选网卡: 取默认路由出口网卡 ----
if [[ -z "$IFACE" ]]; then
    IFACE="$(ip -o route get 1.1.1.1 2>/dev/null | grep -oP 'dev \K\S+' || true)"
    if [[ -z "$IFACE" ]]; then
        echo "无法自动判断网卡,请用 -i 指定。当前可用网卡:" >&2
        ip -br addr >&2
        exit 1
    fi
    echo "自动选择网卡: $IFACE (如手机经其他网卡/网桥流入,请用 -i 指定,如 br-lan)"
fi

# ---- 引流场景: 开启 ip_forward,否则手机会断网 ----
if [[ "$ENABLE_FORWARD" -eq 1 ]]; then
    echo "开启 ip_forward (ARP 引流场景)..."
    sysctl -w net.ipv4.ip_forward=1 >/dev/null
fi

mkdir -p "$OUTDIR"
PATTERN="$OUTDIR/rocom-%Y%m%d-%H%M%S.pcap"

echo "=========================================="
echo " 网卡     : $IFACE"
echo " 端口     : $PORT/tcp (双向)"
echo " 输出     : $PATTERN"
echo " 轮转     : 每 ${ROTATE_SECONDS}s 或 ${ROTATE_SIZE_MB}MB,环形保留 ${KEEP_FILES} 个"
echo " 提醒     : DATA 的 BODY 为 AES 密文,密钥由 0x1002 ACK 明文下发——须从连接建立起抓"
echo "=========================================="
echo " 按 Ctrl-C 停止抓包"
echo

# -s 0       : 全长抓包(变长协议必须)
# -G/-C/-W   : 按时间/大小轮转 + 环形保留
# --time-stamp-precision=nano : 纳秒时间戳,便于时序/RTT 统计
exec tcpdump -i "$IFACE" -s 0 \
    -G "$ROTATE_SECONDS" -C "$ROTATE_SIZE_MB" -W "$KEEP_FILES" \
    --time-stamp-precision=nano \
    -w "$PATTERN" \
    "tcp port $PORT"
