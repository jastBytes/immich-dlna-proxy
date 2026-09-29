package dlna

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/jastBytes/immich-dlna-proxy/config"
)

const (
	ssdpAddr = "239.255.255.250:1900"
	deviceST = "urn:schemas-upnp-org:device:MediaServer:1"
)

// searchTargets lists every NT/ST this device advertises: the root device,
// its UUID, its device type, and each service type. Control points commonly
// search for a specific service type (e.g. ContentDirectory) rather than
// the device type, so all of these must be answerable individually - a
// server that only answers device-level searches is invisible to them.
func searchTargets(uuid string) []string {
	return []string{"upnp:rootdevice", "uuid:" + uuid, deviceST, cdNS, cmNS, mrrNS}
}

// RunSSDP listens for M-SEARCH requests and answers them, and periodically
// sends unsolicited ssdp:alive NOTIFY announcements so clients that are
// already listening pick the server up without having to search.
// It blocks until ctx is cancelled - at which point it announces
// ssdp:byebye, so clients drop the server right away instead of listing
// it until the last announcement's max-age (30 minutes) runs out - or
// until an unrecoverable error occurs.
func RunSSDP(ctx context.Context, cfg *config.Config) error {
	groupAddr, err := net.ResolveUDPAddr("udp4", ssdpAddr)
	if err != nil {
		return err
	}

	var iface *net.Interface
	if cfg.Interface != "" {
		iface, err = net.InterfaceByName(cfg.Interface)
		if err != nil {
			return fmt.Errorf("SSDP interface %q not found: %w", cfg.Interface, err)
		}
	}

	conn, err := net.ListenMulticastUDP("udp4", iface, groupAddr)
	if err != nil {
		return fmt.Errorf("SSDP multicast listen failed: %w", err)
	}
	defer func() { _ = conn.Close() }()

	localIP, err := advertiseIP(cfg.AdvertiseIP, iface)
	if err != nil {
		return fmt.Errorf("could not determine local IP for SSDP: %w", err)
	}
	port := portFromAddr(cfg.ListenAddr)
	location := fmt.Sprintf("http://%s:%s/description.xml", localIP, port)
	log.Printf("SSDP announcing %s", location)

	// Unicast socket used both to reply to M-SEARCH and to send periodic
	// NOTIFY alive announcements to the multicast group.
	outConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(localIP)})
	if err != nil {
		return fmt.Errorf("SSDP unicast socket failed: %w", err)
	}
	defer func() { _ = outConn.Close() }()

	go notifyLoop(ctx, outConn, groupAddr, cfg.UUID, location)

	// Closing the listening socket is what unblocks ReadFromUDP below.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	buf := make([]byte, 2048)
	for {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				sendByebye(outConn, groupAddr, cfg.UUID)
				return nil
			}
			log.Printf("SSDP read error: %v", err)
			continue
		}
		msg := string(buf[:n])
		if !strings.HasPrefix(msg, "M-SEARCH") {
			continue
		}
		st := parseHeader(msg, "ST")
		targets := searchTargets(cfg.UUID)
		matched := st == "ssdp:all"
		for _, t := range targets {
			if st == t {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		go respondMSearch(outConn, src, cfg.UUID, location, st, targets)
	}
}

// respondMSearch answers one M-SEARCH. A targeted search (st is one of our
// advertised types) gets a single matching reply; "ssdp:all" gets one reply
// per advertised type, same as an alive NOTIFY burst.
func respondMSearch(conn *net.UDPConn, dst *net.UDPAddr, uuid, location, st string, targets []string) {
	if st != "ssdp:all" {
		sendSearchReply(conn, dst, uuid, location, st)
		return
	}
	for _, t := range targets {
		sendSearchReply(conn, dst, uuid, location, t)
	}
}

func sendSearchReply(conn *net.UDPConn, dst *net.UDPAddr, uuid, location, st string) {
	usn := "uuid:" + uuid
	if st != usn {
		usn += "::" + st
	}
	resp := "HTTP/1.1 200 OK\r\n" +
		"CACHE-CONTROL: max-age=1800\r\n" +
		"EXT:\r\n" +
		"LOCATION: " + location + "\r\n" +
		"SERVER: " + ssdpServerHeader() + "\r\n" +
		"ST: " + st + "\r\n" +
		"USN: " + usn + "\r\n" +
		"\r\n"
	if _, err := conn.WriteToUDP([]byte(resp), dst); err != nil {
		log.Printf("SSDP M-SEARCH reply failed: %v", err)
	}
}

