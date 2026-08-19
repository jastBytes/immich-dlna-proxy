package dlna

import (
	"net"
	"strings"
	"testing"
	"time"
)

func TestSearchTargets(t *testing.T) {
	targets := searchTargets("test-uuid")
	want := []string{"upnp:rootdevice", "uuid:test-uuid", deviceST, cdNS, cmNS, mrrNS}
	if len(targets) != len(want) {
		t.Fatalf("got %d targets, want %d: %v", len(targets), len(want), targets)
	}
	for i, w := range want {
		if targets[i] != w {
			t.Errorf("targets[%d] = %q, want %q", i, targets[i], w)
		}
	}
}

func TestParseHeader(t *testing.T) {
	msg := "M-SEARCH * HTTP/1.1\r\n" +
		"HOST: 239.255.255.250:1900\r\n" +
		"ST: ssdp:all\r\n" +
		"MAN: \"ssdp:discover\"\r\n" +
		"MX: 3\r\n\r\n"

	cases := []struct {
		name string
		want string
	}{
		{"ST", "ssdp:all"},
		{"st", "ssdp:all"}, // case-insensitive header name
		{"HOST", "239.255.255.250:1900"},
		{"MX", "3"},
		{"NoSuchHeader", ""},
	}
	for _, c := range cases {
		if got := parseHeader(msg, c.name); got != c.want {
			t.Errorf("parseHeader(_, %q) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestParseHeaderIgnoresMalformedLines(t *testing.T) {
	msg := "M-SEARCH * HTTP/1.1\r\nnotaheader\r\nST: upnp:rootdevice\r\n\r\n"
	if got := parseHeader(msg, "ST"); got != "upnp:rootdevice" {
		t.Errorf("parseHeader = %q, want upnp:rootdevice", got)
	}
}

func TestPortFromAddr(t *testing.T) {
	cases := []struct {
		addr string
		want string
	}{
		{":8200", "8200"},
		{"0.0.0.0:9000", "9000"},
		{"localhost:1234", "1234"},
		{"no-port-here", "8200"},
		{"", "8200"},
	}
	for _, c := range cases {
		if got := portFromAddr(c.addr); got != c.want {
			t.Errorf("portFromAddr(%q) = %q, want %q", c.addr, got, c.want)
		}
	}
}

func TestDetectLocalIP(t *testing.T) {
	ip, err := detectLocalIP()
	if err != nil {
		t.Fatalf("detectLocalIP() error: %v", err)
	}
	if net.ParseIP(ip) == nil {
		t.Errorf("detectLocalIP() = %q, not a valid IP", ip)
	}
}

// udpPair opens two loopback UDP sockets: one to send from ("out"), one to
// receive on ("in"). Using real sockets rather than mocking lets these
// tests exercise the exact bytes written on the wire.
func udpPair(t *testing.T) (out, in *net.UDPConn) {
	t.Helper()
	loopback := &net.UDPAddr{IP: net.ParseIP("127.0.0.1")}

	out, err := net.ListenUDP("udp4", loopback)
	if err != nil {
		t.Fatalf("listen out: %v", err)
	}
	t.Cleanup(func() { _ = out.Close() })

	in, err = net.ListenUDP("udp4", loopback)
	if err != nil {
		t.Fatalf("listen in: %v", err)
	}
	t.Cleanup(func() { _ = in.Close() })

	return out, in
}

func readOnePacket(t *testing.T, conn *net.UDPConn) string {
	t.Helper()
	buf := make([]byte, 4096)
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read packet: %v", err)
	}
	return string(buf[:n])
}

func TestSendSearchReplyTargetedST(t *testing.T) {
	out, in := udpPair(t)

	sendSearchReply(out, in.LocalAddr().(*net.UDPAddr), "test-uuid", "http://1.2.3.4:8200/description.xml", cdNS)

	pkt := readOnePacket(t, in)
	if !strings.HasPrefix(pkt, "HTTP/1.1 200 OK\r\n") {
		t.Errorf("expected HTTP 200 status line, got: %s", pkt)
	}
	if !strings.Contains(pkt, "ST: "+cdNS+"\r\n") {
		t.Errorf("expected ST header %q, got: %s", cdNS, pkt)
	}
	if !strings.Contains(pkt, "USN: uuid:test-uuid::"+cdNS+"\r\n") {
		t.Errorf("expected composite USN for non-uuid ST, got: %s", pkt)
	}
	if !strings.Contains(pkt, "LOCATION: http://1.2.3.4:8200/description.xml\r\n") {
		t.Errorf("expected LOCATION header, got: %s", pkt)
	}
}

func TestSendSearchReplyUUIDOnlyST(t *testing.T) {
	out, in := udpPair(t)

	st := "uuid:test-uuid"
	sendSearchReply(out, in.LocalAddr().(*net.UDPAddr), "test-uuid", "http://1.2.3.4:8200/description.xml", st)

	pkt := readOnePacket(t, in)
	// When ST equals the bare uuid, USN must NOT get a "::ST" suffix.
	if !strings.Contains(pkt, "USN: uuid:test-uuid\r\n") {
		t.Errorf("expected bare USN for uuid ST, got: %s", pkt)
	}
	if strings.Contains(pkt, "uuid:test-uuid::") {
		t.Errorf("USN should not have a ::suffix when ST is the bare uuid, got: %s", pkt)
	}
}

func TestRespondMSearchTargetedSendsOneReply(t *testing.T) {
	out, in := udpPair(t)
	targets := searchTargets("test-uuid")

	respondMSearch(out, in.LocalAddr().(*net.UDPAddr), "test-uuid", "http://loc", cdNS, targets)

	pkt := readOnePacket(t, in)
	if !strings.Contains(pkt, "ST: "+cdNS+"\r\n") {
		t.Errorf("expected single reply for ST %q, got: %s", cdNS, pkt)
	}

	// No second packet should follow.
	_ = in.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	buf := make([]byte, 64)
	if _, err := in.Read(buf); err == nil {
		t.Error("expected only one reply for a targeted search, got a second packet")
	}
}

func TestRespondMSearchAllSendsOneReplyPerTarget(t *testing.T) {
	out, in := udpPair(t)
	targets := searchTargets("test-uuid")

	respondMSearch(out, in.LocalAddr().(*net.UDPAddr), "test-uuid", "http://loc", "ssdp:all", targets)

	seen := make(map[string]bool)
	for range targets {
		pkt := readOnePacket(t, in)
		st := ""
		for _, line := range strings.Split(pkt, "\r\n") {
			if strings.HasPrefix(line, "ST: ") {
				st = strings.TrimPrefix(line, "ST: ")
			}
		}
		seen[st] = true
	}
	for _, target := range targets {
		if !seen[target] {
			t.Errorf("expected a reply with ST %q, got replies for: %v", target, seen)
		}
	}
}

func TestSendAliveNotifiesEveryTarget(t *testing.T) {
	out, in := udpPair(t)
	targets := searchTargets("test-uuid")

	sendAlive(out, in.LocalAddr().(*net.UDPAddr), "test-uuid", "http://loc")

	seen := make(map[string]bool)
	for range targets {
		pkt := readOnePacket(t, in)
		if !strings.HasPrefix(pkt, "NOTIFY * HTTP/1.1\r\n") {
			t.Errorf("expected NOTIFY request line, got: %s", pkt)
		}
		if !strings.Contains(pkt, "NTS: ssdp:alive\r\n") {
			t.Errorf("expected NTS: ssdp:alive, got: %s", pkt)
		}
		nt := ""
		for _, line := range strings.Split(pkt, "\r\n") {
			if strings.HasPrefix(line, "NT: ") {
				nt = strings.TrimPrefix(line, "NT: ")
			}
		}
		seen[nt] = true
	}
	for _, target := range targets {
		if !seen[target] {
			t.Errorf("expected a NOTIFY with NT %q, got: %v", target, seen)
		}
	}
}
