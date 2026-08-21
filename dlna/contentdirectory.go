package dlna

import (
	"bufio"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jastBytes/immich-dlna-proxy/immich"
)

type cdEnvelope struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    cdBody   `xml:"Body"`
}

type cdBody struct {
	Browse                *browseArgs `xml:"Browse"`
	Search                *searchArgs `xml:"Search"`
	GetSearchCapabilities *struct{}   `xml:"GetSearchCapabilities"`
	GetSortCapabilities   *struct{}   `xml:"GetSortCapabilities"`
	GetSystemUpdateID     *struct{}   `xml:"GetSystemUpdateID"`
}

type browseArgs struct {
	ObjectID       string `xml:"ObjectID"`
	BrowseFlag     string `xml:"BrowseFlag"`
	Filter         string `xml:"Filter"`
	StartingIndex  int    `xml:"StartingIndex"`
	RequestedCount int    `xml:"RequestedCount"`
	SortCriteria   string `xml:"SortCriteria"`
}

// searchArgs mirrors Search's arguments. We don't parse SearchCriteria - we
// have no way to run an arbitrary DLNA search expression against Immich, so
// Search is handled identically to a BrowseDirectChildren on ContainerID
// (matching this repo's convention of passing through rather than erroring
// on unsupported inputs). Some DLNA clients (e.g. Samsung's SEC_HHP stack)
// only consider a ContentDirectory fully capable once Search is present in
// the service description at all, regardless of whether they ever send one.
type searchArgs struct {
	ContainerID    string `xml:"ContainerID"`
	SearchCriteria string `xml:"SearchCriteria"`
	Filter         string `xml:"Filter"`
	StartingIndex  int    `xml:"StartingIndex"`
	RequestedCount int    `xml:"RequestedCount"`
	SortCriteria   string `xml:"SortCriteria"`
}

const cdNS = "urn:schemas-upnp-org:service:ContentDirectory:1"

func (s *Server) handleContentDirectoryControl(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var env cdEnvelope
	if err := xml.Unmarshal(raw, &env); err != nil {
		http.Error(w, "bad soap envelope", http.StatusBadRequest)
		return
	}

	switch {
	case env.Body.Browse != nil:
		s.handleBrowse(w, r, env.Body.Browse, "BrowseResponse")
	case env.Body.Search != nil:
		s.handleBrowse(w, r, &browseArgs{
			ObjectID:       env.Body.Search.ContainerID,
			BrowseFlag:     "BrowseDirectChildren",
			Filter:         env.Body.Search.Filter,
			StartingIndex:  env.Body.Search.StartingIndex,
			RequestedCount: env.Body.Search.RequestedCount,
			SortCriteria:   env.Body.Search.SortCriteria,
		}, "SearchResponse")
	case env.Body.GetSearchCapabilities != nil:
		writeSoapResponse(w, cdNS, "GetSearchCapabilitiesResponse", map[string]string{"SearchCaps": "dc:title,upnp:class"})
	case env.Body.GetSortCapabilities != nil:
		writeSoapResponse(w, cdNS, "GetSortCapabilitiesResponse", map[string]string{"SortCaps": "dc:title"})
	case env.Body.GetSystemUpdateID != nil:
		writeSoapResponse(w, cdNS, "GetSystemUpdateIDResponse", map[string]string{"Id": "1"})
	default:
		http.Error(w, "unsupported action", http.StatusNotImplemented)
	}
}

