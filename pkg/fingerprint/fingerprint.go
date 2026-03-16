package fingerprint

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
	"github.com/phubbard/lantern/pkg/events"
	"github.com/phubbard/lantern/pkg/model"
)

// TCPSignature represents the fields extracted from a TCP SYN packet
type TCPSignature struct {
	IPVersion  int
	IPTTL      uint8  // initial TTL (infer from observed TTL)
	IPDontFrag bool
	TCPWindow  uint16
	TCPOptions string // ordered option list like "mss,nop,ws,nop,nop,ts,sok,eol"
	TCPMSS     uint16
	TCPScale   int // window scaling factor, -1 if absent
}

// SignatureEntry represents a known OS fingerprint
type SignatureEntry struct {
	Sig        TCPSignature
	OS         string
	OSVersion  string
	DeviceType string
	Label      string // human-readable label
}

// SignatureDB holds known OS fingerprint signatures
type SignatureDB struct {
	entries []SignatureEntry
}

// DefaultSignatureDB returns a built-in database with common OS signatures
func DefaultSignatureDB() *SignatureDB {
	db := &SignatureDB{
		entries: []SignatureEntry{
			// Windows 10/11 signatures
			{
				Sig: TCPSignature{
					IPVersion:  4,
					IPTTL:      128,
					IPDontFrag: true,
					TCPWindow:  65535,
					TCPOptions: "mss,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,ws,nop,nop,ts,nop,nop,sok",
					TCPMSS:     1460,
					TCPScale:   8,
				},
				OS:         "Windows",
				OSVersion:  "10/11",
				DeviceType: "Desktop",
				Label:      "Windows 10/11 (Desktop)",
			},
			// Windows 10/11 (laptop variant)
			{
				Sig: TCPSignature{
					IPVersion:  4,
					IPTTL:      128,
					IPDontFrag: true,
					TCPWindow:  65535,
					TCPOptions: "mss,nop,ws,nop,nop,ts,nop,nop,sok",
					TCPMSS:     1460,
					TCPScale:   8,
				},
				OS:         "Windows",
				OSVersion:  "10/11",
				DeviceType: "Desktop",
				Label:      "Windows 10/11 (Modern)",
			},
			// Windows Server 2019/2022
			{
				Sig: TCPSignature{
					IPVersion:  4,
					IPTTL:      128,
					IPDontFrag: true,
					TCPWindow:  64512,
					TCPOptions: "mss,nop,ws,nop,nop,ts,nop,nop,sok",
					TCPMSS:     1460,
					TCPScale:   7,
				},
				OS:         "Windows",
				OSVersion:  "Server 2019/2022",
				DeviceType: "Server",
				Label:      "Windows Server 2019/2022",
			},
			// macOS 12 (Monterey)
			{
				Sig: TCPSignature{
					IPVersion:  4,
					IPTTL:      64,
					IPDontFrag: false,
					TCPWindow:  65535,
					TCPOptions: "mss,nop,nop,ts,nop,ws,nop,nop,sok",
					TCPMSS:     1460,
					TCPScale:   6,
				},
				OS:         "macOS",
				OSVersion:  "12",
				DeviceType: "Desktop",
				Label:      "macOS 12 Monterey",
			},
			// macOS 13 (Ventura)
			{
				Sig: TCPSignature{
					IPVersion:  4,
					IPTTL:      64,
					IPDontFrag: false,
					TCPWindow:  65535,
					TCPOptions: "mss,nop,nop,ts,nop,ws,nop,nop,sok",
					TCPMSS:     1460,
					TCPScale:   6,
				},
				OS:         "macOS",
				OSVersion:  "13",
				DeviceType: "Desktop",
				Label:      "macOS 13 Ventura",
			},
			// macOS 14 (Sonoma)
			{
				Sig: TCPSignature{
					IPVersion:  4,
					IPTTL:      64,
					IPDontFrag: false,
					TCPWindow:  65535,
					TCPOptions: "mss,nop,nop,ts,nop,ws,nop,nop,sok",
					TCPMSS:     1460,
					TCPScale:   6,
				},
				OS:         "macOS",
				OSVersion:  "14",
				DeviceType: "Desktop",
				Label:      "macOS 14 Sonoma",
			},
			// Linux kernel 4.x (Ubuntu 18.04/20.04)
			{
				Sig: TCPSignature{
					IPVersion:  4,
					IPTTL:      64,
					IPDontFrag: false,
					TCPWindow:  64240,
					TCPOptions: "mss,nop,nop,ts,nop,ws",
					TCPMSS:     1460,
					TCPScale:   7,
				},
				OS:         "Linux",
				OSVersion:  "4.x",
				DeviceType: "Desktop",
				Label:      "Linux Kernel 4.x",
			},
			// Linux kernel 5.x (Ubuntu 20.04/22.04)
			{
				Sig: TCPSignature{
					IPVersion:  4,
					IPTTL:      64,
					IPDontFrag: false,
					TCPWindow:  65160,
					TCPOptions: "mss,nop,nop,ts,nop,ws",
					TCPMSS:     1460,
					TCPScale:   7,
				},
				OS:         "Linux",
				OSVersion:  "5.x",
				DeviceType: "Desktop",
				Label:      "Linux Kernel 5.x",
			},
			// Linux kernel 6.x (Ubuntu 22.04+)
			{
				Sig: TCPSignature{
					IPVersion:  4,
					IPTTL:      64,
					IPDontFrag: false,
					TCPWindow:  65160,
					TCPOptions: "mss,nop,nop,ts,nop,ws",
					TCPMSS:     1460,
					TCPScale:   7,
				},
				OS:         "Linux",
				OSVersion:  "6.x",
				DeviceType: "Desktop",
				Label:      "Linux Kernel 6.x",
			},
			// Debian/Raspberry Pi
			{
				Sig: TCPSignature{
					IPVersion:  4,
					IPTTL:      64,
					IPDontFrag: false,
					TCPWindow:  29200,
					TCPOptions: "mss,nop,nop,ts,nop,ws",
					TCPMSS:     1460,
					TCPScale:   5,
				},
				OS:         "Linux",
				OSVersion:  "Debian ARM",
				DeviceType: "IoT",
				Label:      "Debian/Raspberry Pi",
			},
			// CentOS/RHEL 7
			{
				Sig: TCPSignature{
					IPVersion:  4,
					IPTTL:      64,
					IPDontFrag: false,
					TCPWindow:  65535,
					TCPOptions: "mss,nop,nop,ts,nop,ws",
					TCPMSS:     1460,
					TCPScale:   7,
				},
				OS:         "Linux",
				OSVersion:  "CentOS/RHEL 7",
				DeviceType: "Server",
				Label:      "CentOS/RHEL 7",
			},
			// CentOS/RHEL 8+
			{
				Sig: TCPSignature{
					IPVersion:  4,
					IPTTL:      64,
					IPDontFrag: false,
					TCPWindow:  65535,
					TCPOptions: "mss,nop,nop,ts,nop,ws",
					TCPMSS:     1460,
					TCPScale:   7,
				},
				OS:         "Linux",
				OSVersion:  "CentOS/RHEL 8+",
				DeviceType: "Server",
				Label:      "CentOS/RHEL 8+",
			},
			// iOS 15/16
			{
				Sig: TCPSignature{
					IPVersion:  4,
					IPTTL:      64,
					IPDontFrag: false,
					TCPWindow:  65535,
					TCPOptions: "mss,nop,nop,ts,nop,ws,nop,nop,sok",
					TCPMSS:     1460,
					TCPScale:   6,
				},
				OS:         "iOS",
				OSVersion:  "15/16",
				DeviceType: "Mobile",
				Label:      "iOS 15/16",
			},
			// iOS 17
			{
				Sig: TCPSignature{
					IPVersion:  4,
					IPTTL:      64,
					IPDontFrag: false,
					TCPWindow:  65535,
					TCPOptions: "mss,nop,nop,ts,nop,ws,nop,nop,sok",
					TCPMSS:     1460,
					TCPScale:   6,
				},
				OS:         "iOS",
				OSVersion:  "17",
				DeviceType: "Mobile",
				Label:      "iOS 17",
			},
			// Android 12
			{
				Sig: TCPSignature{
					IPVersion:  4,
					IPTTL:      64,
					IPDontFrag: false,
					TCPWindow:  32768,
					TCPOptions: "mss,nop,nop,ts,nop,ws",
					TCPMSS:     1460,
					TCPScale:   5,
				},
				OS:         "Android",
				OSVersion:  "12",
				DeviceType: "Mobile",
				Label:      "Android 12",
			},
			// Android 13
			{
				Sig: TCPSignature{
					IPVersion:  4,
					IPTTL:      64,
					IPDontFrag: false,
					TCPWindow:  32768,
					TCPOptions: "mss,nop,nop,ts,nop,ws",
					TCPMSS:     1460,
					TCPScale:   5,
				},
				OS:         "Android",
				OSVersion:  "13",
				DeviceType: "Mobile",
				Label:      "Android 13",
			},
			// Android 14
			{
				Sig: TCPSignature{
					IPVersion:  4,
					IPTTL:      64,
					IPDontFrag: false,
					TCPWindow:  32768,
					TCPOptions: "mss,nop,nop,ts,nop,ws",
					TCPMSS:     1460,
					TCPScale:   5,
				},
				OS:         "Android",
				OSVersion:  "14",
				DeviceType: "Mobile",
				Label:      "Android 14",
			},
			// FreeBSD 13
			{
				Sig: TCPSignature{
					IPVersion:  4,
					IPTTL:      64,
					IPDontFrag: false,
					TCPWindow:  65535,
					TCPOptions: "mss,nop,nop,ts,nop,ws",
					TCPMSS:     1460,
					TCPScale:   6,
				},
				OS:         "FreeBSD",
				OSVersion:  "13",
				DeviceType: "Server",
				Label:      "FreeBSD 13",
			},
			// FreeBSD 14
			{
				Sig: TCPSignature{
					IPVersion:  4,
					IPTTL:      64,
					IPDontFrag: false,
					TCPWindow:  65535,
					TCPOptions: "mss,nop,nop,ts,nop,ws",
					TCPMSS:     1460,
					TCPScale:   6,
				},
				OS:         "FreeBSD",
				OSVersion:  "14",
				DeviceType: "Server",
				Label:      "FreeBSD 14",
			},
			// Generic Linux ARM (IoT devices)
			{
				Sig: TCPSignature{
					IPVersion:  4,
					IPTTL:      64,
					IPDontFrag: false,
					TCPWindow:  32768,
					TCPOptions: "mss,nop,nop,ts,nop,ws",
					TCPMSS:     1460,
					TCPScale:   5,
				},
				OS:         "Linux",
				OSVersion:  "Generic ARM",
				DeviceType: "IoT",
				Label:      "Generic Linux ARM (IoT)",
			},
			// OpenWRT (router)
			{
				Sig: TCPSignature{
					IPVersion:  4,
					IPTTL:      64,
					IPDontFrag: false,
					TCPWindow:  29200,
					TCPOptions: "mss,nop,nop,ts,nop,ws",
					TCPMSS:     1460,
					TCPScale:   5,
				},
				OS:         "OpenWRT",
				OSVersion:  "Generic",
				DeviceType: "Router",
				Label:      "OpenWRT Router",
			},
			// Cisco IOS
			{
				Sig: TCPSignature{
					IPVersion:  4,
					IPTTL:      255,
					IPDontFrag: true,
					TCPWindow:  4128,
					TCPOptions: "mss,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,ws,nop,nop,ts",
					TCPMSS:     1460,
					TCPScale:   3,
				},
				OS:         "Cisco",
				OSVersion:  "IOS",
				DeviceType: "NetworkDevice",
				Label:      "Cisco IOS",
			},
			// HP LaserJet Printer
			{
				Sig: TCPSignature{
					IPVersion:  4,
					IPTTL:      64,
					IPDontFrag: false,
					TCPWindow:  5840,
					TCPOptions: "mss,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,ws,nop,nop,ts",
					TCPMSS:     1460,
					TCPScale:   1,
				},
				OS:         "HP",
				OSVersion:  "LaserJet",
				DeviceType: "Printer",
				Label:      "HP LaserJet Printer",
			},
			// Apple AirPort Express
			{
				Sig: TCPSignature{
					IPVersion:  4,
					IPTTL:      64,
					IPDontFrag: false,
					TCPWindow:  16384,
					TCPOptions: "mss,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,nop,ts",
					TCPMSS:     1460,
					TCPScale:   -1,
				},
				OS:         "Apple",
				OSVersion:  "AirPort",
				DeviceType: "NetworkDevice",
				Label:      "Apple AirPort Express",
			},
			// Generic Linux Server
			{
				Sig: TCPSignature{
					IPVersion:  4,
					IPTTL:      64,
					IPDontFrag: false,
					TCPWindow:  65535,
					TCPOptions: "mss,nop,nop,ts,nop,ws",
					TCPMSS:     1460,
					TCPScale:   7,
				},
				OS:         "Linux",
				OSVersion:  "Generic",
				DeviceType: "Server",
				Label:      "Generic Linux Server",
			},
		},
	}
	return db
}

