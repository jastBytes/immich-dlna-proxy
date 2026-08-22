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
		writeSoapResponse(w, cdNS, "GetSortCapabilitiesResponse", map[string]string{"SortCaps": "dc:title,dc:date"})
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
	var ok bool

	if len(s.users) > 1 {
		didl, returned, total, ok = s.browseMultiUser(w, objectID, args, baseURL)
	} else {
		// Single configured account: browse exactly as if it were the only
		// thing that ever existed - no "user:<idx>" folder level, and
		// /media/ URLs keep their original /media/{assetID} shape (userIdx
		// -1, see mediaURL).
		didl, returned, total, ok = s.browseUserScope(w, s.users[0].Client, -1, "", objectID, s.cfg.FriendlyName, "-1", "0", args, baseURL)
	}
	if !ok {
		return
	}

	if !isAssetObjectID(objectID) {
		log.Printf("Browse %s %s -> %d/%d items", objectID, args.BrowseFlag, returned, total)
	}

	writeSoapResponse(w, cdNS, responseName, map[string]string{
		"Result":         didl,
		"NumberReturned": strconv.Itoa(returned),
		"TotalMatches":   strconv.Itoa(total),
		"UpdateID":       "1",
	})
}

// isAssetObjectID reports whether objectID is a single photo leaf, in
// either the single-user ("asset:<id>") or multi-user
// ("user:<idx>:asset:<id>") ObjectID shape - used only to skip the routine
// per-Browse log line for the especially frequent per-photo lookups DLNA
// clients make.
func isAssetObjectID(objectID string) bool {
	return strings.Contains(objectID, "asset:")
}

// browseMultiUser handles the top of the ObjectID space when more than one
// IMMICH_API_KEYS entry is configured: "0" lists one container per
// configured account (named via UserClient.Name, fetched from Immich at
// startup - see main.go), and "user:<idx>[:<local>]" descends into that
// account's own albums/people tree via browseUserScope.
func (s *Server) browseMultiUser(w http.ResponseWriter, objectID string, args *browseArgs, baseURL string) (didl string, returned, total int, ok bool) {
	switch {
	case objectID == "0" && args.BrowseFlag == "BrowseMetadata":
		return wrapDIDL(buildContainer("0", "-1", s.cfg.FriendlyName, len(s.users))), 1, 1, true

	case objectID == "0": // BrowseDirectChildren on root: one folder per configured account
		fragments := make([]string, len(s.users))
		for i, u := range s.users {
			fragments[i] = buildContainer(userObjectID(i), "0", u.Name, 2)
		}
		total = len(fragments)
		fragments = page(fragments, args.StartingIndex, args.RequestedCount)
		return wrapDIDL(strings.Join(fragments, "")), len(fragments), total, true

	case strings.HasPrefix(objectID, "user:"):
		idx, local, valid := parseUserObjectID(objectID, len(s.users))
		if !valid {
			http.Error(w, "unknown object", http.StatusNotFound)
			return "", 0, 0, false
		}
		user := s.users[idx]
		return s.browseUserScope(w, user.Client, idx, "user:"+strconv.Itoa(idx)+":", local, user.Name, "0", userObjectID(idx), args, baseURL)

	default:
		http.Error(w, "unknown object", http.StatusNotFound)
		return "", 0, 0, false
	}
}

// userObjectID is the ObjectID for one configured account's top-level
// folder.
func userObjectID(idx int) string {
	return "user:" + strconv.Itoa(idx)
}

// parseUserObjectID splits a multi-user ObjectID like "user:1" or
// "user:1:album:abc" into the configured-account index and the remaining
// local ObjectID relative to that account's own tree ("0" for the bare
// "user:<idx>" case, meaning that account's own root - see
// browseUserScope's local=="0" case). ok is false for a malformed or
// out-of-range index.
func parseUserObjectID(objectID string, numUsers int) (idx int, local string, ok bool) {
	rest := strings.TrimPrefix(objectID, "user:")
	idxStr, local, found := strings.Cut(rest, ":")
	if !found {
		local = "0"
	}
	n, err := strconv.Atoi(idxStr)
	if err != nil || n < 0 || n >= numUsers {
		return 0, "", false
	}
	return n, local, true
}

