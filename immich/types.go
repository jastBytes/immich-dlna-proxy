package immich

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Album is the shape returned by GET /api/albums and GET /api/albums/{id}.
// Neither includes the album's assets (see GetAlbumAssets) - Immich
// returns more fields than this too; we only decode what we need.
type Album struct {
	ID         string `json:"id"`
	AlbumName  string `json:"albumName"`
	AssetCount int    `json:"assetCount"`
	// AlbumThumbnailAssetID is the ID of the asset Immich uses as the
	// album's cover photo (user-selected, or the most recent asset by
	// default). Empty for an empty album. Reused directly as a
	// /media/{id} URL for the album container's albumArtURI - no
	// separate thumbnail endpoint needed, unlike people.
	AlbumThumbnailAssetID string `json:"albumThumbnailAssetId"`
}

// Asset is a single photo/video entry.
type Asset struct {
	ID               string `json:"id"`
	OriginalFileName string `json:"originalFileName"`
	OriginalMimeType string `json:"originalMimeType"`
	// Type is "IMAGE" or "VIDEO" in current Immich API versions.
	Type string `json:"type"`
	// FileCreatedAt is Immich's authoritative capture timestamp (RFC3339,
	// usually derived from EXIF) - see CapturedAt.
	FileCreatedAt string `json:"fileCreatedAt"`
	// LocalDateTime is the capture time as the camera's wall clock showed
	// it, in the photo's own time zone - Immich serializes it with a "Z"
	// suffix, but its fields are local, not UTC. See LocalCaptureDate.
	LocalDateTime string `json:"localDateTime"`
	// ExifInfo carries Immich's parsed EXIF metadata; only FileSizeInByte
	// is used here, for the DIDL-Lite <res> element's size attribute (see
	// buildAssetItem in dlna/contentdirectory.go) - without it, DLNA
	// clients that show a size/date readout for the selected item (e.g.
	// Samsung's Smart TV browser) display a placeholder like "0 bytes"
	// instead of leaving it blank. Older Immich versions or endpoints
	// that omit exifInfo simply decode to a zero value, which
	// buildAssetItem treats as "size unknown" and omits the attribute
	// entirely, matching this repo's pass-through-on-unsupported-input
	// convention.
	ExifInfo struct {
		FileSizeInByte int64 `json:"fileSizeInByte"`
		// ExifImageWidth/Height are the pixel dimensions Immich read from
		// the file - used as a video item's DIDL-Lite resolution attribute
		// (see buildAssetItem). Zero when unknown.
		ExifImageWidth  int `json:"exifImageWidth"`
		ExifImageHeight int `json:"exifImageHeight"`
		// Country and City are Immich's reverse-geocoded place for a
		// geotagged asset, used by the optional "Places" folder. Empty
		// (or null in the JSON) when unknown.
		Country string `json:"country"`
		City    string `json:"city"`
	} `json:"exifInfo"`
	// Duration is a video's length - see AssetDuration for the formats
	// Immich has used.
	Duration AssetDuration `json:"duration"`
}

// AssetDuration is a video's length, decoded from whichever format the
// Immich server uses: Immich 1.x/2.x send a "H:MM:SS.ffffff" string
// (e.g. "0:01:05.250000"), Immich 3.x an integer number of milliseconds
// (e.g. 65250), and photos "0:00:00.00000" or null. Anything else decodes
// to zero ("unknown") instead of failing: one unexpected value here must
// never make the whole listing it's part of undecodable - that's how
// every listing containing a video broke on Immich 3 when this field was
// a plain string.
type AssetDuration time.Duration

func (d *AssetDuration) UnmarshalJSON(b []byte) error {
	*d = 0
	var ms float64
	if err := json.Unmarshal(b, &ms); err == nil {
		if ms > 0 {
			*d = AssetDuration(time.Duration(ms * float64(time.Millisecond)))
		}
		return nil
	}
	var str string
	if err := json.Unmarshal(b, &str); err == nil {
		*d = AssetDuration(parseClockDuration(str))
	}
	return nil
}

