package focus

import (
	"slices"
	"strings"
	"testing"
	"time"
)

var testNames = map[string]string{
	"id-summer": "Summer 2025",
	"id-cabin":  "Cabin",
	"id-cabin2": "Cabin",
	"id-bday":   "Birthdays",
}

func testResolver(entries []string) []string {
	names := make([]string, len(entries))
	for i, e := range entries {
		id, _, _ := strings.Cut(e, "@")
		names[i] = testNames[id]
	}
	return names
}

// newTestStore returns a store where target has learned albums.
func newTestStore(t *testing.T, lists map[string][]string) *Store {
	t.Helper()
	s := New(AlbumKeywords, testResolver)
	changed := make(chan string, len(lists))
	s.SetOnChange(func(target string) { changed <- target })
	for target, albums := range lists {
		s.Learn(target, albums)
	}
	for range lists {
		select {
		case <-changed:
		case <-time.After(time.Second):
			t.Fatal("album names not resolved")
		}
	}
	return s
}

func TestOptions(t *testing.T) {
	s := newTestStore(t, map[string][]string{
		"":      {"id-summer", "id-cabin", "id-cabin2", "none", "id-summer", "id-unknown", "shared"},
		"one":   {"id-bday"},
		"multi": {"id-bday@anna", "favorites"},
	})

	got, known := s.Options("")
	want := []string{DefaultOption, "Cabin", "Cabin (2)", "Shared albums", "Summer 2025", "Unnamed (id-unkno)"}
	if !known || !slices.Equal(got, want) {
		t.Errorf("Options(\"\") = %v, %v; want %v", got, known, want)
	}

	if got, known := s.Options("one"); !known || got != nil {
		t.Errorf("Options(one) = %v, %v; want nil, true", got, known)
	}

	want = []string{DefaultOption, "Birthdays (anna)", "Favorites"}
	if got, _ := s.Options("multi"); !slices.Equal(got, want) {
		t.Errorf("Options(multi) = %v; want %v", got, want)
	}

	if _, known := s.Options("unseen"); known {
		t.Error("Options(unseen) reported known")
	}
}

func TestActive(t *testing.T) {
	s := newTestStore(t, map[string][]string{
		"":        {"id-summer", "id-cabin"},
		"kitchen": {"id-bday", "id-summer"},
	})

	if _, ok := s.Active("kitchen"); ok {
		t.Fatal("focus active before any selection")
	}

	// The all-clients focus applies to every client, also when the album is
	// not one of the client's own.
	if !s.Select("", "cabin") {
		t.Fatal("Select(\"\", cabin) failed")
	}
	if got, _ := s.Active("kitchen"); got != "id-cabin" {
		t.Errorf("kitchen shows %q, want id-cabin", got)
	}
	if got, _ := s.Active(""); got != "id-cabin" {
		t.Errorf("unnamed client shows %q, want id-cabin", got)
	}

	// A client focus wins over the all-clients focus.
	if !s.Select("kitchen", "Birthdays") {
		t.Fatal("Select(kitchen, Birthdays) failed")
	}
	if got, _ := s.Active("kitchen"); got != "id-bday" {
		t.Errorf("kitchen shows %q, want id-bday", got)
	}
	if got := s.Current("kitchen"); got != "Birthdays" {
		t.Errorf("Current(kitchen) = %q", got)
	}

	// Default on the client follows the all-clients focus again.
	s.Select("kitchen", DefaultOption)
	if got, _ := s.Active("kitchen"); got != "id-cabin" {
		t.Errorf("kitchen shows %q, want id-cabin", got)
	}

	s.Select("", DefaultOption)
	if _, ok := s.Active("kitchen"); ok {
		t.Error("focus still active after Default")
	}

	if s.Select("", "Birthdays") {
		t.Error("Select accepted an album that is not in the list")
	}
}

func TestSelected(t *testing.T) {
	s := newTestStore(t, map[string][]string{
		"":        {"id-summer", "id-cabin"},
		"kitchen": {"id-bday", "id-summer"},
	})
	s.Select("", "Cabin")

	// Selected does not fall back to the all-clients focus.
	if _, ok := s.Selected("kitchen"); ok {
		t.Error("Selected(kitchen) fell back to the all-clients focus")
	}
	if got, ok := s.Selected(""); !ok || got != "id-cabin" {
		t.Errorf("Selected(\"\") = %q, %v", got, ok)
	}
}

func TestPersonKeywords(t *testing.T) {
	s := New(PersonKeywords, testResolver)
	changed := make(chan string, 1)
	s.SetOnChange(func(target string) { changed <- target })
	s.Learn("", []string{"all", "id-bday"})
	<-changed

	if got, _ := s.Options(""); !slices.Equal(got, []string{DefaultOption, "All people", "Birthdays"}) {
		t.Errorf("Options = %v", got)
	}
}

func TestRestore(t *testing.T) {
	s := New(AlbumKeywords, testResolver)
	changed := make(chan string, 1)
	s.SetOnChange(func(target string) { changed <- target })

	// A retained state arrives before the client's albums are known.
	s.Restore("kitchen", "Cabin")
	if got := s.Current("kitchen"); got != DefaultOption {
		t.Errorf("Current before albums known = %q", got)
	}

	s.Learn("kitchen", []string{"id-cabin", "id-summer"})
	<-changed
	if got, _ := s.Active("kitchen"); got != "id-cabin" {
		t.Errorf("kitchen shows %q, want id-cabin", got)
	}

	// A selection made since startup is not replaced by a retained state.
	s.Select("kitchen", DefaultOption)
	s.Restore("kitchen", "Cabin")
	if _, ok := s.Active("kitchen"); ok {
		t.Error("retained state overwrote a newer selection")
	}
}