// browseUserScope implements Browse/Search within one configured account's
// namespace - this is the entire single-user Browse behavior, generalized
// so the multi-user case can reuse it once per account instead of
// duplicating it. local is the ObjectID with any "user:<idx>:" prefix
// already stripped, so it's "0" for this scope's own root and
// "albums"/"album:<id>"/"people"/"person:<id>"/"asset:<id>" exactly as in
// the single-user case. childPrefix is prepended to every child ObjectID
// this scope produces ("" for the single-user case, "user:<idx>:" for a
// multi-user one) so a later Browse call routes back to the same account.
// userIdx is threaded through into <res> URLs (see mediaURL) so /media/
// knows which account's API key to download with (-1 for the single-user
// case, which keeps the original /media/{assetID} URL shape).
// rootSelfID/rootParentID/rootTitle describe how local=="0" renders itself
// under BrowseMetadata.
func (s *Server) browseUserScope(w http.ResponseWriter, client *immich.Client, userIdx int, childPrefix, local, rootTitle, rootParentID, rootSelfID string, args *browseArgs, baseURL string) (didl string, returned, total int, ok bool) {
	switch {
	case local == "0" && args.BrowseFlag == "BrowseMetadata":
		return wrapDIDL(buildContainer(rootSelfID, rootParentID, rootTitle, 2)), 1, 1, true

	case local == "0": // BrowseDirectChildren on this account's root: fixed "Albums" / "People" folders
		albums, err := client.ListAlbums()
		if err != nil {
			log.Printf("ListAlbums failed: %v", err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return "", 0, 0, false
		}
		people, err := client.ListPeople()
		if err != nil {
			log.Printf("ListPeople failed: %v", err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return "", 0, 0, false
		}
		namedPeople := countNamedPeople(people)

		fragments := []string{
			buildContainer(childPrefix+"albums", rootSelfID, "Albums", len(albums)),
			buildContainer(childPrefix+"people", rootSelfID, "People", namedPeople),
		}
		total = len(fragments)
		fragments = page(fragments, args.StartingIndex, args.RequestedCount)
		return wrapDIDL(strings.Join(fragments, "")), len(fragments), total, true

	case local == "albums" && args.BrowseFlag == "BrowseMetadata":
		albums, err := client.ListAlbums()
		if err != nil {
			log.Printf("ListAlbums failed: %v", err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return "", 0, 0, false
		}
		return wrapDIDL(buildContainer(childPrefix+"albums", rootSelfID, "Albums", len(albums))), 1, 1, true

	case local == "albums": // BrowseDirectChildren: list albums
		albums, err := client.ListAlbums()
		if err != nil {
			log.Printf("ListAlbums failed: %v", err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return "", 0, 0, false
		}
		sortByTitle(albums, func(a immich.Album) string { return a.AlbumName }, parseSortCriteria(args.SortCriteria))
		total = len(albums)
		paged := page(albums, args.StartingIndex, args.RequestedCount)
		var b strings.Builder
		for _, a := range paged {
			b.WriteString(buildContainer(childPrefix+"album:"+a.ID, childPrefix+"albums", a.AlbumName, a.AssetCount))
		}
		return wrapDIDL(b.String()), len(paged), total, true

	case local == "people" && args.BrowseFlag == "BrowseMetadata":
		people, err := client.ListPeople()
		if err != nil {
			log.Printf("ListPeople failed: %v", err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return "", 0, 0, false
		}
		return wrapDIDL(buildContainer(childPrefix+"people", rootSelfID, "People", countNamedPeople(people))), 1, 1, true

	case local == "people": // BrowseDirectChildren: list named people
		people, err := client.ListPeople()
		if err != nil {
			log.Printf("ListPeople failed: %v", err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return "", 0, 0, false
		}
		named := make([]immich.Person, 0, len(people))
		for _, p := range people {
			if p.IsNamed() {
				named = append(named, p)
			}
		}
		sortByTitle(named, func(p immich.Person) string { return p.Name }, parseSortCriteria(args.SortCriteria))
		total = len(named)
		paged := page(named, args.StartingIndex, args.RequestedCount)
		var b strings.Builder
		for _, p := range paged {
			// childCount omitted (-1): knowing it accurately would need one
			// GetPersonStatistics call per person, which doesn't scale for
			// libraries with many tagged people.
			b.WriteString(buildContainer(childPrefix+"person:"+p.ID, childPrefix+"people", p.Name, -1))
		}
		return wrapDIDL(b.String()), len(paged), total, true

	case strings.HasPrefix(local, "album:"):
		albumID := strings.TrimPrefix(local, "album:")
		album, err := client.GetAlbum(albumID)
		if err != nil {
			log.Printf("GetAlbum(%s) failed: %v", albumID, err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return "", 0, 0, false
		}
		assets, err := client.GetAlbumAssets(albumID)
		if err != nil {
			log.Printf("GetAlbumAssets(%s) failed: %v", albumID, err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return "", 0, 0, false
		}
		photos := filterPhotos(assets)

		if args.BrowseFlag == "BrowseMetadata" {
			return wrapDIDL(buildContainer(childPrefix+local, childPrefix+"albums", album.AlbumName, len(photos))), 1, 1, true
		}
		sortPhotos(photos, parseSortCriteria(args.SortCriteria))
		total = len(photos)
		paged := page(photos, args.StartingIndex, args.RequestedCount)
		var b strings.Builder
		for _, a := range paged {
			resURL := mediaURL(baseURL, userIdx, a.ID)
			b.WriteString(buildPhotoItem(childPrefix+"asset:"+a.ID, childPrefix+local, a.OriginalFileName, a.OriginalMimeType, resURL))
		}
		return wrapDIDL(b.String()), len(paged), total, true

	case strings.HasPrefix(local, "person:"):
		personID := strings.TrimPrefix(local, "person:")

		if args.BrowseFlag == "BrowseMetadata" {
			person, err := client.GetPerson(personID)
			if err != nil {
				log.Printf("GetPerson(%s) failed: %v", personID, err)
				http.Error(w, "upstream error", http.StatusBadGateway)
				return "", 0, 0, false
			}
			return wrapDIDL(buildContainer(childPrefix+local, childPrefix+"people", person.Name, -1)), 1, 1, true
		}
		assets, err := client.GetPersonAssets(personID)
		if err != nil {
			log.Printf("GetPersonAssets(%s) failed: %v", personID, err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return "", 0, 0, false
		}
		photos := filterPhotos(assets)
		sortPhotos(photos, parseSortCriteria(args.SortCriteria))
		total = len(photos)
		paged := page(photos, args.StartingIndex, args.RequestedCount)
		var b strings.Builder
		for _, a := range paged {
			resURL := mediaURL(baseURL, userIdx, a.ID)
			b.WriteString(buildPhotoItem(childPrefix+"asset:"+a.ID, childPrefix+local, a.OriginalFileName, a.OriginalMimeType, resURL))
		}
		return wrapDIDL(b.String()), len(paged), total, true

	case strings.HasPrefix(local, "asset:"):
		assetID := strings.TrimPrefix(local, "asset:")
		asset, err := client.GetAsset(assetID)
		if err != nil {
			log.Printf("GetAsset(%s) failed: %v", assetID, err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return "", 0, 0, false
		}
		resURL := mediaURL(baseURL, userIdx, asset.ID)
		return wrapDIDL(buildPhotoItem(childPrefix+local, rootSelfID, asset.OriginalFileName, asset.OriginalMimeType, resURL)), 1, 1, true

	default:
		http.Error(w, "unknown object", http.StatusNotFound)
		return "", 0, 0, false
	}
}

// sortRequest is the (property, direction) this server extracted from a
// DLNA SortCriteria string - see parseSortCriteria. An empty property
// means "leave Immich's own order alone".
type sortRequest struct {
	property   string // "dc:title" or "dc:date"
	descending bool
}

// parseSortCriteria extracts the first recognized property/direction pair
// out of a DLNA SortCriteria string (e.g. "+dc:title" or
// "-dc:date,+upnp:originalTrackNumber" - comma-separated, each entry
// prefixed with + or -). dc:title (every container/item's name) and
// dc:date (a photo's capture time, from Immich's fileCreatedAt) are the
// only properties this server advertises via GetSortCapabilities; any
// other property in the criteria is ignored rather than rejected,
// matching this repo's pass-through-on-unsupported-input convention.
// dc:date only makes sense for photo items, not album/people containers -
// callers sorting a container list simply don't act on it.
func parseSortCriteria(criteria string) sortRequest {
	for _, part := range strings.Split(criteria, ",") {
		switch strings.TrimSpace(part) {
		case "+dc:title":
			return sortRequest{property: "dc:title"}
		case "-dc:title":
			return sortRequest{property: "dc:title", descending: true}
		case "+dc:date":
			return sortRequest{property: "dc:date"}
		case "-dc:date":
			return sortRequest{property: "dc:date", descending: true}
		}
	}
	return sortRequest{}
}

// sortByTitle sorts items in place by a case-insensitive comparison of the
// string title returns for each, applying it only when req asked for
// dc:title (any other/empty property leaves the slice - and thus
// Immich's own ordering - untouched).
func sortByTitle[T any](items []T, title func(T) string, req sortRequest) {
	if req.property != "dc:title" {
		return
	}
	slices.SortFunc(items, func(a, b T) int {
		c := strings.Compare(strings.ToLower(title(a)), strings.ToLower(title(b)))
		if req.descending {
			return -c
		}
		return c
	})
}

// sortPhotos sorts photo assets in place per req, supporting both
// properties this server advertises: dc:title (filename) and dc:date
// (capture time, via Asset.CapturedAt - an asset with a missing or
// unparseable timestamp sorts as the zero time, i.e. oldest).
func sortPhotos(photos []immich.Asset, req sortRequest) {
	switch req.property {
	case "dc:title":
		sortByTitle(photos, func(a immich.Asset) string { return a.OriginalFileName }, req)
	case "dc:date":
		slices.SortFunc(photos, func(a, b immich.Asset) int {
			c := a.CapturedAt().Compare(b.CapturedAt())
			if req.descending {
				return -c
			}
			return c
		})
	}
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