// Match performs score-based matching against the signature database
func (db *SignatureDB) Match(sig TCPSignature) (entry SignatureEntry, confidence float64) {
	var bestEntry SignatureEntry
	var bestScore float64 = -1.0

	for _, candidate := range db.entries {
		score := scoreSimilarity(sig, candidate.Sig)
		if score > bestScore {
			bestScore = score
			bestEntry = candidate
		}
	}

	if bestScore < 0 {
		// Return a generic Linux entry if no match found
		bestEntry = db.entries[len(db.entries)-1] // Generic Linux Server
		confidence = 0.0
	} else {
		confidence = bestScore / 100.0
	}

	return bestEntry, confidence
}

// scoreSimilarity computes a matching score between two signatures
func scoreSimilarity(observed, candidate TCPSignature) float64 {
	var score float64 = 0.0

	// TTL matching (up to 30 points)
	observedInitial := InferInitialTTL(observed.IPTTL)
	if observedInitial == candidate.IPTTL {
		score += 30
	}

	// Window size matching (up to 20 points)
	if observed.TCPWindow == candidate.TCPWindow {
		score += 20
	} else if windowInRange(observed.TCPWindow, candidate.TCPWindow) {
		score += 10
	}

	// TCP options order matching (up to 30 points)
	if observed.TCPOptions == candidate.TCPOptions {
		score += 30
	} else if optionsPartialMatch(observed.TCPOptions, candidate.TCPOptions) {
		score += 15
	}

	// MSS matching (up to 10 points)
	if observed.TCPMSS == candidate.TCPMSS {
		score += 10
	} else if observed.TCPMSS > 0 && candidate.TCPMSS > 0 &&
		((observed.TCPMSS > candidate.TCPMSS*9/10 && observed.TCPMSS < candidate.TCPMSS*11/10) ||
			(observed.TCPMSS > 1300 && observed.TCPMSS < 1500)) {
		score += 5
	}

	// Window scale matching (up to 10 points)
	if observed.TCPScale == candidate.TCPScale {
		score += 10
	} else if observed.TCPScale >= 0 && candidate.TCPScale >= 0 &&
		(observed.TCPScale == candidate.TCPScale-1 || observed.TCPScale == candidate.TCPScale+1) {
		score += 5
	}

	// DF flag consistency (bonus points)
	if observed.IPDontFrag == candidate.IPDontFrag {
		score += 5
	}

	return score
}