func notifyLoop(ctx context.Context, conn *net.UDPConn, group *net.UDPAddr, uuid, location string) {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for {
		sendAlive(conn, group, uuid, location)
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

// sendByebye announces that the device is going away, one NOTIFY per
// advertised target, mirroring sendAlive.
func sendByebye(conn *net.UDPConn, group *net.UDPAddr, uuid string) {
	for _, nt := range searchTargets(uuid) {
		usn := "uuid:" + uuid
		if nt != usn {
			usn += "::" + nt
		}
		notify := "NOTIFY * HTTP/1.1\r\n" +
			"HOST: 239.255.255.250:1900\r\n" +
			"NT: " + nt + "\r\n" +
			"NTS: ssdp:byebye\r\n" +
			"USN: " + usn + "\r\n" +
			"\r\n"
		if _, err := conn.WriteToUDP([]byte(notify), group); err != nil {
			log.Printf("SSDP byebye failed: %v", err)
		}
	}
}

func sendAlive(conn *net.UDPConn, group *net.UDPAddr, uuid, location string) {
	for _, nt := range searchTargets(uuid) {
		usn := "uuid:" + uuid
		if nt != usn {
			usn += "::" + nt
		}
		notify := "NOTIFY * HTTP/1.1\r\n" +
			"HOST: 239.255.255.250:1900\r\n" +
			"CACHE-CONTROL: max-age=1800\r\n" +
			"LOCATION: " + location + "\r\n" +
			"NT: " + nt + "\r\n" +
			"NTS: ssdp:alive\r\n" +
			"SERVER: " + ssdpServerHeader() + "\r\n" +
			"USN: " + usn + "\r\n" +
			"\r\n"
		if _, err := conn.WriteToUDP([]byte(notify), group); err != nil {
			log.Printf("SSDP NOTIFY failed: %v", err)
		}
	}
}

func parseHeader(msg, name string) string {
	for _, line := range strings.Split(msg, "\r\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(parts[0]), name) {
			return strings.TrimSpace(parts[1])
		}
	}
	return ""
}

func portFromAddr(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return "8200"
	}
	return port
}

// advertiseIP picks the IP announced in SSDP LOCATION URLs: ADVERTISE_IP
// if set, else the IPv4 address of SSDP_INTERFACE if one is configured
// (so a multi-homed host announces the address on the network it's
// actually advertising to, not whichever one has the default route), else
// the address the OS would use to reach the internet, else - for a LAN
// with no default route at all - the first non-loopback IPv4 address.
func advertiseIP(configured string, iface *net.Interface) (string, error) {
	if configured != "" {
		return configured, nil
	}
	if iface != nil {
		return interfaceIPv4(iface)
	}
	if ip, err := detectLocalIP(); err == nil {
		return ip, nil
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for i := range ifaces {
		if ifaces[i].Flags&net.FlagUp == 0 || ifaces[i].Flags&net.FlagLoopback != 0 {
			continue
		}
		if ip, err := interfaceIPv4(&ifaces[i]); err == nil {
			return ip, nil
		}
	}
	return "", fmt.Errorf("no non-loopback IPv4 address found; set ADVERTISE_IP")
}

// interfaceIPv4 returns iface's first IPv4 address.
func interfaceIPv4(iface *net.Interface) (string, error) {
	addrs, err := iface.Addrs()
	if err != nil {
		return "", err
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok {
			if ip4 := ipnet.IP.To4(); ip4 != nil {
				return ip4.String(), nil
			}
		}
	}
	return "", fmt.Errorf("interface %s has no IPv4 address", iface.Name)
}

// detectLocalIP finds an outbound-facing local IP by "dialing" a UDP socket
// (no packets are actually sent for UDP dial) to a public address and
// reading back the local address the OS would use.
func detectLocalIP() (string, error) {
	conn, err := net.Dial("udp4", "8.8.8.8:80")
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()
	addr := conn.LocalAddr().(*net.UDPAddr)
	return addr.IP.String(), nil
}
