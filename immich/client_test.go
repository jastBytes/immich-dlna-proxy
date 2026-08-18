package immich

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSearchMetadataAssetsFollowsPagination verifies GetAlbumAssets collects
// items across multiple pages by following the "nextPage" cursor.
func TestSearchMetadataAssetsFollowsPagination(t *testing.T) {
	var pagesSeen []int

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody struct {
			AlbumIds []string `json:"albumIds"`
			Page     int      `json:"page"`
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &reqBody); err != nil {
			t.Fatalf("bad request body: %v", err)
		}
		if reqBody.Page == 0 {
			reqBody.Page = 1
		}
		pagesSeen = append(pagesSeen, reqBody.Page)

		w.Header().Set("Content-Type", "application/json")
		switch reqBody.Page {
		case 1:
			w.Write([]byte(`{"assets":{"total":2,"count":1,"nextPage":"2",
				"items":[{"id":"a1","originalFileName":"a1.jpg","type":"IMAGE"}]}}`))
		case 2:
			w.Write([]byte(`{"assets":{"total":2,"count":1,"nextPage":null,
				"items":[{"id":"a2","originalFileName":"a2.jpg","type":"IMAGE"}]}}`))
		default:
			t.Fatalf("unexpected page %d", reqBody.Page)
		}
	}))
	defer ts.Close()

	client := New(ts.URL, "test-key")
	assets, err := client.GetAlbumAssets("album1")
	if err != nil {
		t.Fatal(err)
	}

	if len(assets) != 2 || assets[0].ID != "a1" || assets[1].ID != "a2" {
		t.Fatalf("expected [a1 a2], got %+v", assets)
	}
	if len(pagesSeen) != 2 || pagesSeen[0] != 1 || pagesSeen[1] != 2 {
		t.Fatalf("expected to fetch pages [1 2], got %v", pagesSeen)
	}
}
