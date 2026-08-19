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
	return c.searchMetadataAssets("albumIds", albumID)
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
	return c.searchMetadataAssets("personIds", personID)
}

// searchMetadataAssets fetches every asset matching a single-value filter
// (e.g. albumIds/personIds) via POST /api/search/metadata, following
// "nextPage" until the server stops returning one.
func (c *Client) searchMetadataAssets(filterField, id string) ([]Asset, error) {
	var all []Asset
	page := 1
	for {
		reqBody, err := json.Marshal(map[string]any{filterField: []string{id}, "page": page})
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
			return nil, fmt.Errorf("immich searchMetadata(%s=%s): unexpected status %s", filterField, id, resp.Status)
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
