package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
)

const (
	dhcpServerPort = 67
	dhcpClientPort = 68
	broadcastAddr  = "255.255.255.255"
	dhcpTimeout    = 5 * time.Second
)

// Color codes for terminal output
const (
	colorReset = "\033[0m"
	colorGreen = "\033[0;32m"
	colorRed   = "\033[0;31m"
)

// TestResult tracks pass/fail counts
type TestResult struct {
	Passed int
	Failed int
}

// printPass prints a passing test result
func (tr *TestResult) printPass(name string) {
	fmt.Printf("%sPASS%s %s\n", colorGreen, colorReset, name)
	tr.Passed++
}

// printFail prints a failing test result with details
func (tr *TestResult) printFail(name, detail string) {
	fmt.Printf("%sFAIL%s %s\n", colorRed, colorReset, name)
	if detail != "" {
		fmt.Printf("  %s\n", detail)
	}
	tr.Failed++
}

// getMACAddress returns the MAC address of the given interface
func getMACAddress(ifaceName string) (net.HardwareAddr, error) {
	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		return nil, err
	}
	return iface.HardwareAddr, nil
}

// sendDHCPDiscover sends a DHCP DISCOVER message and waits for OFFER
func sendDHCPDiscover(clientMAC net.HardwareAddr, hostname string) (*dhcpv4.DHCPv4, error) {
	// Create DISCOVER packet using the convenient constructor
	discover, err := dhcpv4.New(
		dhcpv4.WithClientMAC(clientMAC),
		dhcpv4.WithBroadcast(true),
		dhcpv4.WithOption(dhcpv4.OptMessageType(dhcpv4.MessageTypeDiscover)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create DISCOVER packet: %w", err)
	}

	// Add hostname if provided
	if hostname != "" {
		discover.UpdateOption(dhcpv4.OptHostName(hostname))
	}

	// Listen for OFFER on port 68 before sending to avoid race conditions
	listenConn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: dhcpClientPort})
	if err != nil {
		return nil, fmt.Errorf("failed to listen on UDP: %w", err)
	}
	defer listenConn.Close()

	// Set socket to allow broadcast
	listenConn.SetReadDeadline(time.Now().Add(dhcpTimeout))

	// Send the packet via broadcast
	sendConn, err := net.DialUDP("udp4", &net.UDPAddr{Port: dhcpClientPort}, &net.UDPAddr{IP: net.ParseIP(broadcastAddr), Port: dhcpServerPort})
	if err != nil {
		return nil, fmt.Errorf("failed to dial UDP: %w", err)
	}
	defer sendConn.Close()

	_, err = sendConn.Write(discover.ToBytes())
	if err != nil {
		return nil, fmt.Errorf("failed to send DISCOVER: %w", err)
	}

	// Read response
	buf := make([]byte, 4096)
	n, _, err := listenConn.ReadFromUDP(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to receive OFFER: %w", err)
	}

	offer, err := dhcpv4.FromBytes(buf[:n])
	if err != nil {
		return nil, fmt.Errorf("failed to parse OFFER: %w", err)
	}

	return offer, nil
}

// sendDHCPRequest sends a DHCP REQUEST message and waits for ACK
func sendDHCPRequest(clientMAC net.HardwareAddr, offerIP net.IP, serverIP net.IP, xid dhcpv4.TransactionID, clientIP net.IP) (*dhcpv4.DHCPv4, error) {
	// Create REQUEST packet
	request, err := dhcpv4.New(
		dhcpv4.WithClientMAC(clientMAC),
		dhcpv4.WithBroadcast(true),
		dhcpv4.WithTransactionID(xid),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create REQUEST packet: %w", err)
	}

	// Set as REQUEST message type
	request.UpdateOption(dhcpv4.OptMessageType(dhcpv4.MessageTypeRequest))
	request.UpdateOption(dhcpv4.OptRequestedIPAddress(offerIP))
	request.UpdateOption(dhcpv4.OptServerIdentifier(serverIP))

	// Listen for ACK
	listenConn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: dhcpClientPort})
	if err != nil {
		return nil, fmt.Errorf("failed to listen on UDP: %w", err)
	}
	defer listenConn.Close()

	listenConn.SetReadDeadline(time.Now().Add(dhcpTimeout))

	// Send via broadcast
	sendConn, err := net.DialUDP("udp4", &net.UDPAddr{Port: dhcpClientPort}, &net.UDPAddr{IP: net.ParseIP(broadcastAddr), Port: dhcpServerPort})
	if err != nil {
		return nil, fmt.Errorf("failed to dial UDP: %w", err)
	}
	defer sendConn.Close()

	_, err = sendConn.Write(request.ToBytes())
	if err != nil {
		return nil, fmt.Errorf("failed to send REQUEST: %w", err)
	}

	// Read response
	buf := make([]byte, 4096)
	n, _, err := listenConn.ReadFromUDP(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to receive ACK: %w", err)
	}

	ack, err := dhcpv4.FromBytes(buf[:n])
	if err != nil {
		return nil, fmt.Errorf("failed to parse ACK: %w", err)
	}

	return ack, nil
}

