package dlna

import (
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

// RunSSDP listens for M-SEARCH requests and answers them, and periodically
// sends unsolicited ssdp:alive NOTIFY announcements so clients that are
// already listening pick the server up without having to search.
// It blocks until an unrecoverable error occurs.
func RunSSDP(cfg *config.Config) error {
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

	localIP, err := detectLocalIP()
	if err != nil {
		return fmt.Errorf("could not determine local IP for SSDP: %w", err)
	}
	port := portFromAddr(cfg.ListenAddr)
	location := fmt.Sprintf("http://%s:%s/description.xml", localIP, port)

	// Unicast socket used both to reply to M-SEARCH and to send periodic
	// NOTIFY alive announcements to the multicast group.
	outConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(localIP)})
	if err != nil {
		return fmt.Errorf("SSDP unicast socket failed: %w", err)
	}
	defer func() { _ = outConn.Close() }()

	go notifyLoop(outConn, groupAddr, cfg.UUID, location)

	buf := make([]byte, 2048)
	for {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("SSDP read error: %v", err)
			continue
		}
		msg := string(buf[:n])
		if !strings.HasPrefix(msg, "M-SEARCH") {
			continue
		}
		st := parseHeader(msg, "ST")
		if st != "ssdp:all" && st != deviceST && st != "upnp:rootdevice" && !strings.HasPrefix(st, "uuid:") {
			continue
		}
		go respondMSearch(outConn, src, cfg.UUID, location, st)
	}
}

func respondMSearch(conn *net.UDPConn, dst *net.UDPAddr, uuid, location, st string) {
	if st == "" {
		st = deviceST
	}
	resp := "HTTP/1.1 200 OK\r\n" +
		"CACHE-CONTROL: max-age=1800\r\n" +
		"EXT:\r\n" +
		"LOCATION: " + location + "\r\n" +
		"SERVER: Linux UPnP/1.0 immich-dlna-proxy/1.0\r\n" +
		"ST: " + st + "\r\n" +
		"USN: uuid:" + uuid + "::" + st + "\r\n" +
		"\r\n"
	if _, err := conn.WriteToUDP([]byte(resp), dst); err != nil {
		log.Printf("SSDP M-SEARCH reply failed: %v", err)
	}
}

func notifyLoop(conn *net.UDPConn, group *net.UDPAddr, uuid, location string) {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for {
		sendAlive(conn, group, uuid, location)
		<-ticker.C
	}
}

func sendAlive(conn *net.UDPConn, group *net.UDPAddr, uuid, location string) {
	nts := []string{"upnp:rootdevice", "uuid:" + uuid, deviceST}
	for _, nt := range nts {
		usn := "uuid:" + uuid
		if nt != "uuid:"+uuid {
			usn = "uuid:" + uuid + "::" + nt
		}
		notify := "NOTIFY * HTTP/1.1\r\n" +
			"HOST: 239.255.255.250:1900\r\n" +
			"CACHE-CONTROL: max-age=1800\r\n" +
			"LOCATION: " + location + "\r\n" +
			"NT: " + nt + "\r\n" +
			"NTS: ssdp:alive\r\n" +
			"SERVER: Linux UPnP/1.0 immich-dlna-proxy/1.0\r\n" +
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
