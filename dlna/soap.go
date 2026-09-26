package dlna

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/jastBytes/immich-dlna-proxy/immich"
)

// maxSOAPBodyBytes caps how much of a SOAP request body is read. Real
// control requests are well under 2 KB; without a cap, any device on the
// network could make the proxy buffer an arbitrarily large body.
const maxSOAPBodyBytes = 64 << 10

// readSOAPBody reads a SOAP request body up to maxSOAPBodyBytes, having
// already answered the request (400/413) if it returns false.
func readSOAPBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSOAPBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return nil, false
		}
		http.Error(w, "bad request", http.StatusBadRequest)
		return nil, false
	}
	return raw, true
}

// upnpError is a UPnP action error, reported to the control point as a
// SOAP fault (UPnP Device Architecture 1.0, section 3.2.2).
type upnpError struct {
	code int
	desc string
}

func (e *upnpError) Error() string { return fmt.Sprintf("UPnP error %d: %s", e.code, e.desc) }

var (
	// errNoSuchObject is ContentDirectory's "701 No such object" - an
	// ObjectID that doesn't exist (or a malformed one).
	errNoSuchObject = &upnpError{701, "No such object"}
	// errActionFailed is the generic "501 Action Failed", used when Immich
	// couldn't be reached or answered with an error.
	errActionFailed = &upnpError{501, "Action Failed"}
)

// writeBrowseError reports a failed Browse/Search as a SOAP fault. Errors
// from Immich become "701 No such object" when Immich says the object
// doesn't exist, and "501 Action Failed" otherwise - control points
// handle a proper fault gracefully, whereas a bare HTTP error page is
// something several TV stacks treat as the whole server being broken.
func writeBrowseError(w http.ResponseWriter, objectID string, err error) {
	var ue *upnpError
	switch {
	case errors.As(err, &ue):
	case immich.IsNotFound(err):
		log.Printf("Browse %s: %v", objectID, err)
		ue = errNoSuchObject
	default:
		log.Printf("Browse %s failed: %v", objectID, err)
		ue = errActionFailed
	}
	writeSoapFault(w, ue)
}

// writeSoapFault writes a UPnP SOAP fault. Per the UPnP spec that's an
// HTTP 500 carrying the UPnP error code and description in the fault's
// <detail>.
func writeSoapFault(w http.ResponseWriter, e *upnpError) {
	envelope := "<?xml version=\"1.0\" encoding=\"utf-8\"?>\n" +
		`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" ` +
		`s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
		`<s:Body><s:Fault>` +
		`<faultcode>s:Client</faultcode>` +
		`<faultstring>UPnPError</faultstring>` +
		`<detail><UPnPError xmlns="urn:schemas-upnp-org:control-1-0">` +
		fmt.Sprintf(`<errorCode>%d</errorCode><errorDescription>%s</errorDescription>`, e.code, xmlTextEscape(e.desc)) +
		`</UPnPError></detail>` +
		`</s:Fault></s:Body></s:Envelope>`
	writeRawUPnPResponseStatus(w, http.StatusInternalServerError, envelope)
}
