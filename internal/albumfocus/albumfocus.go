// Package albumfocus lets Home Assistant narrow the slideshow down to one of
// the albums a kiosk already shows.
//
// The all-clients target ("") offers the albums from config.yaml; a named
// client offers the albums it requests (its URL, or config.yaml when the URL
// has none). A selection made for a client wins over the all-clients
// selection, and DefaultOption means "no focus": the client shows its normal
// sources, or follows the all-clients selection.
package albumfocus

import (
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"charm.land/log/v2"

	"github.com/damongolding/immich-kiosk/internal/kiosk"
)

// DefaultOption is the select option (and state) meaning "no focus".
const DefaultOption = "Default"

// minAlbums is the number of albums a target needs before a select is offered.
const minAlbums = 2

// keywordLabels are the labels of the album keywords Kiosk understands.
var keywordLabels = map[string]string{
	kiosk.AlbumKeywordAll:        "All albums",
	kiosk.AlbumKeywordOwned:      "Owned albums",
	kiosk.AlbumKeywordShared:     "Shared albums",
	kiosk.AlbumKeywordFavourites: "Favorites",
	kiosk.AlbumKeywordFavorites:  "Favorites",
}

// NameResolver returns the Immich album name for each entry, in the same
// order; an empty name means the album could not be found.
type NameResolver func(entries []string) []string

// album is one configured album entry and its select label.
type album struct {
	entry string
	label string
}

// albumList holds the albums of one target.
type albumList struct {
	entries []string
	albums  []album
	// gen guards against an older name lookup overwriting a newer one.
	gen int
}

// Store holds the albums of each target and the selected focus.
type Store struct {
	mu    sync.Mutex
	lists map[string]*albumList // target ("" = all clients) -> albums
	// selected holds labels. An explicit DefaultOption is stored as "" so a
	// retained state cannot overwrite it.
	selected map[string]string

	resolve  NameResolver
	onChange func(target string)
}

// New creates a Store that looks up album names with resolve.
func New(resolve NameResolver) *Store {
	return &Store{
		lists:    make(map[string]*albumList),
		selected: make(map[string]string),
		resolve:  resolve,
	}
}

// SetOnChange registers a callback run after a target's options changed.
func (s *Store) SetOnChange(fn func(target string)) {
	s.onChange = fn
}

// Learn records the albums of target. Album names are looked up in the
// background when the list differs from the one already known.
func (s *Store) Learn(target string, albums []string) {
	entries := cleanEntries(albums)

	s.mu.Lock()
	l, ok := s.lists[target]
	if ok && slices.Equal(l.entries, entries) {
		s.mu.Unlock()
		return
	}
	if !ok {
		l = &albumList{}
		s.lists[target] = l
	}
	l.entries = entries
	l.gen++
	gen := l.gen
	s.mu.Unlock()

	go s.label(target, entries, gen)
}

// label looks up the album names of entries and stores the labels.
func (s *Store) label(target string, entries []string, gen int) {
	albums := buildLabels(entries, s.lookupNames(entries))

	s.mu.Lock()
	l := s.lists[target]
	if l == nil || l.gen != gen {
		s.mu.Unlock()
		return
	}
	l.albums = albums
	s.mu.Unlock()

	log.Debug("album focus: albums updated", "target", target, "albums", len(albums))

	if s.onChange != nil {
		s.onChange(target)
	}
}

// lookupNames resolves the names of the entries that are not keywords.
func (s *Store) lookupNames(entries []string) []string {
	names := make([]string, len(entries))
	if s.resolve == nil {
		return names
	}

	var ids []string
	var idx []int
	for i, e := range entries {
		if _, ok := keywordLabels[strings.ToLower(e)]; !ok {
			ids = append(ids, e)
			idx = append(idx, i)
		}
	}
	if len(ids) == 0 {
		return names
	}

	resolved := s.resolve(ids)
	for j, i := range idx {
		if j < len(resolved) {
			names[i] = resolved[j]
		}
	}
	return names
}