// verifyDNSResolution checks if the assigned IP resolves via DNS
func verifyDNSResolution(serverIP net.IP, hostname string, expectedIP net.IP, domain string) error {
	// Build the FQDN
	fqdn := hostname + "." + domain

	// Use dig command
	cmd := exec.Command("dig", "@"+serverIP.String(), fqdn, "A", "+short")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("dig command failed: %w", err)
	}

	result := strings.TrimSpace(string(output))
	if result == "" {
		return fmt.Errorf("no DNS response for %s", fqdn)
	}

	if result != expectedIP.String() {
		return fmt.Errorf("expected %s, got %s", expectedIP.String(), result)
	}

	return nil
}

// verifyPTRResolution checks the reverse DNS lookup
func verifyPTRResolution(serverIP net.IP, ip net.IP, expectedHostname string) error {
	cmd := exec.Command("dig", "@"+serverIP.String(), "-x", ip.String(), "+short")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("dig command failed: %w", err)
	}

	result := strings.TrimSpace(string(output))
	if result == "" {
		return fmt.Errorf("no PTR response for %s", ip.String())
	}

	if !strings.Contains(result, expectedHostname) {
		return fmt.Errorf("expected hostname containing %s, got %s", expectedHostname, result)
	}

	return nil
}

// validateOffer checks that an OFFER message has required fields
func validateOffer(offer *dhcpv4.DHCPv4) error {
	if offer.YourClientIP == nil {
		return fmt.Errorf("OFFER missing YourClientIP")
	}

	msgType := offer.MessageType()
	if msgType != dhcpv4.MessageTypeOffer {
		return fmt.Errorf("expected MessageType Offer, got %v", msgType)
	}

	if offer.SubnetMask() == nil {
		return fmt.Errorf("OFFER missing SubnetMask")
	}

	if offer.Router() == nil {
		return fmt.Errorf("OFFER missing Router")
	}

	if offer.DNS() == nil {
		return fmt.Errorf("OFFER missing DNS Servers")
	}

	return nil
}

// validateACK checks that an ACK message has required fields
func validateACK(ack *dhcpv4.DHCPv4) error {
	if ack.YourClientIP == nil {
		return fmt.Errorf("ACK missing YourClientIP")
	}

	msgType := ack.MessageType()
	if msgType != dhcpv4.MessageTypeAck {
		return fmt.Errorf("expected MessageType Ack, got %v", msgType)
	}

	if ack.IPAddressLeaseTime() == 0 {
		return fmt.Errorf("ACK missing IP Address Lease Time")
	}

	return nil
}

// isIPInRange checks if an IP is within the expected DHCP range
func isIPInRange(ip net.IP, rangeStart, rangeEnd string) bool {
	start := net.ParseIP(rangeStart)
	end := net.ParseIP(rangeEnd)

	if ip.To4() == nil || start.To4() == nil || end.To4() == nil {
		return false
	}

	ipInt := ipToInt(ip.To4())
	startInt := ipToInt(start.To4())
	endInt := ipToInt(end.To4())

	return ipInt >= startInt && ipInt <= endInt
}

// ipToInt converts an IPv4 address to an integer
func ipToInt(ip net.IP) uint32 {
	return uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
}

// waitForServer checks if the DHCP server is responding
func waitForServer(serverIP net.IP, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("udp4", serverIP.String()+":67", 1*time.Second)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("server not responding after %v", timeout)
}

