//go:build linux

package capture

import (
	"log"
	"net"
	"net/netip"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/afpacket"
	"github.com/google/gopacket/layers"
)

// RunLive 在指定网卡上用 AF_PACKET 被动抓包(无需 libpcap)。阻塞运行。
func (e *Engine) RunLive(iface string) error {
	// 单臂网关去重:抓包网卡在做 SNAT 转发时,会把游戏流的一个副本(源改为本机 IP)
	// 再次从同一网卡发出并被捕获。登记本机 IP 到忽略集,只保留 NAT 前的真实客户端会话。
	ignoreSelfIPs(e, iface)

	tp, err := afpacket.NewTPacket(
		afpacket.OptInterface(iface),
		afpacket.OptPollTimeout(time.Second),
	)
	if err != nil {
		return err
	}
	defer tp.Close()
	src := gopacket.NewPacketSource(tp, layers.LayerTypeEthernet)
	src.NoCopy = true
	e.process(src)
	return nil
}

// ignoreSelfIPs 把网卡自身的单播 IP 登记进忽略集(单臂 NAT 去重,见 RunLive)。
func ignoreSelfIPs(e *Engine, iface string) {
	ifi, err := net.InterfaceByName(iface)
	if err != nil {
		return
	}
	addrs, err := ifi.Addrs()
	if err != nil {
		return
	}
	var ips []netip.Addr
	for _, a := range addrs {
		var raw net.IP
		switch v := a.(type) {
		case *net.IPNet:
			raw = v.IP
		case *net.IPAddr:
			raw = v.IP
		}
		if ip, ok := netip.AddrFromSlice(raw); ok && !ip.IsLoopback() {
			ip = ip.Unmap()
			e.AddSkipIP(ip)
			ips = append(ips, ip)
		}
	}
	if len(ips) > 0 {
		log.Printf("单臂网关去重: 忽略本机 %s 的 IP %v", iface, ips)
	}
}