func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request, args *browseArgs, responseName string) {
	baseURL := "http://" + r.Host

	objectID := args.ObjectID
	if objectID == "" {
		objectID = "0"
	}

	var didl string
	var returned, total int

	switch {
	case objectID == "0" && args.BrowseFlag == "BrowseMetadata":
		didl = wrapDIDL(buildContainer("0", "-1", s.cfg.FriendlyName, 2))
		returned, total = 1, 1

	case objectID == "0": // BrowseDirectChildren on root: fixed "Albums" / "People" folders
		albums, err := s.immich.ListAlbums()
		if err != nil {
			log.Printf("ListAlbums failed: %v", err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return
		}
		people, err := s.immich.ListPeople()
		if err != nil {
			log.Printf("ListPeople failed: %v", err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return
		}
		namedPeople := countNamedPeople(people)

		fragments := []string{
			buildContainer("albums", "0", "Albums", len(albums)),
			buildContainer("people", "0", "People", namedPeople),
		}
		total = len(fragments)
		fragments = page(fragments, args.StartingIndex, args.RequestedCount)
		didl = wrapDIDL(strings.Join(fragments, ""))
		returned = len(fragments)

	case objectID == "albums" && args.BrowseFlag == "BrowseMetadata":
		albums, err := s.immich.ListAlbums()
		if err != nil {
			log.Printf("ListAlbums failed: %v", err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return
		}
		didl = wrapDIDL(buildContainer("albums", "0", "Albums", len(albums)))
		returned, total = 1, 1

	case objectID == "albums": // BrowseDirectChildren: list albums
		albums, err := s.immich.ListAlbums()
		if err != nil {
			log.Printf("ListAlbums failed: %v", err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return
		}
		desc, sortOK := parseSortCriteria(args.SortCriteria)
		sortByTitle(albums, func(a immich.Album) string { return a.AlbumName }, desc, sortOK)
		total = len(albums)
		paged := page(albums, args.StartingIndex, args.RequestedCount)
		var b strings.Builder
		for _, a := range paged {
			b.WriteString(buildContainer("album:"+a.ID, "albums", a.AlbumName, a.AssetCount))
		}
		didl = wrapDIDL(b.String())
		returned = len(paged)

	case objectID == "people" && args.BrowseFlag == "BrowseMetadata":
		people, err := s.immich.ListPeople()
		if err != nil {
			log.Printf("ListPeople failed: %v", err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return
		}
		didl = wrapDIDL(buildContainer("people", "0", "People", countNamedPeople(people)))
		returned, total = 1, 1

	case objectID == "people": // BrowseDirectChildren: list named people
		people, err := s.immich.ListPeople()
		if err != nil {
			log.Printf("ListPeople failed: %v", err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return
		}
		named := make([]immich.Person, 0, len(people))
		for _, p := range people {
			if p.IsNamed() {
				named = append(named, p)
			}
		}
		desc, sortOK := parseSortCriteria(args.SortCriteria)
		sortByTitle(named, func(p immich.Person) string { return p.Name }, desc, sortOK)
		total = len(named)
		paged := page(named, args.StartingIndex, args.RequestedCount)
		var b strings.Builder
		for _, p := range paged {
			// childCount omitted (-1): knowing it accurately would need one
			// GetPersonStatistics call per person, which doesn't scale for
			// libraries with many tagged people.
			b.WriteString(buildContainer("person:"+p.ID, "people", p.Name, -1))
		}
		didl = wrapDIDL(b.String())
		returned = len(paged)

	case strings.HasPrefix(objectID, "album:"):
		albumID := strings.TrimPrefix(objectID, "album:")
		album, err := s.immich.GetAlbum(albumID)
		if err != nil {
			log.Printf("GetAlbum(%s) failed: %v", albumID, err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return
		}
		assets, err := s.immich.GetAlbumAssets(albumID)
		if err != nil {
			log.Printf("GetAlbumAssets(%s) failed: %v", albumID, err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return
		}
		photos := filterPhotos(assets)

		if args.BrowseFlag == "BrowseMetadata" {
			didl = wrapDIDL(buildContainer(objectID, "albums", album.AlbumName, len(photos)))
			returned, total = 1, 1
		} else {
			desc, sortOK := parseSortCriteria(args.SortCriteria)
			sortByTitle(photos, func(a immich.Asset) string { return a.OriginalFileName }, desc, sortOK)
			total = len(photos)
			paged := page(photos, args.StartingIndex, args.RequestedCount)
			var b strings.Builder
			for _, a := range paged {
				resURL := baseURL + "/media/" + a.ID
				b.WriteString(buildPhotoItem("asset:"+a.ID, objectID, a.OriginalFileName, a.OriginalMimeType, resURL))
			}
			didl = wrapDIDL(b.String())
			returned = len(paged)
		}

	case strings.HasPrefix(objectID, "person:"):
		personID := strings.TrimPrefix(objectID, "person:")

		if args.BrowseFlag == "BrowseMetadata" {
			person, err := s.immich.GetPerson(personID)
			if err != nil {
				log.Printf("GetPerson(%s) failed: %v", personID, err)
				http.Error(w, "upstream error", http.StatusBadGateway)
				return
			}
			didl = wrapDIDL(buildContainer(objectID, "people", person.Name, -1))
			returned, total = 1, 1
		} else {
			assets, err := s.immich.GetPersonAssets(personID)
			if err != nil {
				log.Printf("GetPersonAssets(%s) failed: %v", personID, err)
				http.Error(w, "upstream error", http.StatusBadGateway)
				return
			}
			photos := filterPhotos(assets)
			desc, sortOK := parseSortCriteria(args.SortCriteria)
			sortByTitle(photos, func(a immich.Asset) string { return a.OriginalFileName }, desc, sortOK)
			total = len(photos)
			paged := page(photos, args.StartingIndex, args.RequestedCount)
			var b strings.Builder
			for _, a := range paged {
				resURL := baseURL + "/media/" + a.ID
				b.WriteString(buildPhotoItem("asset:"+a.ID, objectID, a.OriginalFileName, a.OriginalMimeType, resURL))
			}
			didl = wrapDIDL(b.String())
			returned = len(paged)
		}

	case strings.HasPrefix(objectID, "asset:"):
		assetID := strings.TrimPrefix(objectID, "asset:")
		asset, err := s.immich.GetAsset(assetID)
		if err != nil {
			log.Printf("GetAsset(%s) failed: %v", assetID, err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return
		}
		resURL := baseURL + "/media/" + asset.ID
		didl = wrapDIDL(buildPhotoItem(objectID, "0", asset.OriginalFileName, asset.OriginalMimeType, resURL))
		returned, total = 1, 1

	default:
		http.Error(w, "unknown object", http.StatusNotFound)
		return
	}

	if !strings.HasPrefix(objectID, "asset:") {
		log.Printf("Browse %s %s -> %d/%d items", objectID, args.BrowseFlag, returned, total)
	}

	writeSoapResponse(w, cdNS, responseName, map[string]string{
		"Result":         didl,
		"NumberReturned": strconv.Itoa(returned),
		"TotalMatches":   strconv.Itoa(total),
		"UpdateID":       "1",
	})
}

// parseSortCriteria extracts an ascending/descending request for
// "dc:title" out of a DLNA SortCriteria string (e.g. "+dc:title" or
// "-dc:title,+upnp:originalTrackNumber" - comma-separated, each entry
// prefixed with + or -). dc:title is the only sortable property this
// server advertises via GetSortCapabilities, since it's the only text
// value every container/item DIDL-Lite fragment carries; any other
// property in the criteria is ignored rather than rejected, matching
// this repo's pass-through-on-unsupported-input convention. ok is false
// when no recognized property is present, meaning "leave Immich's own
// order alone".
func parseSortCriteria(criteria string) (descending, ok bool) {
	for _, part := range strings.Split(criteria, ",") {
		switch strings.TrimSpace(part) {
		case "+dc:title":
			return false, true
		case "-dc:title":
			return true, true
		}
	}
	return false, false
}

// sortByTitle sorts items in place by a case-insensitive comparison of the
// string title returns for each, applying it only when SortCriteria asked
// for dc:title (ok==false leaves the slice - and thus Immich's own
// ordering - untouched).
func sortByTitle[T any](items []T, title func(T) string, descending, ok bool) {
	if !ok {
		return
	}
	slices.SortFunc(items, func(a, b T) int {
		c := strings.Compare(strings.ToLower(title(a)), strings.ToLower(title(b)))
		if descending {
			return -c
		}
		return c
	})
}

// page applies UPnP ContentDirectory Browse pagination: RequestedCount 0
// means "no limit". Ignoring StartingIndex/RequestedCount (as this server
// used to) meant a client that paginates - anything browsing a container
// with more items than fit in one page - received the identical full list
// on every page instead of successive slices of it.
func page[T any](items []T, startingIndex, requestedCount int) []T {
	if startingIndex < 0 || startingIndex >= len(items) {
		return nil
	}
	end := len(items)
	if requestedCount > 0 && startingIndex+requestedCount < end {
		end = startingIndex + requestedCount
	}
	return items[startingIndex:end]
}

// filterPhotos keeps only the photo assets (see Asset.IsPhoto) - we don't
// support serving/playing videos yet.
func filterPhotos(assets []immich.Asset) []immich.Asset {
	photos := make([]immich.Asset, 0, len(assets))
	for _, a := range assets {
		if a.IsPhoto() {
			photos = append(photos, a)
		}
	}
	return photos
}

func countNamedPeople(people []immich.Person) int {
	n := 0
	for _, p := range people {
		if p.IsNamed() {
			n++
		}
	}
	return n
}

// xmlTextEscape escapes a string for use as XML element text content,
// handling only the characters that are ever mandatory to escape there (&
// and <; > is included for readability/safety but isn't required by the
// spec). Deliberately not html.EscapeString: that also converts quotes to
// &#34;/&#39;, which is correct but unnecessary in text content - and a
// packet capture showed a real, working minidlna instance leaves quotes
// unescaped when embedding a DIDL-Lite fragment (itself full of quoted
// attributes) inside <Result>. Some DLNA clients (confirmed: the Samsung
// SEC_HHP stack) apparently only undo &lt;/&gt;/&amp; before treating the
// result as raw XML, so a Result value earlier escaped with html.EscapeString
// left every attribute as e.g. id=&#34;0&#34; after that partial unescape -
// invalid attribute syntax, silently breaking every container's attributes
// including childCount, which is why the client never had anything worth
// descending into.
func xmlTextEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// writeSoapResponse wraps arbitrary out-arguments in a SOAP 1.1 envelope,
// escaping each value for use as XML text content.
func writeSoapResponse(w http.ResponseWriter, serviceNS, actionResponseName string, args map[string]string) {
	// Preserve a stable, spec-friendly argument order for the actions we use.
	order := []string{"Result", "NumberReturned", "TotalMatches", "UpdateID",
		"SearchCaps", "SortCaps", "Id", "Source", "Sink", "ConnectionIDs", "RegistrationRespMsg",
		"RcsID", "AVTransportID", "ProtocolInfo", "PeerConnectionManager",
		"PeerConnectionID", "Direction", "Status"}

	var body strings.Builder
	fmt.Fprintf(&body, `<u:%s xmlns:u="%s">`, actionResponseName, serviceNS)
	for _, k := range order {
		if v, ok := args[k]; ok {
			fmt.Fprintf(&body, "<%s>%s</%s>", k, xmlTextEscape(v), k)
		}
	}
	fmt.Fprintf(&body, `</u:%s>`, actionResponseName)

	envelope := "<?xml version=\"1.0\" encoding=\"utf-8\"?>\n" +
		`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" ` +
		`s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
		`<s:Body>` + body.String() + `</s:Body></s:Envelope>`
	writeRawUPnPResponse(w, envelope)
}

// writeRawUPnPResponse writes the response with the exact header
// name/casing/order/spacing a real minidlna instance sends (verified via
// packet capture against a Samsung TV that would only ever call
// BrowseMetadata, never BrowseDirectChildren, against our previous
// Header().Set()-based responses): notably "EXT:" with no trailing space,
// which Go's normal header writer cannot produce (it always inserts ": ").
// http.ResponseWriter offers no way to do this, so the connection is
// hijacked and the response written by hand.
func writeRawUPnPResponse(w http.ResponseWriter, body string) {
	hj, ok := w.(http.Hijacker)
	conn, buf, err := func() (net.Conn, *bufio.ReadWriter, error) {
		if !ok {
			return nil, nil, http.ErrNotSupported
		}
		return hj.Hijack()
	}()
	if err != nil {
		// Not every ResponseWriter supports hijacking (e.g. httptest's
		// ResponseRecorder in tests) - fall back to a normal response.
		w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
		_, _ = io.WriteString(w, body)
		return
	}
	defer func() { _ = conn.Close() }()

	if _, err := fmt.Fprintf(buf, "HTTP/1.1 200 OK\r\n"+
		"Content-Type: text/xml; charset=\"utf-8\"\r\n"+
		"Connection: close\r\n"+
		"Content-Length: %d\r\n"+
		"Server: Linux UPnP/1.0 DLNADOC/1.50 immich-dlna-proxy/1.0\r\n"+
		"Date: %s\r\n"+
		"EXT:\r\n\r\n%s",
		len(body), time.Now().UTC().Format(http.TimeFormat), body); err != nil {
		return
	}
	_ = buf.Flush()
}
