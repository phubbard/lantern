package netutil

import (
	"fmt"
	"net"
	"strings"
)

// IPToUint32 converts a net.IP to a uint32 (IPv4 only)
func IPToUint32(ip net.IP) uint32 {
	ip = ip.To4()
	if ip == nil {
		return 0
	}
	return uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
}

// Uint32ToIP converts a uint32 to a net.IP (IPv4 only)
func Uint32ToIP(n uint32) net.IP {
	return net.IPv4(byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
}

// NextIP returns the next IP address after the given IP
func NextIP(ip net.IP) net.IP {
	ip = ip.To4()
	if ip == nil {
		return nil
	}
	n := IPToUint32(ip)
	return Uint32ToIP(n + 1)
}

// PrevIP returns the previous IP address before the given IP
func PrevIP(ip net.IP) net.IP {
	ip = ip.To4()
	if ip == nil {
		return nil
	}
	n := IPToUint32(ip)
	if n == 0 {
		return nil
	}
	return Uint32ToIP(n - 1)
}

// IPInRange checks if an IP is within the start and end range (inclusive)
func IPInRange(ip, start, end net.IP) bool {
	ip = ip.To4()
	start = start.To4()
	end = end.To4()

	if ip == nil || start == nil || end == nil {
		return false
	}

	ipNum := IPToUint32(ip)
	startNum := IPToUint32(start)
	endNum := IPToUint32(end)

	return ipNum >= startNum && ipNum <= endNum
}

// LastOctet returns the last octet (4th octet) of an IPv4 address
func LastOctet(ip net.IP) byte {
	ip = ip.To4()
	if ip == nil {
		return 0
	}
	return ip[3]
}

// FormatLastOctet returns the last octet as a zero-padded 3-digit string
func FormatLastOctet(ip net.IP) string {
	return fmt.Sprintf("%03d", LastOctet(ip))
}

// ParseMAC parses a MAC address string into a net.HardwareAddr
func ParseMAC(s string) (net.HardwareAddr, error) {
	mac, err := net.ParseMAC(s)
	if err != nil {
		return nil, fmt.Errorf("invalid MAC address %q: %w", s, err)
	}
	return mac, nil
}

// NormalizeMAC returns a MAC address as lowercase colon-separated string
func NormalizeMAC(mac net.HardwareAddr) string {
	return strings.ToLower(mac.String())
}

// SubnetContains checks if an IP is contained within a subnet
func SubnetContains(subnet net.IPNet, ip net.IP) bool {
	return subnet.Contains(ip)
}

// BroadcastAddr returns the broadcast address for a subnet
func BroadcastAddr(subnet net.IPNet) net.IP {
	// Get the network address
	network := subnet.IP.To4()
	if network == nil {
		return nil
	}

	// Get the mask
	mask := subnet.Mask
	if mask == nil {
		return nil
	}

	// Calculate broadcast address by setting all host bits to 1
	broadcast := make(net.IP, len(network))
	copy(broadcast, network)

	for i := 0; i < len(mask); i++ {
		// Invert the mask and OR with the network address
		broadcast[i] = broadcast[i] | ^mask[i]
	}

	return broadcast
}

// NetworkAddr returns the network address for a subnet
func NetworkAddr(subnet net.IPNet) net.IP {
	network := subnet.IP.To4()
	if network == nil {
		return subnet.IP
	}

	result := make(net.IP, len(network))
	copy(result, network)

	mask := subnet.Mask
	if mask != nil {
		for i := 0; i < len(mask); i++ {
			result[i] = result[i] & mask[i]
		}
	}

	return result
}
