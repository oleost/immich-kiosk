package switches

import "testing"

func TestActive(t *testing.T) {
	s := New()

	if s.Active("kitchen", "pause") {
		t.Fatal("switch on before it was set")
	}

	s.Set("kitchen", "pause", true)
	if !s.Active("kitchen", "pause") || s.Active("hall", "pause") || s.Active("", "pause") {
		t.Error("a client switch should only apply to that client")
	}

	// The all-clients switch applies to every client, and a client switch
	// that is off does not cancel it.
	s.Set("kitchen", "pause", false)
	s.Set("", "pause", true)
	if !s.Active("kitchen", "pause") || !s.Active("", "pause") {
		t.Error("the all-clients switch should apply to every client")
	}
	if s.Active("kitchen", "sleep") {
		t.Error("switches must be independent")
	}
}

func TestRestore(t *testing.T) {
	s := New()

	if !s.Restore("kitchen", "pause", true) || !s.Get("kitchen", "pause") {
		t.Fatal("retained state not applied")
	}

	// A switch set since startup is not replaced by a retained state.
	s.Set("", "sleep", false)
	if s.Restore("", "sleep", true) || s.Get("", "sleep") {
		t.Error("retained state overwrote a newer value")
	}
}