// windowInRange checks if window sizes are within reasonable range
func windowInRange(observed, candidate uint16) bool {
	if candidate == 0 {
		return false
	}
	diff := float64(observed) / float64(candidate)
	return diff > 0.8 && diff < 1.2
}

// optionsPartialMatch checks if options have significant overlap
func optionsPartialMatch(observed, candidate string) bool {
	obsOpts := strings.Split(observed, ",")
	candOpts := strings.Split(candidate, ",")

	matchCount := 0
	for _, opt := range obsOpts {
		for _, candOpt := range candOpts {
			if opt == candOpt {
				matchCount++
				break
			}
		}
	}

	minLen := len(obsOpts)
	if len(candOpts) < minLen {
		minLen = len(candOpts)
	}

	if minLen == 0 {
		return false
	}

	return float64(matchCount)/float64(minLen) > 0.6
}

// InferInitialTTL infers the initial TTL based on observed TTL
// Common initial TTLs are 64 (Linux/macOS), 128 (Windows), 255 (Solaris/Cisco)
func InferInitialTTL(observed uint8) uint8 {
	if observed >= 225 {
		return 255
	}
	if observed >= 96 {
		return 128
	}
	return 64
}

// Sniffer captures TCP SYN packets on the network
type Sniffer struct {
	iface    string
	handle   *pcap.Handle
	db       *SignatureDB
	events   *events.Store
	callback func(mac string, fp *model.HostFingerprint)
	logger   *slog.Logger
	done     chan struct{}
}

