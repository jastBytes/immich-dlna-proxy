package immich

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Client talks to a single Immich server using an API key.
//
// NOTE: Immich's REST API has changed shape across major versions
// (see https://immich.app/docs/api for your installed version's spec,
// usually browsable at {server}/api/doc or {server}/api/openapi.yaml).
// The endpoints and JSON fields below match Immich's commonly-used v1
// API as of 2026. If your server responds with 404s here, check the
// live OpenAPI doc on your instance and adjust the paths/fields.
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func New(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) newRequest(method, path string) (*http.Request, error) {
	req, err := http.NewRequest(method, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// ListAlbums returns all albums visible to the API key's owner.
func (c *Client) ListAlbums() ([]Album, error) {
	req, err := c.newRequest(http.MethodGet, "/api/albums")
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("immich ListAlbums: unexpected status %s", resp.Status)
	}
	var albums []Album
	if err := json.NewDecoder(resp.Body).Decode(&albums); err != nil {
		return nil, err
	}
	return albums, nil
}

// GetAlbum returns the album's own metadata (name, asset count, etc.) but
// not its assets - see GetAlbumAssets for those.
func (c *Client) GetAlbum(id string) (*Album, error) {
	req, err := c.newRequest(http.MethodGet, "/api/albums/"+id)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("immich GetAlbum(%s): unexpected status %s", id, resp.Status)
	}
	var album Album
	if err := json.NewDecoder(resp.Body).Decode(&album); err != nil {
		return nil, err
	}
	return &album, nil
}

// GetAlbumAssets returns every asset (photo or video) in the given album.
func (c *Client) GetAlbumAssets(albumID string) ([]Asset, error) {
	return c.searchMetadataAssets(map[string]any{"albumIds": []string{albumID}})
}

// ListPeople returns named, non-hidden people (GET /api/people defaults to
// excluding hidden people; we filter to named ones ourselves since Immich
// also returns unconfirmed/unnamed face clusters here).
func (c *Client) ListPeople() ([]Person, error) {
	req, err := c.newRequest(http.MethodGet, "/api/people")
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("immich ListPeople: unexpected status %s", resp.Status)
	}
	var out PeopleResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.People, nil
}

// GetPerson returns metadata for a single person.
func (c *Client) GetPerson(id string) (*Person, error) {
	req, err := c.newRequest(http.MethodGet, "/api/people/"+id)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("immich GetPerson(%s): unexpected status %s", id, resp.Status)
	}
	var person Person
	if err := json.NewDecoder(resp.Body).Decode(&person); err != nil {
		return nil, err
	}
	return &person, nil
}

// GetPersonAssets returns every asset (photo or video) this person appears in.
func (c *Client) GetPersonAssets(personID string) ([]Asset, error) {
	return c.searchMetadataAssets(map[string]any{"personIds": []string{personID}})
}

// GetPersonThumbnail fetches the person's face-crop thumbnail image, used
// as the person container's albumArtURI. Unlike DownloadOriginal, Immich
// serves this directly as image bytes rather than via an asset ID - there
// is no corresponding Asset to look up.
func (c *Client) GetPersonThumbnail(personID string) (body io.ReadCloser, mimeType string, err error) {
	req, err := c.newRequest(http.MethodGet, "/api/people/"+personID+"/thumbnail")
	if err != nil {
		return nil, "", err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, "", fmt.Errorf("immich GetPersonThumbnail(%s): unexpected status %s", personID, resp.Status)
	}

	mimeType = resp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	return resp.Body, mimeType, nil
}

// ListTimelineAssets returns every asset (photo or video) visible to the API
// key's owner, most recently taken first - the same chronological order
// Immich's own web/mobile timeline view uses.
func (c *Client) ListTimelineAssets() ([]Asset, error) {
	return c.searchMetadataAssets(map[string]any{"order": "desc"})
}

// searchMetadataAssets fetches every asset matching the given filter body
// (e.g. {"albumIds": [...]}, {"personIds": [...]}, or {"order": "desc"} for
// no filter at all) via POST /api/search/metadata, following "nextPage"
// until the server stops returning one.
func (c *Client) searchMetadataAssets(filter map[string]any) ([]Asset, error) {
	var all []Asset
	page := 1
	for {
		reqBody, err := json.Marshal(mergePage(filter, page))
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequest(http.MethodPost, c.BaseURL+"/api/search/metadata", bytes.NewReader(reqBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("x-api-key", c.APIKey)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.HTTP.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("immich searchMetadata(%v): unexpected status %s", filter, resp.Status)
		}
		var out struct {
			Assets struct {
				Items    []Asset `json:"items"`
				NextPage *string `json:"nextPage"`
			} `json:"assets"`
		}
		err = json.NewDecoder(resp.Body).Decode(&out)
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}
		all = append(all, out.Assets.Items...)

		if out.Assets.NextPage == nil {
			return all, nil
		}
		next, err := strconv.Atoi(*out.Assets.NextPage)
		if err != nil {
			return all, nil
		}
		page = next
	}
}

// mergePage returns a copy of filter with "page" set, leaving the caller's
// map untouched (it's reused across pagination requests).
func mergePage(filter map[string]any, page int) map[string]any {
	body := make(map[string]any, len(filter)+1)
	for k, v := range filter {
		body[k] = v
	}
	body["page"] = page
	return body
}

// GetMyUser returns the account that owns this client's API key (via
// GET /api/users/me) - used to label the top-level per-user folder when
// more than one IMMICH_API_KEYS entry is configured.
func (c *Client) GetMyUser() (*User, error) {
	req, err := c.newRequest(http.MethodGet, "/api/users/me")
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("immich GetMyUser: unexpected status %s", resp.Status)
	}
	var user User
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, err
	}
	return &user, nil
}

// GetAsset returns metadata for a single asset (used to know its mime type
// before streaming it, if the caller doesn't already have that from the
// album listing).
func (c *Client) GetAsset(id string) (*Asset, error) {
	req, err := c.newRequest(http.MethodGet, "/api/assets/"+id)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("immich GetAsset(%s): unexpected status %s", id, resp.Status)
	}
	var asset Asset
	if err := json.NewDecoder(resp.Body).Decode(&asset); err != nil {
		return nil, err
	}
	return &asset, nil
}

// GetAssetThumbnail fetches Immich's server-generated preview-sized
// thumbnail for an asset. A video's own bytes can't double as an image
// preview the way a photo's can, so video items use this instead of
// DownloadOriginal for their DIDL-Lite albumArtURI.
func (c *Client) GetAssetThumbnail(assetID string) (body io.ReadCloser, mimeType string, err error) {
	req, err := c.newRequest(http.MethodGet, "/api/assets/"+assetID+"/thumbnail?size=preview")
	if err != nil {
		return nil, "", err
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, "", fmt.Errorf("immich GetAssetThumbnail(%s): unexpected status %s", assetID, resp.Status)
	}

	mimeType = resp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	return resp.Body, mimeType, nil
}

// DownloadOriginal fetches the complete original file. It never forwards a
// Range header, since callers always need the whole object (to decode it
// for EXIF orientation / resizing, or to populate the disk cache).
// The caller must close the returned ReadCloser.
func (c *Client) DownloadOriginal(assetID string) (body io.ReadCloser, mimeType string, err error) {
	req, err := c.newRequest(http.MethodGet, "/api/assets/"+assetID+"/original")
	if err != nil {
		return nil, "", err
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, "", fmt.Errorf("immich DownloadOriginal(%s): unexpected status %s", assetID, resp.Status)
	}

	mimeType = resp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	return resp.Body, mimeType, nil
}
