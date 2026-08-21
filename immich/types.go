package immich

import "time"

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
