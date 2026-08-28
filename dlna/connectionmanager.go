package dlna

import (
	"encoding/xml"
	"io"
	"net/http"
)

type cmEnvelope struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    cmBody   `xml:"Body"`
}

type cmBody struct {
	GetProtocolInfo          *struct{} `xml:"GetProtocolInfo"`
	GetCurrentConnectionIDs  *struct{} `xml:"GetCurrentConnectionIDs"`
	GetCurrentConnectionInfo *struct {
		ConnectionID string `xml:"ConnectionID"`
	} `xml:"GetCurrentConnectionInfo"`
}

const cmNS = "urn:schemas-upnp-org:service:ConnectionManager:1"

func (s *Server) handleConnectionManagerControl(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var env cmEnvelope
	if err := xml.Unmarshal(raw, &env); err != nil {
		http.Error(w, "bad soap envelope", http.StatusBadRequest)
		return
	}

	switch {
	case env.Body.GetProtocolInfo != nil:
		writeSoapResponse(w, cmNS, "GetProtocolInfoResponse", map[string]string{
			"Source": "http-get:*:image/jpeg:*,http-get:*:image/png:*,http-get:*:image/gif:*," +
				"http-get:*:video/mp4:*,http-get:*:video/quicktime:*,http-get:*:video/x-matroska:*,http-get:*:video/x-msvideo:*",
			"Sink": "",
		})
	case env.Body.GetCurrentConnectionIDs != nil:
		writeSoapResponse(w, cmNS, "GetCurrentConnectionIDsResponse", map[string]string{
			"ConnectionIDs": "0",
		})
	case env.Body.GetCurrentConnectionInfo != nil:
		writeSoapResponse(w, cmNS, "GetCurrentConnectionInfoResponse", map[string]string{
			"RcsID":                 "-1",
			"AVTransportID":         "-1",
			"ProtocolInfo":          "",
			"PeerConnectionManager": "",
			"PeerConnectionID":      "-1",
			"Direction":             "Output",
			"Status":                "OK",
		})
	default:
		http.Error(w, "unsupported action", http.StatusNotImplemented)
	}
}