// Options returns the select options of target, DefaultOption first. known is
// false while the target's albums are not known yet; options is nil when the
// target has too few albums to offer a select.
func (s *Store) Options(target string) (options []string, known bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	l := s.lists[target]
	if l == nil || (l.albums == nil && len(l.entries) > 0) {
		return nil, false
	}
	if len(l.albums) < minAlbums {
		return nil, true
	}

	options = []string{DefaultOption}
	for _, a := range l.albums {
		options = append(options, a.label)
	}
	return options, true
}

// Select sets the focus of target to label, or clears it with DefaultOption.
// It reports false when the label is not one of target's albums. While the
// albums are not known yet the label is kept and resolved later.
func (s *Store) Select(target, label string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if strings.EqualFold(label, DefaultOption) {
		s.selected[target] = ""
		return true
	}

	l := s.lists[target]
	if l != nil && l.albums != nil {
		a, ok := findLabel(l.albums, label)
		if !ok {
			return false
		}
		label = a.label
	}
	s.selected[target] = label
	return true
}

// Restore sets the focus of target from a retained state, unless a selection
// was already made since startup. It reports whether the state was applied.
func (s *Store) Restore(target, label string) bool {
	s.mu.Lock()
	_, ok := s.selected[target]
	s.mu.Unlock()
	return !ok && s.Select(target, label)
}

// Current returns the state of target's select: the selected label, or
// DefaultOption when nothing that target offers is selected.
func (s *Store) Current(target string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a, ok := s.selection(target); ok {
		return a.label
	}
	return DefaultOption
}

// Active returns the album entry clientName should show. A focus set for the
// client wins over the all-clients focus.
func (s *Store) Active(clientName string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if clientName != "" {
		if a, ok := s.selection(clientName); ok {
			return a.entry, true
		}
	}
	if a, ok := s.selection(""); ok {
		return a.entry, true
	}
	return "", false
}

// selection resolves the label selected for target. s.mu must be held.
func (s *Store) selection(target string) (album, bool) {
	label := s.selected[target]
	if label == "" {
		return album{}, false
	}
	l := s.lists[target]
	if l == nil {
		return album{}, false
	}
	return findLabel(l.albums, label)
}

func findLabel(albums []album, label string) (album, bool) {
	for _, a := range albums {
		if strings.EqualFold(a.label, label) {
			return a, true
		}
	}
	return album{}, false
}

// cleanEntries drops empty and "none" entries and duplicates, keeping order.
func cleanEntries(albums []string) []string {
	entries := []string{}
	for _, a := range albums {
		a = strings.TrimSpace(a)
		if a == "" || strings.EqualFold(a, "none") || slices.Contains(entries, a) {
			continue
		}
		entries = append(entries, a)
	}
	return entries
}

// buildLabels gives every entry a unique label, sorted by label. Keywords get
// a fixed label, albums of another user get the user name appended, and an
// album without a name falls back to its ID.
func buildLabels(entries, names []string) []album {
	albums := make([]album, 0, len(entries))
	for i, entry := range entries {
		albums = append(albums, album{entry: entry, label: entryLabel(entry, names[i])})
	}

	sort.SliceStable(albums, func(i, j int) bool {
		return strings.ToLower(albums[i].label) < strings.ToLower(albums[j].label)
	})

	// Home Assistant select options must be unique.
	seen := map[string]bool{strings.ToLower(DefaultOption): true}
	for i := range albums {
		label := albums[i].label
		for n := 2; seen[strings.ToLower(label)]; n++ {
			label = albums[i].label + " (" + strconv.Itoa(n) + ")"
		}
		seen[strings.ToLower(label)] = true
		albums[i].label = label
	}
	return albums
}

func entryLabel(entry, name string) string {
	if label, ok := keywordLabels[strings.ToLower(entry)]; ok {
		return label
	}

	id, user, hasUser := strings.Cut(entry, kiosk.MultipleUserIndicator)
	label := strings.Join(strings.Fields(name), " ")
	if label == "" {
		label = id
	}
	if hasUser {
		label += " (" + user + ")"
	}
	return label
}