func main() {
	server := flag.String("server", "172.30.0.2", "DHCP server IP")
	ifaceName := flag.String("interface", "eth0", "Network interface")
	domain := flag.String("domain", "test.lantern", "Domain suffix")
	flag.Parse()

	results := &TestResult{}

	fmt.Println("")
	fmt.Println("════════════════════════════════════════")
	fmt.Println(" DHCP Protocol Tests")
	fmt.Println("════════════════════════════════════════")
	fmt.Println("")

	serverIP := net.ParseIP(*server)
	if serverIP == nil {
		fmt.Printf("%sFAIL%s Invalid server IP: %s\n", colorRed, colorReset, *server)
		os.Exit(1)
	}

	// Get client MAC address
	clientMAC, err := getMACAddress(*ifaceName)
	if err != nil {
		fmt.Printf("%sFAIL%s Failed to get MAC address: %v\n", colorRed, colorReset, err)
		os.Exit(1)
	}

	fmt.Printf("Using MAC address: %s\n", clientMAC.String())
	fmt.Printf("Using server: %s\n\n", serverIP.String())

	// Test 1: DISCOVER → OFFER
	fmt.Println("Test 1: Send DISCOVER, receive OFFER...")
	offer, err := sendDHCPDiscover(clientMAC, "")
	if err != nil {
		results.printFail("DHCP: DISCOVER → OFFER", err.Error())
	} else {
		err = validateOffer(offer)
		if err != nil {
			results.printFail("DHCP: DISCOVER → OFFER", err.Error())
		} else {
			if !isIPInRange(offer.YourClientIP, "172.30.0.100", "172.30.0.250") {
				results.printFail("DHCP: DISCOVER → OFFER", fmt.Sprintf("offered IP %s not in DHCP range", offer.YourClientIP))
			} else {
				results.printPass("DHCP: DISCOVER → OFFER (IP: " + offer.YourClientIP.String() + ")")
			}
		}
	}

	if offer == nil {
		fmt.Printf("%sFAIL%s Could not get OFFER to proceed with tests\n", colorRed, colorReset)
		fmt.Printf("\n════════════════════════════════════════\n")
		fmt.Printf(" Results: %s%d passed%s, %s%d failed%s\n", colorGreen, results.Passed, colorReset, colorRed, results.Failed, colorReset)
		fmt.Printf("════════════════════════════════════════\n")
		os.Exit(1)
	}

	// Test 2: REQUEST → ACK
	fmt.Println("Test 2: Send REQUEST, receive ACK...")
	ack, err := sendDHCPRequest(clientMAC, offer.YourClientIP, serverIP, offer.TransactionID, offer.YourClientIP)
	if err != nil {
		results.printFail("DHCP: REQUEST → ACK", err.Error())
	} else {
		err = validateACK(ack)
		if err != nil {
			results.printFail("DHCP: REQUEST → ACK", err.Error())
		} else {
			if !ack.YourClientIP.Equal(offer.YourClientIP) {
				results.printFail("DHCP: REQUEST → ACK", fmt.Sprintf("ACK IP %s differs from OFFER IP %s", ack.YourClientIP, offer.YourClientIP))
			} else {
				results.printPass("DHCP: REQUEST → ACK (IP: " + ack.YourClientIP.String() + ")")
			}
		}
	}

	assignedIP := offer.YourClientIP

	// Test 3: DISCOVER with hostname
	fmt.Println("Test 3: Send DISCOVER with hostname...")
	offer2, err := sendDHCPDiscover(clientMAC, "testdevice")
	if err != nil {
		results.printFail("DHCP: DISCOVER with hostname", err.Error())
	} else {
		err = validateOffer(offer2)
		if err != nil {
			results.printFail("DHCP: DISCOVER with hostname", err.Error())
		} else {
			results.printPass("DHCP: DISCOVER with hostname (IP: " + offer2.YourClientIP.String() + ")")
		}
	}

	// Test 4: Verify DNS resolution of assigned IP
	fmt.Println("Test 4: Verify DNS resolution...")
	if assignedIP != nil {
		// Generate a hostname based on the last octet of the IP
		parts := strings.Split(assignedIP.String(), ".")
		lastOctet := parts[len(parts)-1]
		hostname := "dhcp-" + lastOctet

		err = verifyDNSResolution(serverIP, hostname, assignedIP, *domain)
		if err != nil {
			results.printFail("DHCP: DNS resolution of assigned IP", err.Error())
		} else {
			results.printPass("DHCP: DNS resolution of assigned IP (" + hostname + "." + *domain + ")")
		}

		// Test 5: Verify PTR resolution
		fmt.Println("Test 5: Verify PTR resolution...")
		err = verifyPTRResolution(serverIP, assignedIP, hostname)
		if err != nil {
			results.printFail("DHCP: PTR resolution", err.Error())
		} else {
			results.printPass("DHCP: PTR resolution")
		}
	}

	// Test 6: Verify assigned IP is in the lease
	fmt.Println("Test 6: Verify IP in DHCP lease range...")
	if !isIPInRange(assignedIP, "172.30.0.100", "172.30.0.250") {
		results.printFail("DHCP: IP in lease range", fmt.Sprintf("IP %s not in expected range", assignedIP))
	} else {
		results.printPass("DHCP: IP in lease range")
	}

	// Print results
	fmt.Println("")
	fmt.Println("════════════════════════════════════════")
	fmt.Printf(" Results: %s%d passed%s, %s%d failed%s\n", colorGreen, results.Passed, colorReset, colorRed, results.Failed, colorReset)
	fmt.Println("════════════════════════════════════════")

	if results.Failed > 0 {
		os.Exit(1)
	}
}
