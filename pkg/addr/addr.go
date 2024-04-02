package addr

import (
	"fmt"
	"net"
	"os"
)

var (
	privateBlocks []*net.IPNet
)

func init() {
	blocks := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"100.64.0.0/10",
		"fd00::/8",
	}
	AppendPrivateBlocks(blocks...)
}

// AppendPrivateBlocks append private network blocks
func AppendPrivateBlocks(bs ...string) {
	for _, b := range bs {
		if _, block, err := net.ParseCIDR(b); err == nil {
			privateBlocks = append(privateBlocks, block)
		}
	}
}

func isPrivateIP(ipAddr string) bool {
	ip := net.ParseIP(ipAddr)
	if ip == nil {
		return false
	}

	for _, blocks := range privateBlocks {
		if blocks.Contains(ip) {
			return true
		}
	}
	return false
}

func addrToIP(addr net.Addr) net.IP {
	switch v := addr.(type) {
	case *net.IPAddr:
		return v.IP
	case *net.IPNet:
		return v.IP
	default:
		return nil
	}
}

func localIPs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	var ipAddrs []string

	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue // ignore error
		}

		for _, addr := range addrs {
			if ip := addrToIP(addr); ip != nil {
				// if ip.IsLoopback() {
				// 	fmt.Println("isLoopback", ip)
				// }
				ipAddrs = append(ipAddrs, ip.String())
			}
		}
	}

	return ipAddrs
}

// Extract returns a real ip
func Extract(addr string) (string, error) {
	// if addr specified then its returned
	if len(addr) > 0 {
		if addr != "0.0.0.0" && addr != "[::]" && addr != "::" {
			return addr, nil
		}
	}
	var podAddrs = os.Getenv("POD_IP")
	if len(podAddrs) > 0 {
		return podAddrs, nil
	}
	var privateAddrs []string
	var publicAddrs []string
	var loopbackAddrs []string

	for _, ipAddr := range localIPs() {
		ip := net.ParseIP(ipAddr)
		if ip == nil {
			continue
		}
		if ip.IsUnspecified() {
			continue
		}
		if ip.IsLoopback() {
			loopbackAddrs = append(loopbackAddrs, ipAddr)
		} else if isPrivateIP(ipAddr) {
			privateAddrs = append(privateAddrs, ipAddr)
		} else {
			publicAddrs = append(publicAddrs, ipAddr)
		}
	}
	if len(privateAddrs) > 0 {
		return privateAddrs[0], nil
	} else if len(publicAddrs) > 0 {
		return publicAddrs[0], nil
	} else if len(loopbackAddrs) > 0 {
		return loopbackAddrs[0], nil
	}
	return "", fmt.Errorf("not found ip")
}

// IPs returns all known ips
func IPs() []string {
	return localIPs()
}

// Parse try parse addr to host and port
func ParseHostPort(addr string) string {
	host, port := ParseHostAndPort(addr)
	return net.JoinHostPort(host, port)
}

var portNaming = map[string]string{
	"http": "80",
}

func ParseHostAndPort(addr string) (string, string) {
	host, port, err := net.SplitHostPort(addr)
	if err == nil {
		addr = host
	}
	host, _ = Extract(host)
	if v, ok := portNaming[port]; ok {
		port = v
	}
	return host, port
}