// NewSniffer creates a new packet sniffer
func NewSniffer(iface string, db *SignatureDB, es *events.Store,
	callback func(string, *model.HostFingerprint), logger *slog.Logger) *Sniffer {
	return &Sniffer{
		iface:    iface,
		db:       db,
		events:   es,
		callback: callback,
		logger:   logger,
		done:     make(chan struct{}),
	}
}

// Start begins sniffing TCP SYN packets
func (s *Sniffer) Start(ctx context.Context) error {
	// Open pcap handle on interface
	handle, err := pcap.OpenLive(s.iface, 96, true, pcap.BlockForever)
	if err != nil {
		return fmt.Errorf("failed to open pcap handle: %w", err)
	}
	s.handle = handle

	// Set BPF filter for TCP SYN packets
	filter := "tcp[tcpflags] & tcp-syn != 0"
	if err := handle.SetBPFFilter(filter); err != nil {
		handle.Close()
		return fmt.Errorf("failed to set BPF filter: %w", err)
	}

	s.logger.Info("Started TCP fingerprint sniffer", "interface", s.iface)

	// Create packet source
	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())

	// Packet processing loop
	go func() {
		for {
			select {
			case <-ctx.Done():
				s.Stop()
				return
			case <-s.done:
				return
			case packet := <-packetSource.Packets():
				if packet != nil {
					s.processPacket(packet)
				}
			}
		}
	}()

	return nil
}

