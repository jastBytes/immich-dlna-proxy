package immich

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("immich ListAlbums: unexpected status %s", resp.Status)
	}
	var albums []Album
	if err := json.NewDecoder(resp.Body).Decode(&albums); err != nil {
		return nil, err
	}
	return albums, nil
}

// GetAlbum returns the album with its assets included.
func (c *Client) GetAlbum(id string) (*AlbumDetail, error) {
	req, err := c.newRequest(http.MethodGet, "/api/albums/"+id)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("immich GetAlbum(%s): unexpected status %s", id, resp.Status)
	}
	var detail AlbumDetail
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		return nil, err
	}
	return &detail, nil
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
	defer resp.Body.Close()
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
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("immich GetPerson(%s): unexpected status %s", id, resp.Status)
	}
	var person Person
	if err := json.NewDecoder(resp.Body).Decode(&person); err != nil {
		return nil, err
	}
	return &person, nil
}

// GetPersonAssets returns every asset (photo or video) this person appears
// in. Unlike album/search endpoints this one isn't paginated - fine for
// our purposes, but very large libraries with a person in thousands of
// photos could see a slow response here.
func (c *Client) GetPersonAssets(id string) ([]Asset, error) {
	req, err := c.newRequest(http.MethodGet, "/api/people/"+id+"/assets")
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("immich GetPersonAssets(%s): unexpected status %s", id, resp.Status)
	}
	var assets []Asset
	if err := json.NewDecoder(resp.Body).Decode(&assets); err != nil {
		return nil, err
	}
	return assets, nil
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
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("immich GetAsset(%s): unexpected status %s", id, resp.Status)
	}
	var asset Asset
	if err := json.NewDecoder(resp.Body).Decode(&asset); err != nil {
		return nil, err
	}
	return &asset, nil
}

// DownloadOriginal fetches the complete original file for caching purposes.
// Unlike StreamOriginal, it never forwards a Range header - callers that
// want the whole object (e.g. to populate the disk cache) should use this.
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
		resp.Body.Close()
		return nil, "", fmt.Errorf("immich DownloadOriginal(%s): unexpected status %s", assetID, resp.Status)
	}

	mimeType = resp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	return resp.Body, mimeType, nil
}

// StreamOriginal proxies the original file bytes for an asset straight
// through to w, forwarding the Range header so TVs can do partial reads
// (some picture viewers issue range requests even for photos).
func (c *Client) StreamOriginal(w http.ResponseWriter, r *http.Request, assetID string) error {
	req, err := c.newRequest(http.MethodGet, "/api/assets/"+assetID+"/original")
	if err != nil {
		return err
	}
	if rng := r.Header.Get("Range"); rng != "" {
		req.Header.Set("Range", rng)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		http.Error(w, "immich upstream error", http.StatusBadGateway)
		return fmt.Errorf("immich StreamOriginal(%s): unexpected status %s", assetID, resp.Status)
	}

	for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified", "ETag"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, err = io.Copy(w, resp.Body)
	return err
}