// parseClockDuration parses Immich's old "H:MM:SS[.fff...]" duration
// format, returning 0 for anything malformed.
func parseClockDuration(s string) time.Duration {
	h, rest, ok := strings.Cut(s, ":")
	if !ok {
		return 0
	}
	m, sec, ok := strings.Cut(rest, ":")
	if !ok || len(m) != 2 {
		return 0
	}
	whole, frac, _ := strings.Cut(sec, ".")
	if len(whole) != 2 || !allDigits(h) || !allDigits(m) || !allDigits(whole) || (frac != "" && !allDigits(frac)) {
		return 0
	}
	hours, _ := strconv.Atoi(h)
	mins, _ := strconv.Atoi(m)
	secs, _ := strconv.Atoi(whole)
	d := time.Duration(hours)*time.Hour + time.Duration(mins)*time.Minute + time.Duration(secs)*time.Second
	if frac != "" {
		frac = (frac + "000000000")[:9] // to nanoseconds
		ns, _ := strconv.Atoi(frac)
		d += time.Duration(ns)
	}
	return d
}

// DLNADuration returns the video's length in the H+:MM:SS.fff form the
// DIDL-Lite res@duration attribute uses, or "" for photos and unknown or
// zero lengths - matching this repo's pass-through convention, a missing
// duration just means the attribute is omitted.
func (a Asset) DLNADuration() string {
	d := time.Duration(a.Duration)
	if !a.IsVideo() || d <= 0 {
		return ""
	}
	ms := d.Milliseconds()
	return fmt.Sprintf("%d:%02d:%02d.%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// IsPhoto reports whether the asset is a photo.
func (a Asset) IsPhoto() bool {
	return a.Type == "IMAGE"
}

// CapturedAt parses FileCreatedAt for sorting by capture date (dc:date in
// DLNA SortCriteria - see sortPhotos in dlna/contentdirectory.go). An
// asset with a missing or unparseable timestamp returns the zero time -
// sorting as the oldest possible asset - rather than erroring, matching
// this repo's pass-through-on-unsupported-input convention.
func (a Asset) CapturedAt() time.Time {
	t, err := time.Parse(time.RFC3339, a.FileCreatedAt)
	if err != nil {
		return time.Time{}
	}
	return t
}

// LocalCaptureDate returns the calendar date the asset was captured on in
// its own time zone - the date Immich's own "On this day" memories go by:
// a photo taken at 00:30 on 1 October in Berlin was taken on 1 October,
// even though its UTC instant (FileCreatedAt) is 30 September 22:30. It
// uses LocalDateTime, falling back to FileCreatedAt's UTC date when that
// is missing; ok is false if neither parses.
func (a Asset) LocalCaptureDate() (year int, month time.Month, day int, ok bool) {
	for _, s := range []string{a.LocalDateTime, a.FileCreatedAt} {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			year, month, day = t.UTC().Date()
			return year, month, day, true
		}
	}
	return 0, 0, 0, false
}

// IsVideo reports whether the asset is a video.
func (a Asset) IsVideo() bool {
	return a.Type == "VIDEO"
}

// Person is one entry from GET /api/people (a named face cluster).
type Person struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	IsHidden bool   `json:"isHidden"`
	// ThumbnailPath is an Immich-internal file path, not a URL this proxy
	// can fetch directly - it's only used here as a presence check for
	// "does this person have a face-crop thumbnail at all". The actual
	// image comes from GET /api/people/{id}/thumbnail (see
	// Client.GetPersonThumbnail).
	ThumbnailPath string `json:"thumbnailPath"`
}

// HasThumbnail reports whether Immich has a face-crop thumbnail for this
// person, fetchable via Client.GetPersonThumbnail.
func (p Person) HasThumbnail() bool {
	return p.ThumbnailPath != ""
}

// IsNamed reports whether this person has been given a name. Immich
// creates a Person for every detected face cluster, including ones the
// user hasn't confirmed/named yet - we only want to show named people as
// browsable folders, not an "Unknown" folder per unconfirmed face.
func (p Person) IsNamed() bool {
	return p.Name != ""
}

// PeopleResponse is the shape returned by GET /api/people.
type PeopleResponse struct {
	People []Person `json:"people"`
	Total  int      `json:"total"`
	Hidden int      `json:"hidden"`
}

// User is the shape returned by GET /api/users/me - the account that owns
// the API key used to authenticate. Used to label the top-level per-user
// folder when more than one IMMICH_API_KEYS entry is configured.
type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}
