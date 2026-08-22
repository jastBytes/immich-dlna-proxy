package dlna

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jastBytes/immich-dlna-proxy/config"
	"github.com/jastBytes/immich-dlna-proxy/immich"
)

func TestHandleDescription(t *testing.T) {
	cfg := &config.Config{
		ImmichURL:    "http://immich.local",
		APIKeys:      []string{"test-key"},
		FriendlyName: `My "Photos" & <Stuff>`,
		UUID:         "test-uuid-1234",
	}
	client := immich.New(cfg.ImmichURL, cfg.APIKeys[0])
	srv := NewServer(cfg, []UserClient{{Client: client}}, nil)

	req := httptest.NewRequest(http.MethodGet, "/description.xml", nil)
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != `text/xml; charset="utf-8"` {
		t.Errorf("Content-Type = %q", ct)
	}
	body := rec.Body.String()

	if !strings.Contains(body, "<UDN>uuid:test-uuid-1234</UDN>") {
		t.Errorf("expected UDN with configured UUID, got: %s", body)
	}
	// The friendly name must be escaped, since it goes inside an XML text
	// node - unescaped quotes/ampersands/angle brackets would break parsing.
	if strings.Contains(body, `My "Photos" & <Stuff>`) {
		t.Errorf("friendlyName was not escaped: %s", body)
	}
	if !strings.Contains(body, "My &#34;Photos&#34; &amp; &lt;Stuff&gt;") {
		t.Errorf("expected escaped friendlyName, got: %s", body)
	}
	for _, want := range []string{
		"urn:schemas-upnp-org:service:ContentDirectory:1",
		"urn:schemas-upnp-org:service:ConnectionManager:1",
		"urn:microsoft.com:service:X_MS_MediaReceiverRegistrar:1",
		"/ctl/ContentDirectory",
		"/ctl/ConnectionManager",
		"/ctl/X_MS_MediaReceiverRegistrar",
		"<iconList>",
		"/icon48.png",
		"/icon120.png",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected description to mention %q, got: %s", want, body)
		}
	}
}

func TestHandleIcons(t *testing.T) {
	srv := newTestServer(t)

	for _, path := range []string{"/icon48.png", "/icon120.png"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.Mux().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
			t.Errorf("%s: Content-Type = %q", path, ct)
		}
		if !bytes.HasPrefix(rec.Body.Bytes(), []byte("\x89PNG\r\n\x1a\n")) {
			t.Errorf("%s: body does not start with the PNG signature", path)
		}
	}
}

func TestHandleContentDirectorySCPD(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/ContentDirectory.xml", nil)
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != `text/xml; charset="utf-8"` {
		t.Errorf("Content-Type = %q", ct)
	}
	body := rec.Body.String()
	for _, action := range []string{"Browse", "GetSearchCapabilities", "GetSortCapabilities", "GetSystemUpdateID"} {
		if !strings.Contains(body, "<name>"+action+"</name>") {
			t.Errorf("expected SCPD to list action %q, got: %s", action, body)
		}
	}
}

func TestHandleConnectionManagerSCPD(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/ConnectionManager.xml", nil)
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != `text/xml; charset="utf-8"` {
		t.Errorf("Content-Type = %q", ct)
	}
	body := rec.Body.String()
	for _, action := range []string{"GetProtocolInfo", "GetCurrentConnectionIDs", "GetCurrentConnectionInfo"} {
		if !strings.Contains(body, "<name>"+action+"</name>") {
			t.Errorf("expected SCPD to list action %q, got: %s", action, body)
		}
	}
}
