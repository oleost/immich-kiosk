package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	_ "image/jpeg"
	"slices"
	"testing"
	"time"

	"github.com/damongolding/immich-kiosk/internal/config"
	"github.com/damongolding/immich-kiosk/internal/focus"
	"github.com/damongolding/immich-kiosk/internal/kiosk"
	"github.com/damongolding/immich-kiosk/internal/switches"
	"github.com/damongolding/immich-kiosk/internal/utils"
)

// setupTestControls installs control stores whose names are the entries.
func setupTestControls(t *testing.T, albums, people map[string][]string) {
	t.Helper()
	oldAlbums, oldPeople, oldSwitches := albumFocusStore, personFocusStore, switchStore
	t.Cleanup(func() {
		albumFocusStore, personFocusStore, switchStore = oldAlbums, oldPeople, oldSwitches
		// Selecting queues "next" for clients that are not connected.
		sseHub.mu.Lock()
		clear(sseHub.pending)
		sseHub.mu.Unlock()
	})

	sameName := func(entries []string) []string { return entries }
	albumFocusStore = focus.New(focus.AlbumKeywords, sameName)
	personFocusStore = focus.New(focus.PersonKeywords, sameName)
	switchStore = switches.New()

	learn := func(store *focus.Store, lists map[string][]string) {
		changed := make(chan string, len(lists))
		store.SetOnChange(func(target string) { changed <- target })
		for target, entries := range lists {
			store.Learn(target, entries)
		}
		for range lists {
			select {
			case <-changed:
			case <-time.After(time.Second):
				t.Fatal("options not resolved")
			}
		}
	}
	learn(albumFocusStore, albums)
	learn(personFocusStore, people)
}

func testRequestConfig() config.Config {
	c := config.Config{
		Albums: []string{"cabin", "summer"},
		People: []string{"anna", "ola"},
		Tags:   []string{"tag"},
		Rating: 3,
	}
	c.Memories = true
	return c
}

func TestApplyFocus(t *testing.T) {
	setupTestControls(t,
		map[string][]string{"": {"cabin", "summer"}, "kitchen": {"cabin", "summer"}},
		map[string][]string{"": {"anna", "ola"}, "kitchen": {"anna", "ola"}},
	)

	c := testRequestConfig()
	if applyFocus(&c, "kitchen") {
		t.Fatal("focus applied before any selection")
	}

	// The all-clients album focus applies to a named and an unnamed client.
	albumFocusStore.Select("", "cabin")
	for _, client := range []string{"kitchen", ""} {
		c = testRequestConfig()
		if !applyFocus(&c, client) || !slices.Equal(c.Albums, []string{"cabin"}) ||
			len(c.People) != 0 || len(c.Tags) != 0 || c.Memories || c.Rating != -1 {
			t.Errorf("client %q: got albums=%v people=%v tags=%v memories=%v rating=%v",
				client, c.Albums, c.People, c.Tags, c.Memories, c.Rating)
		}
	}

	// A person focus on the client wins over the all-clients album focus.
	personFocusStore.Select("kitchen", "anna")
	c = testRequestConfig()
	applyFocus(&c, "kitchen")
	if !slices.Equal(c.People, []string{"anna"}) || len(c.Albums) != 0 {
		t.Errorf("kitchen: got albums=%v people=%v, want only anna", c.Albums, c.People)
	}
}

func TestApplyControlsFavorites(t *testing.T) {
	setupTestControls(t, map[string][]string{"": {"cabin", "summer"}}, map[string][]string{"": {"anna"}})

	switchStore.Set("kitchen", fieldFavorites, true)

	c := testRequestConfig()
	if !applyControls(context.Background(), &c, "kitchen", "", "") || !c.FilterFavorites {
		t.Error("favorites filter not applied to kitchen")
	}
	// The sources are left alone.
	if !slices.Equal(c.Albums, []string{"cabin", "summer"}) {
		t.Errorf("albums = %v", c.Albums)
	}

	c = testRequestConfig()
	if applyControls(context.Background(), &c, "hall", "", "") || c.FilterFavorites {
		t.Error("favorites filter applied to another client")
	}
}

func TestFocusSelectExcludesOther(t *testing.T) {
	setupTestControls(t,
		map[string][]string{"kitchen": {"cabin", "summer"}},
		map[string][]string{"kitchen": {"anna", "ola"}},
	)

	var published []string
	publish := func(field, target string) { published = append(published, field+"@"+target) }
	albumSelect := focusSelect(fieldAlbum, "Album: show", "", albumFocusStore, fieldPerson, personFocusStore, publish)

	personFocusStore.Select("kitchen", "anna")
	if !albumSelect.Select("kitchen", "cabin") {
		t.Fatal("album select failed")
	}
	if _, ok := personFocusStore.Selected("kitchen"); ok {
		t.Error("person focus not reset when an album was picked")
	}
	if want := []string{"person@kitchen"}; !slices.Equal(published, want) {
		t.Errorf("published = %v, want %v", published, want)
	}

	// Default on one select leaves the other alone.
	personFocusStore.Select("kitchen", "ola")
	published = nil
	albumSelect.Select("kitchen", focus.DefaultOption)
	if _, ok := personFocusStore.Selected("kitchen"); !ok || len(published) != 0 {
		t.Error("Default on the album select reset the person focus")
	}
}

func TestControlEvent(t *testing.T) {
	setupTestControls(t, nil, nil)
	switchStore.Set("", fieldSleep, true)
	switchStore.Set("kitchen", fieldPause, true)

	decode := func(clientName string) controlPayload {
		var p controlPayload
		if err := json.Unmarshal([]byte(controlEvent(clientName).data), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}

	if p := decode("kitchen"); !p.Pause || !p.Sleep {
		t.Errorf("kitchen = %+v, want pause and sleep", p)
	}
	if p := decode("_global"); p.Pause || !p.Sleep {
		t.Errorf("unnamed client = %+v, want only sleep", p)
	}
	if !sleepForced("hall") || !sleepForced("") {
		t.Error("all-clients sleep not forced for every client")
	}
}

func TestPreviewJPEG(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1600, 800))
	for x := range 1600 {
		img.Set(x, 0, color.RGBA{R: 255, A: 255})
	}
	dataURI, err := utils.ImageToBase64(img, kiosk.MimeTypePng)
	if err != nil {
		t.Fatal(err)
	}

	preview := previewJPEG(dataURI)
	got, format, err := image.Decode(bytes.NewReader(preview))
	if err != nil {
		t.Fatal(err)
	}
	if format != "jpeg" || got.Bounds().Dx() != nowShowingPreviewSize || got.Bounds().Dy() != nowShowingPreviewSize/2 {
		t.Errorf("preview is %s %v", format, got.Bounds())
	}

	if previewJPEG("not a data uri") != nil {
		t.Error("previewJPEG accepted invalid data")
	}
}

func TestImmichAssetURL(t *testing.T) {
	c := &config.Config{ImmichURL: "http://immich:2283/"}
	if got := immichAssetURL(c, "abc"); got != "http://immich:2283/photos/abc" {
		t.Errorf("got %q", got)
	}
	c.ImmichExternalURL = "https://photos.example.com"
	if got := immichAssetURL(c, "abc"); got != "https://photos.example.com/photos/abc" {
		t.Errorf("got %q", got)
	}
}
