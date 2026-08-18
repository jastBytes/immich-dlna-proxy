package dlna

import (
	"encoding/xml"
	"io"
	"net/http"
)

// X_MS_MediaReceiverRegistrar is a Microsoft-defined UPnP extension that
// several DLNA clients (Xbox, Windows Media Player, and a number of
// Samsung TV firmwares) use to check whether they're "authorized" to see
// a media server's content before browsing it. If the service isn't
// advertised in the device description at all, these clients silently
// treat the server as having no content instead of erroring - so we
// advertise it and always answer "yes, authorized".
const mrrNS = "urn:microsoft.com:service:X_MS_MediaReceiverRegistrar:1"

const mediaReceiverRegistrarSCPD = `<?xml version="1.0" encoding="UTF-8"?>
<scpd xmlns="urn:schemas-upnp-org:service-1-0">
  <specVersion><major>1</major><minor>0</minor></specVersion>
  <actionList>
    <action>
      <name>IsAuthorized</name>
      <argumentList>
        <argument><name>DeviceID</name><direction>in</direction><relatedStateVariable>A_ARG_TYPE_DeviceID</relatedStateVariable></argument>
        <argument><name>Result</name><direction>out</direction><relatedStateVariable>A_ARG_TYPE_Result</relatedStateVariable></argument>
      </argumentList>
    </action>
    <action>
      <name>IsValidated</name>
      <argumentList>
        <argument><name>DeviceID</name><direction>in</direction><relatedStateVariable>A_ARG_TYPE_DeviceID</relatedStateVariable></argument>
        <argument><name>Result</name><direction>out</direction><relatedStateVariable>A_ARG_TYPE_Result</relatedStateVariable></argument>
      </argumentList>
    </action>
    <action>
      <name>RegisterDevice</name>
      <argumentList>
        <argument><name>RegistrationReqMsg</name><direction>in</direction><relatedStateVariable>A_ARG_TYPE_RegistrationReqMsg</relatedStateVariable></argument>
        <argument><name>RegistrationRespMsg</name><direction>out</direction><relatedStateVariable>A_ARG_TYPE_RegistrationRespMsg</relatedStateVariable></argument>
      </argumentList>
    </action>
  </actionList>
  <serviceStateTable>
    <stateVariable sendEvents="no"><name>A_ARG_TYPE_DeviceID</name><dataType>string</dataType></stateVariable>
    <stateVariable sendEvents="no"><name>A_ARG_TYPE_Result</name><dataType>int</dataType></stateVariable>
    <stateVariable sendEvents="no"><name>A_ARG_TYPE_RegistrationReqMsg</name><dataType>bin.base64</dataType></stateVariable>
    <stateVariable sendEvents="no"><name>A_ARG_TYPE_RegistrationRespMsg</name><dataType>bin.base64</dataType></stateVariable>
    <stateVariable sendEvents="yes"><name>AuthorizationGrantedUpdateID</name><dataType>ui4</dataType></stateVariable>
    <stateVariable sendEvents="yes"><name>AuthorizationDeniedUpdateID</name><dataType>ui4</dataType></stateVariable>
    <stateVariable sendEvents="yes"><name>ValidationSucceededUpdateID</name><dataType>ui4</dataType></stateVariable>
    <stateVariable sendEvents="yes"><name>ValidationRevokedUpdateID</name><dataType>ui4</dataType></stateVariable>
  </serviceStateTable>
</scpd>`

type mrrEnvelope struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    mrrBody  `xml:"Body"`
}

type mrrBody struct {
	IsAuthorized   *struct{} `xml:"IsAuthorized"`
	IsValidated    *struct{} `xml:"IsValidated"`
	RegisterDevice *struct{} `xml:"RegisterDevice"`
}

func (s *Server) handleMediaReceiverRegistrarSCPD(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	w.Write([]byte(mediaReceiverRegistrarSCPD))
}

func (s *Server) handleMediaReceiverRegistrarControl(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var env mrrEnvelope
	if err := xml.Unmarshal(raw, &env); err != nil {
		http.Error(w, "bad soap envelope", http.StatusBadRequest)
		return
	}

	switch {
	case env.Body.IsAuthorized != nil:
		writeSoapResponse(w, mrrNS, "IsAuthorizedResponse", map[string]string{"Result": "1"})
	case env.Body.IsValidated != nil:
		writeSoapResponse(w, mrrNS, "IsValidatedResponse", map[string]string{"Result": "1"})
	case env.Body.RegisterDevice != nil:
		writeSoapResponse(w, mrrNS, "RegisterDeviceResponse", map[string]string{"RegistrationRespMsg": ""})
	default:
		http.Error(w, "unsupported action", http.StatusNotImplemented)
	}
}
