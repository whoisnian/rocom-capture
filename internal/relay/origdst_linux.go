package relay

import (
	"errors"
	"fmt"
	"net"
	"syscall"
)

// soOriginalDst 是 netfilter 的 SO_ORIGINAL_DST(内核里没给它一个 syscall 常量)。
const soOriginalDst = 80

// originalDst 取被 nftables/iptables redirect 改写之前的原始目的地址。
//
// redirect 把包 DNAT 到本机监听口,accept 出来的 conn.LocalAddr() 已经是本机地址,
// 真正的目的地要向 conntrack 要。取出来的是 struct sockaddr_in(16 字节),布局与
// syscall.IPv6Mreq 一致,所以借它当容器 —— 这是这个 socket 选项的惯用取法。
func originalDst(c net.Conn) (string, error) {
	tc, ok := c.(*net.TCPConn)
	if !ok {
		return "", errors.New("relay: 不是 TCP 连接")
	}
	rc, err := tc.SyscallConn()
	if err != nil {
		return "", err
	}
	var mreq *syscall.IPv6Mreq
	var serr error
	if err := rc.Control(func(fd uintptr) {
		mreq, serr = syscall.GetsockoptIPv6Mreq(int(fd), syscall.SOL_IP, soOriginalDst)
	}); err != nil {
		return "", err
	}
	if serr != nil {
		return "", serr
	}
	// sockaddr_in: [0:2] sin_family, [2:4] sin_port(网络序), [4:8] sin_addr
	ip := net.IPv4(mreq.Multiaddr[4], mreq.Multiaddr[5], mreq.Multiaddr[6], mreq.Multiaddr[7])
	port := int(mreq.Multiaddr[2])<<8 | int(mreq.Multiaddr[3])
	return fmt.Sprintf("%s:%d", ip, port), nil
}
