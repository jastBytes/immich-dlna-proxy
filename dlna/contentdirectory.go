package dlna

import (
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/jastBytes/immich-dlna-proxy/immich"
)

type cdEnvelope struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    cdBody   `xml:"Body"`
}

type cdBody struct {
	Browse                *browseArgs `xml:"Browse"`
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
		s.handleBrowse(w, r, env.Body.Browse)
	case env.Body.GetSearchCapabilities != nil:
		writeSoapResponse(w, cdNS, "GetSearchCapabilitiesResponse", map[string]string{"SearchCaps": ""})
	case env.Body.GetSortCapabilities != nil:
		writeSoapResponse(w, cdNS, "GetSortCapabilitiesResponse", map[string]string{"SortCaps": ""})
	case env.Body.GetSystemUpdateID != nil:
		writeSoapResponse(w, cdNS, "GetSystemUpdateIDResponse", map[string]string{"Id": "1"})
	default:
		http.Error(w, "unsupported action", http.StatusNotImplemented)
	}
}

func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request, args *browseArgs) {
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

		var b strings.Builder
		b.WriteString(buildContainer("albums", "0", "Albums", len(albums)))
		b.WriteString(buildContainer("people", "0", "People", namedPeople))
		didl = wrapDIDL(b.String())
		returned, total = 2, 2

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
		var b strings.Builder
		for _, a := range albums {
			b.WriteString(buildContainer("album:"+a.ID, "albums", a.AlbumName, a.AssetCount))
		}
		didl = wrapDIDL(b.String())
		returned, total = len(albums), len(albums)

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
		var b strings.Builder
		var n int
		for _, p := range people {
			if !p.IsNamed() {
				continue
			}
			// childCount omitted (-1): knowing it accurately would need one
			// GetPersonStatistics call per person, which doesn't scale for
			// libraries with many tagged people.
			b.WriteString(buildContainer("person:"+p.ID, "people", p.Name, -1))
			n++
		}
		didl = wrapDIDL(b.String())
		returned, total = n, n

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
			var b strings.Builder
			for _, a := range photos {
				resURL := baseURL + "/media/" + a.ID
				b.WriteString(buildPhotoItem("asset:"+a.ID, objectID, a.OriginalFileName, a.OriginalMimeType, resURL))
			}
			didl = wrapDIDL(b.String())
			returned, total = len(photos), len(photos)
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
			var b strings.Builder
			for _, a := range photos {
				resURL := baseURL + "/media/" + a.ID
				b.WriteString(buildPhotoItem("asset:"+a.ID, objectID, a.OriginalFileName, a.OriginalMimeType, resURL))
			}
			n := len(photos)
			didl = wrapDIDL(b.String())
			returned, total = n, n
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

	writeSoapResponse(w, cdNS, "BrowseResponse", map[string]string{
		"Result":         didl,
		"NumberReturned": strconv.Itoa(returned),
		"TotalMatches":   strconv.Itoa(total),
		"UpdateID":       "1",
	})
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
			fmt.Fprintf(&body, "<%s>%s</%s>", k, html.EscapeString(v), k)
		}
	}
	fmt.Fprintf(&body, `</u:%s>`, actionResponseName)

	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	_, _ = fmt.Fprintf(w, `<?xml version="1.0"?>`+
		`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" `+
		`s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">`+
		`<s:Body>%s</s:Body></s:Envelope>`, body.String())
}
