// Package focus lets Home Assistant narrow the slideshow down to one of the
// items (albums or people) a kiosk already shows.
//
// The all-clients target ("") offers the items from config.yaml; a named
// client offers the items it requests (its URL, or config.yaml when the URL
// has none). A selection made for a client wins over the all-clients
// selection, and DefaultOption means "no focus": the client shows its normal
// sources, or follows the all-clients selection.
package focus

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

// minItems is the number of items a target needs before a select is offered.
const minItems = 2

// AlbumKeywords are the labels of the album keywords Kiosk understands.
var AlbumKeywords = map[string]string{
	kiosk.AlbumKeywordAll:        "All albums",
	kiosk.AlbumKeywordOwned:      "Owned albums",
	kiosk.AlbumKeywordShared:     "Shared albums",
	kiosk.AlbumKeywordFavourites: "Favorites",
	kiosk.AlbumKeywordFavorites:  "Favorites",
}

// PersonKeywords are the labels of the person keywords Kiosk understands.
var PersonKeywords = map[string]string{
	kiosk.PersonKeywordAll: "All people",
}

// NameResolver returns the Immich name of each entry, in the same order; an
// empty name means the item could not be found.
type NameResolver func(entries []string) []string

// item is one configured entry and its select label.
type item struct {
	entry string
	label string
}

// itemList holds the items of one target.
type itemList struct {
	entries []string
	items   []item
	// gen guards against an older name lookup overwriting a newer one.
	gen int
}

// Store holds the items of each target and the selected focus.
type Store struct {
	mu    sync.Mutex
	lists map[string]*itemList // target ("" = all clients) -> items
	// selected holds labels. An explicit DefaultOption is stored as "" so a
	// retained state cannot overwrite it.
	selected map[string]string

	keywords map[string]string
	resolve  NameResolver
	onChange func(target string)
}

// New creates a Store that labels keywords with keywords and looks up the
// names of other entries with resolve.
func New(keywords map[string]string, resolve NameResolver) *Store {
	return &Store{
		lists:    make(map[string]*itemList),
		selected: make(map[string]string),
		keywords: keywords,
		resolve:  resolve,
	}
}

// SetOnChange registers a callback run after a target's options changed.
func (s *Store) SetOnChange(fn func(target string)) {
	s.onChange = fn
}

// Learn records the items of target. Names are looked up in the background
// when the list differs from the one already known.
func (s *Store) Learn(target string, entries []string) {
	entries = cleanEntries(entries)

	s.mu.Lock()
	l, ok := s.lists[target]
	if ok && slices.Equal(l.entries, entries) {
		s.mu.Unlock()
		return
	}
	if !ok {
		l = &itemList{}
		s.lists[target] = l
	}
	l.entries = entries
	l.gen++
	gen := l.gen
	s.mu.Unlock()

	go s.label(target, entries, gen)
}

// label looks up the names of entries and stores the labels.
func (s *Store) label(target string, entries []string, gen int) {
	items := s.buildLabels(entries, s.lookupNames(entries))

	s.mu.Lock()
	l := s.lists[target]
	if l == nil || l.gen != gen {
		s.mu.Unlock()
		return
	}
	l.items = items
	s.mu.Unlock()

	log.Debug("focus: options updated", "target", target, "items", len(items))

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
		if _, ok := s.keywords[strings.ToLower(e)]; !ok {
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
// false while the target's items are not known yet; options is nil when the
// target has too few items to offer a select.
func (s *Store) Options(target string) (options []string, known bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	l := s.lists[target]
	if l == nil || (l.items == nil && len(l.entries) > 0) {
		return nil, false
	}
	if len(l.items) < minItems {
		return nil, true
	}

	options = []string{DefaultOption}
	for _, it := range l.items {
		options = append(options, it.label)
	}
	return options, true
}

// Select sets the focus of target to label, or clears it with DefaultOption.
// It reports false when the label is not one of target's items. While the
// items are not known yet the label is kept and resolved later.
func (s *Store) Select(target, label string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if strings.EqualFold(label, DefaultOption) {
		s.selected[target] = ""
		return true
	}

	l := s.lists[target]
	if l != nil && l.items != nil {
		it, ok := findLabel(l.items, label)
		if !ok {
			return false
		}
		label = it.label
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
	if it, ok := s.selection(target); ok {
		return it.label
	}
	return DefaultOption
}

// Selected returns the entry selected for exactly target, without falling
// back to the all-clients selection.
func (s *Store) Selected(target string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.selection(target)
	return it.entry, ok
}

// Active returns the entry clientName should show. A focus set for the client
// wins over the all-clients focus.
func (s *Store) Active(clientName string) (string, bool) {
	if clientName != "" {
		if entry, ok := s.Selected(clientName); ok {
			return entry, true
		}
	}
	return s.Selected("")
}

// selection resolves the label selected for target. s.mu must be held.
func (s *Store) selection(target string) (item, bool) {
	label := s.selected[target]
	if label == "" {
		return item{}, false
	}
	l := s.lists[target]
	if l == nil {
		return item{}, false
	}
	return findLabel(l.items, label)
}

func findLabel(items []item, label string) (item, bool) {
	for _, it := range items {
		if strings.EqualFold(it.label, label) {
			return it, true
		}
	}
	return item{}, false
}

// cleanEntries drops empty and "none" entries and duplicates, keeping order.
func cleanEntries(entries []string) []string {
	cleaned := []string{}
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" || strings.EqualFold(e, "none") || slices.Contains(cleaned, e) {
			continue
		}
		cleaned = append(cleaned, e)
	}
	return cleaned
}

// buildLabels gives every entry a unique label, sorted by label. Keywords get
// a fixed label, items of another user get the user name appended, and an
// item without a name is shown as "Unnamed (<start of its ID>)".
func (s *Store) buildLabels(entries, names []string) []item {
	items := make([]item, 0, len(entries))
	for i, entry := range entries {
		items = append(items, item{entry: entry, label: s.entryLabel(entry, names[i])})
	}

	sort.SliceStable(items, func(i, j int) bool {
		return strings.ToLower(items[i].label) < strings.ToLower(items[j].label)
	})

	// Home Assistant select options must be unique.
	seen := map[string]bool{strings.ToLower(DefaultOption): true}
	for i := range items {
		label := items[i].label
		for n := 2; seen[strings.ToLower(label)]; n++ {
			label = items[i].label + " (" + strconv.Itoa(n) + ")"
		}
		seen[strings.ToLower(label)] = true
		items[i].label = label
	}
	return items
}

func (s *Store) entryLabel(entry, name string) string {
	if label, ok := s.keywords[strings.ToLower(entry)]; ok {
		return label
	}

	id, user, hasUser := strings.Cut(entry, kiosk.MultipleUserIndicator)
	label := strings.Join(strings.Fields(name), " ")
	if label == "" {
		label = "Unnamed (" + shortID(id) + ")"
	}
	if hasUser {
		label += " (" + user + ")"
	}
	return label
}

// shortID returns the first block of a UUID, enough to tell items apart.
func shortID(id string) string {
	if r := []rune(id); len(r) > 8 {
		return string(r[:8])
	}
	return id
}
