package immich

// Album is the shape returned by GET /api/albums and GET /api/albums/{id}.
// Neither includes the album's assets (see GetAlbumAssets) - Immich
// returns more fields than this too; we only decode what we need.
type Album struct {
	ID         string `json:"id"`
	AlbumName  string `json:"albumName"`
	AssetCount int    `json:"assetCount"`
}

// Asset is a single photo/video entry.
type Asset struct {
	ID               string `json:"id"`
	OriginalFileName string `json:"originalFileName"`
	OriginalMimeType string `json:"originalMimeType"`
	// Type is "IMAGE" or "VIDEO" in current Immich API versions.
	Type string `json:"type"`
}

// IsPhoto reports whether the asset should be exposed to DLNA clients.
// We only support photos for now.
func (a Asset) IsPhoto() bool {
	return a.Type == "IMAGE"
}

// Person is one entry from GET /api/people (a named face cluster).
type Person struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	IsHidden      bool   `json:"isHidden"`
	ThumbnailPath string `json:"thumbnailPath"`
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