// processPacket extracts fingerprint information from a TCP SYN packet
func (s *Sniffer) processPacket(packet gopacket.Packet) {
	// Extract Ethernet layer for source MAC
	ethLayer := packet.Layer(layers.LayerTypeEthernet)
	if ethLayer == nil {
		return
	}
	eth, ok := ethLayer.(*layers.Ethernet)
	if !ok {
		return
	}
	srcMAC := eth.SrcMAC.String()

	// Extract IPv4 layer
	ipLayer := packet.Layer(layers.LayerTypeIPv4)
	if ipLayer == nil {
		return
	}
	ipv4, ok := ipLayer.(*layers.IPv4)
	if !ok {
		return
	}

	// Extract TCP layer
	tcpLayer := packet.Layer(layers.LayerTypeTCP)
	if tcpLayer == nil {
		return
	}
	tcp, ok := tcpLayer.(*layers.TCP)
	if !ok {
		return
	}

	// Verify this is a SYN packet
	if !tcp.SYN {
		return
	}

	// Build TCP signature
	optStr, mss, scale := s.extractTCPOptions(tcp)

	sig := TCPSignature{
		IPVersion:  4,
		IPTTL:      ipv4.TTL,
		IPDontFrag: (ipv4.Flags & layers.IPv4DontFragment) != 0,
		TCPWindow:  tcp.Window,
		TCPOptions: optStr,
		TCPMSS:     mss,
		TCPScale:   scale,
	}

	// Match against database
	entry, confidence := s.db.Match(sig)

	// Build host fingerprint
	fp := &model.HostFingerprint{
		OS:         entry.OS,
		OSVersion:  entry.OSVersion,
		DeviceType: entry.DeviceType,
		Confidence: confidence,
		RawSig: fmt.Sprintf("ttl=%d;win=%d;mss=%d;scale=%d;opts=%s;df=%v",
			sig.IPTTL, sig.TCPWindow, sig.TCPMSS, sig.TCPScale, sig.TCPOptions, sig.IPDontFrag),
		FirstSeen: time.Now(),
		LastSeen:  time.Now(),
	}

	// Record event
	s.events.Record(model.HostEvent{
		Timestamp: time.Now(),
		MAC:       srcMAC,
		IP:        ipv4.SrcIP.String(),
		Type:      model.EventFingerprint,
		Detail:    fmt.Sprintf("os=%s;version=%s;type=%s;confidence=%.2f", entry.OS, entry.OSVersion, entry.DeviceType, confidence),
	})

	// Call callback
	if s.callback != nil {
		s.callback(srcMAC, fp)
	}

	s.logger.Debug("Fingerprinted host",
		"mac", srcMAC,
		"ip", ipv4.SrcIP.String(),
		"os", entry.OS,
		"version", entry.OSVersion,
		"confidence", confidence,
	)
}

