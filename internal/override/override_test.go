package override

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/damongolding/immich-kiosk/internal/config"
)

func TestFetchAlbumAssetsSkipsTrashed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"assets": {"items": [
			{"id": "1", "originalFileName": "kept.jpg", "isTrashed": false},
			{"id": "2", "originalFileName": "trashed.jpg", "isTrashed": true},
			{"id": "3", "originalFileName": "b.jpg", "exifInfo": {"description": "Pasta"}}
		]}}`))
	}))
	defer srv.Close()

	s := New(srv.URL, "key", config.KioskSettings{HTTPTimeout: 5})
	entries, err := s.fetchAlbumAssets("album")
	if err != nil {
		t.Fatal(err)
	}

	var labels []string
	for _, e := range entries {
		labels = append(labels, e.Label)
	}
	if want := []string{"kept.jpg", "Pasta"}; !slices.Equal(labels, want) {
		t.Errorf("labels = %v, want %v", labels, want)
	}
}
