package dlna

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleMediaReceiverRegistrarSCPD(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/X_MS_MediaReceiverRegistrar.xml", nil)
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "IsAuthorized") {
		t.Errorf("expected SCPD to describe IsAuthorized action, got: %s", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != `text/xml; charset="utf-8"` {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestHandleMediaReceiverRegistrarControlIsAuthorized(t *testing.T) {
	srv := newTestServer(t)
	body := `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">
  <s:Body><u:IsAuthorized xmlns:u="urn:microsoft.com:service:X_MS_MediaReceiverRegistrar:1">
    <DeviceID></DeviceID>
  </u:IsAuthorized></s:Body>
</s:Envelope>`

	code, resp := soapPost(t, srv, "/ctl/X_MS_MediaReceiverRegistrar", body)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", code, resp)
	}
	if !strings.Contains(resp, "IsAuthorizedResponse") || !strings.Contains(resp, "<Result>1</Result>") {
		t.Errorf("expected IsAuthorizedResponse with Result 1, got: %s", resp)
	}
}

func TestHandleMediaReceiverRegistrarControlIsValidated(t *testing.T) {
	srv := newTestServer(t)
	body := `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">
  <s:Body><u:IsValidated xmlns:u="urn:microsoft.com:service:X_MS_MediaReceiverRegistrar:1">
    <DeviceID></DeviceID>
  </u:IsValidated></s:Body>
</s:Envelope>`

	code, resp := soapPost(t, srv, "/ctl/X_MS_MediaReceiverRegistrar", body)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", code, resp)
	}
	if !strings.Contains(resp, "IsValidatedResponse") || !strings.Contains(resp, "<Result>1</Result>") {
		t.Errorf("expected IsValidatedResponse with Result 1, got: %s", resp)
	}
}

func TestHandleMediaReceiverRegistrarControlRegisterDevice(t *testing.T) {
	srv := newTestServer(t)
	body := `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">
  <s:Body><u:RegisterDevice xmlns:u="urn:microsoft.com:service:X_MS_MediaReceiverRegistrar:1">
    <RegistrationReqMsg></RegistrationReqMsg>
  </u:RegisterDevice></s:Body>
</s:Envelope>`

	code, resp := soapPost(t, srv, "/ctl/X_MS_MediaReceiverRegistrar", body)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", code, resp)
	}
	if !strings.Contains(resp, "RegisterDeviceResponse") {
		t.Errorf("expected RegisterDeviceResponse, got: %s", resp)
	}
}

func TestHandleMediaReceiverRegistrarControlUnsupportedAction(t *testing.T) {
	srv := newTestServer(t)
	body := `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">
  <s:Body><u:SomeUnknownAction xmlns:u="urn:microsoft.com:service:X_MS_MediaReceiverRegistrar:1"/></s:Body>
</s:Envelope>`

	code, _ := soapPost(t, srv, "/ctl/X_MS_MediaReceiverRegistrar", body)
	if code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", code)
	}
}

func TestHandleMediaReceiverRegistrarControlBadEnvelope(t *testing.T) {
	srv := newTestServer(t)
	code, _ := soapPost(t, srv, "/ctl/X_MS_MediaReceiverRegistrar", "not xml at all")
	if code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
}