// extractTCPOptions parses TCP options and extracts MSS and window scale
func (s *Sniffer) extractTCPOptions(tcp *layers.TCP) (string, uint16, int) {
	var optionNames []string
	var mss uint16 = 0
	var scale int = -1

	for _, opt := range tcp.Options {
		switch opt.OptionType {
		case layers.TCPOptionKindEndList:
			optionNames = append(optionNames, "eol")
		case layers.TCPOptionKindNop:
			optionNames = append(optionNames, "nop")
		case layers.TCPOptionKindMSS:
			optionNames = append(optionNames, "mss")
			if len(opt.OptionData) >= 2 {
				mss = uint16(opt.OptionData[0])<<8 | uint16(opt.OptionData[1])
			}
		case layers.TCPOptionKindWindowScale:
			optionNames = append(optionNames, "ws")
			if len(opt.OptionData) >= 1 {
				scale = int(opt.OptionData[0])
			}
		case layers.TCPOptionKindTimestamps:
			optionNames = append(optionNames, "ts")
		case layers.TCPOptionKindSACKPermitted:
			optionNames = append(optionNames, "sok")
		case layers.TCPOptionKindSACK:
			optionNames = append(optionNames, "sack")
		default:
			// Unknown option
			optionNames = append(optionNames, fmt.Sprintf("opt%d", opt.OptionType))
		}
	}

	optStr := strings.Join(optionNames, ",")
	return optStr, mss, scale
}

// Stop closes the packet sniffer
func (s *Sniffer) Stop() {
	if s.handle != nil {
		s.handle.Close()
	}
	close(s.done)
	s.logger.Info("Stopped TCP fingerprint sniffer", "interface", s.iface)
}
