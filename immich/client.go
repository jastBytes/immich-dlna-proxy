package immich

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

	// HTTP is used for the JSON API calls (listings, metadata). Its
	// overall Timeout bounds the whole exchange, body included, which is
	// fine for responses that are at most a few MB of JSON.
	HTTP *http.Client

	// Stream is used for the binary downloads (DownloadOriginal,
	// GetAssetThumbnail, GetPersonThumbnail). It deliberately has no
	// overall Timeout: http.Client.Timeout also covers reading the body,
	// so a multi-GB video that takes longer than that to transfer would be
	// cut off mid-download. Instead, the transport bounds how long Immich
	// may take to start answering (ResponseHeaderTimeout), and the
	// returned body aborts if a single read stalls for longer than
	// stallTimeout (see stallTimeoutBody) - a slow but progressing
	// download is never interrupted.
	Stream *http.Client
}

// apiTimeout bounds JSON API calls, and how long any request may wait for
// Immich's response headers.
const apiTimeout = 30 * time.Second

// stallTimeout is how long a single read from a download body may block
// before the download is aborted. A var rather than a const so tests can
// shrink it.
var stallTimeout = 30 * time.Second

func New(baseURL, apiKey string) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = apiTimeout
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: apiTimeout, Transport: transport},
		Stream:  &http.Client{Transport: transport},
	}
}

// ValidID reports whether id is safe to use as an Immich asset/album/
// person ID: non-empty and made only of ASCII letters, digits and '-'.
// Immich IDs are UUIDs, so this never rejects a real one, but it does
// reject anything that could change the meaning of the API path it's
// spliced into - '/', '.', '?', '#', '%' - which matters because these IDs
// arrive from unauthenticated DLNA clients (URL paths, Browse ObjectIDs)
// and are sent to Immich with this proxy's API key. Without it, e.g.
// "/thumbnail/0/..%2F..%2Fusers" would make the proxy fetch and relay
// GET /api/users.
func ValidID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
		default:
			return false
		}
	}
	return true
}

// idPath escapes an ID for use as a single path segment - defense in
// depth behind ValidID, which callers are expected to check first.
func idPath(id string) string {
	return url.PathEscape(id)
}

func (c *Client) newRequest(method, path string) (*http.Request, error) {
	return c.newRequestWithContext(context.Background(), method, path)
}

func (c *Client) newRequestWithContext(ctx context.Context, method, path string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, nil)
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
	req, err := c.newRequest(http.MethodGet, "/api/albums/"+idPath(id))
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
	req, err := c.newRequest(http.MethodGet, "/api/people/"+idPath(id))
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
func (c *Client) GetPersonThumbnail(ctx context.Context, personID string) (body io.ReadCloser, mimeType string, err error) {
	return c.stream(ctx, "GetPersonThumbnail("+personID+")", "/api/people/"+idPath(personID)+"/thumbnail")
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
	req, err := c.newRequest(http.MethodGet, "/api/assets/"+idPath(id))
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
func (c *Client) GetAssetThumbnail(ctx context.Context, assetID string) (body io.ReadCloser, mimeType string, err error) {
	return c.stream(ctx, "GetAssetThumbnail("+assetID+")", "/api/assets/"+idPath(assetID)+"/thumbnail?size=preview")
}

// DownloadOriginal fetches the complete original file. It never forwards a
// Range header, since callers always need the whole object (to decode it
// for EXIF orientation / resizing, or to populate the disk cache).
// The caller must close the returned ReadCloser.
func (c *Client) DownloadOriginal(ctx context.Context, assetID string) (body io.ReadCloser, mimeType string, err error) {
	return c.stream(ctx, "DownloadOriginal("+assetID+")", "/api/assets/"+idPath(assetID)+"/original")
}

// stream performs a binary GET against path via c.Stream, returning the
// response body (wrapped so a stalled read aborts after stallTimeout) and
// its Content-Type, defaulting to image/jpeg if Immich didn't send one.
// Cancelling ctx (e.g. the DLNA client hung up) aborts the download. The
// caller must close the returned body.
func (c *Client) stream(ctx context.Context, what, path string) (io.ReadCloser, string, error) {
	ctx, cancel := context.WithCancel(ctx)
	req, err := c.newRequestWithContext(ctx, http.MethodGet, path)
	if err != nil {
		cancel()
		return nil, "", err
	}
	resp, err := c.Stream.Do(req)
	if err != nil {
		cancel()
		return nil, "", err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		cancel()
		return nil, "", fmt.Errorf("immich %s: unexpected status %s", what, resp.Status)
	}

	mimeType := resp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	return newStallTimeoutBody(resp.Body, cancel, stallTimeout), mimeType, nil
}

// stallTimeoutBody aborts a download (by cancelling its request context)
// when a single Read blocks for longer than timeout. The timer only runs
// while a Read is in progress, so time the consumer spends not reading -
// e.g. a paused TV applying TCP backpressure while the body is copied
// straight to it - never counts as a stall.
type stallTimeoutBody struct {
	rc      io.ReadCloser
	timer   *time.Timer
	timeout time.Duration
	cancel  context.CancelFunc
}

func newStallTimeoutBody(rc io.ReadCloser, cancel context.CancelFunc, timeout time.Duration) *stallTimeoutBody {
	t := time.AfterFunc(timeout, cancel)
	t.Stop()
	return &stallTimeoutBody{rc: rc, timer: t, timeout: timeout, cancel: cancel}
}

func (b *stallTimeoutBody) Read(p []byte) (int, error) {
	b.timer.Reset(b.timeout)
	n, err := b.rc.Read(p)
	b.timer.Stop()
	return n, err
}

func (b *stallTimeoutBody) Close() error {
	b.timer.Stop()
	err := b.rc.Close()
	b.cancel()
	return err
}
