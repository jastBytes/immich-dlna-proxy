package dlna

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jastBytes/immich-dlna-proxy/config"
	"github.com/jastBytes/immich-dlna-proxy/immich"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := &config.Config{ImmichURL: "http://immich.local", APIKeys: []string{"test-key"}, FriendlyName: "Test Server"}
	client := immich.New(cfg.ImmichURL, cfg.APIKeys[0])
	return NewServer(cfg, []UserClient{{Client: client}}, nil)
}

// soapPost posts a SOAP body to path on srv's mux and returns the raw
// response body and status code.
func soapPost(t *testing.T, srv *Server, path, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func TestHandleConnectionManagerControlGetProtocolInfo(t *testing.T) {
	srv := newTestServer(t)
	body := `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">
  <s:Body><u:GetProtocolInfo xmlns:u="urn:schemas-upnp-org:service:ConnectionManager:1"/></s:Body>
</s:Envelope>`

	code, resp := soapPost(t, srv, "/ctl/ConnectionManager", body)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", code, resp)
	}
	if !strings.Contains(resp, "GetProtocolInfoResponse") {
		t.Errorf("expected GetProtocolInfoResponse, got: %s", resp)
	}
	if !strings.Contains(resp, "http-get:*:image/jpeg:*") {
		t.Errorf("expected Source protocol info, got: %s", resp)
	}
}

func TestHandleConnectionManagerControlGetCurrentConnectionIDs(t *testing.T) {
	srv := newTestServer(t)
	body := `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">
  <s:Body><u:GetCurrentConnectionIDs xmlns:u="urn:schemas-upnp-org:service:ConnectionManager:1"/></s:Body>
</s:Envelope>`

	code, resp := soapPost(t, srv, "/ctl/ConnectionManager", body)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", code, resp)
	}
	if !strings.Contains(resp, "<ConnectionIDs>0</ConnectionIDs>") {
		t.Errorf("expected ConnectionIDs 0, got: %s", resp)
	}
}

func TestHandleConnectionManagerControlGetCurrentConnectionInfo(t *testing.T) {
	srv := newTestServer(t)
	body := `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">
  <s:Body><u:GetCurrentConnectionInfo xmlns:u="urn:schemas-upnp-org:service:ConnectionManager:1">
    <ConnectionID>0</ConnectionID>
  </u:GetCurrentConnectionInfo></s:Body>
</s:Envelope>`

	code, resp := soapPost(t, srv, "/ctl/ConnectionManager", body)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", code, resp)
	}
	for _, want := range []string{"<RcsID>-1</RcsID>", "<Status>OK</Status>", "<Direction>Output</Direction>"} {
		if !strings.Contains(resp, want) {
			t.Errorf("expected %s in response, got: %s", want, resp)
		}
	}
}

func TestHandleConnectionManagerControlUnsupportedAction(t *testing.T) {
	srv := newTestServer(t)
	body := `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">
  <s:Body><u:SomeUnknownAction xmlns:u="urn:schemas-upnp-org:service:ConnectionManager:1"/></s:Body>
</s:Envelope>`

	code, _ := soapPost(t, srv, "/ctl/ConnectionManager", body)
	if code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", code)
	}
}

func TestHandleConnectionManagerControlBadEnvelope(t *testing.T) {
	srv := newTestServer(t)
	code, _ := soapPost(t, srv, "/ctl/ConnectionManager", "not xml at all")
	if code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
}
