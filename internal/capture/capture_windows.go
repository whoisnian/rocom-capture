//go:build windows

package capture

import (
	"fmt"
	"log"
	"net"
	"net/netip"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
)

// RunLive 在指定网卡上用 Npcap/WinPcap 被动抓包。阻塞运行。
// Windows 上需先安装 Npcap(https://nmap.org/npcap/)。
func (e *Engine) RunLive(iface string) error {
	ignoreSelfIPs(e, iface)

	handle, err := openPcapHandle(iface, e.Port)
	if err != nil {
		return err
	}
	defer handle.Close()

	src := gopacket.NewPacketSource(handle, layers.LayerTypeEthernet)
	src.NoCopy = true
	e.process(src)
	return nil
}

// openPcapHandle 打开指定网卡的 pcap 捕获句柄,挂载 BPF 过滤器只抓游戏端口流量。
func openPcapHandle(iface string, port int) (*pcap.Handle, error) {
	devs, err := pcap.FindAllDevs()
	if err != nil {
		return nil, err
	}
	var target *pcap.Interface
	for i := range devs {
		d := &devs[i]
		// 优先匹配 Name(如 \Device\NPF_xxx),再匹配 Description(如 "WLAN")
		if d.Name == iface || d.Description == iface {
			target = d
			break
		}
	}
	if target == nil {
		names := make([]string, 0, len(devs))
		for _, d := range devs {
			names = append(names, fmt.Sprintf("%s (%s)", d.Name, d.Description))
		}
		return nil, &errDeviceNotFound{name: iface, available: names}
	}

	// 快照长度 65535(变长协议必须全量捕获), 混合模式(抓所有流量)
	timeout := 1 * time.Second
	handle, err := pcap.OpenLive(target.Name, 65535, true, timeout)
	if err != nil {
		return nil, err
	}

	// 挂载 BPF 过滤器: 只抓目标端口的 TCP 流量
	filter := fmt.Sprintf("tcp and (dst port %d or src port %d)", port, port)
	if err := handle.SetBPFFilter(filter); err != nil {
		handle.Close()
		return nil, err
	}

	log.Printf("已打开 pcap 设备: %s (%s)", target.Name, target.Description)
	return handle, nil
}

// ignoreSelfIPs 把网卡自身的 IP 登记进忽略集(单臂 NAT 去重,与 Linux 版逻辑一致)。
func ignoreSelfIPs(e *Engine, iface string) {
	ifis, err := net.Interfaces()
	if err != nil {
		return
	}
	for _, ifi := range ifis {
		if ifi.Name != iface {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
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
			log.Printf("单臂网关去重: 忽略本机 %s 的 IP %v", ifi.Name, ips)
		}
	}
}

type errDeviceNotFound struct {
	name      string
	available []string
}

func (e *errDeviceNotFound) Error() string {
	msg := fmt.Sprintf("未找到设备: %s\n可用设备:", e.name)
	for _, n := range e.available {
		msg += "\n  " + n
	}
	return msg
}
