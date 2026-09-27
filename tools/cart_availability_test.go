package tools

import (
	"encoding/json"
	"os"
	"testing"
)

// basket_live.json is trimmed from a live REOPENED order (2026-09-27).
// 160707 (AH Spinazie grootverpakking) showed as "Tijdelijk uitverkocht" in
// the app; the rest were available, 171607 with the order-limit label.
func TestBasketAvailability_Live(t *testing.T) {
	data, err := os.ReadFile("testdata/basket_live.json")
	if err != nil {
		t.Fatal(err)
	}
	var resp basketAvailabilityResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatal(err)
	}
	got := parseBasketAvailability(resp)
	if len(got) != 4 {
		t.Fatalf("got %d verdicts, want 4", len(got))
	}
	for _, id := range []int{171607, 605946, 167873} {
		if !got[id].Available {
			t.Errorf("product %d: want available, got %+v", id, got[id])
		}
	}
	// online.status stays AVAILABLE for the sold-out item; only
	// unavailableForOrder and isOrderable give it away.
	sp := got[160707]
	if sp.Available || sp.Reason != "SOLD_OUT" || sp.Label != "Tijdelijk uitverkocht" {
		t.Errorf("spinach: want unavailable SOLD_OUT / Tijdelijk uitverkocht, got %+v", sp)
	}
}

// Edge cases of the rule beyond what the live capture covers.
func TestJudgeAvailability(t *testing.T) {
	yes, no := true, false
	avail := &availabilityIndication{Status: "AVAILABLE"}
	cases := []struct {
		name      string
		in        *productAvailability
		known     bool
		available bool
		reason    string
		from      string
	}{
		{"no data", nil, false, false, "", ""},
		{"no isOrderable", &productAvailability{Online: avail}, false, false, "", ""},
		{"all good", &productAvailability{IsOrderable: &yes, Online: avail}, true, true, "", ""},
		{"unavailableForOrder set", &productAvailability{IsOrderable: &yes, Online: avail,
			UnavailableForOrder: &availabilityIndication{Status: "SOME_REASON", AvailableFrom: "2026-10-05"}},
			true, false, "SOME_REASON", "2026-10-05"},
		{"unavailableForOrder without status", &productAvailability{IsOrderable: &yes, Online: avail,
			UnavailableForOrder: &availabilityIndication{}}, true, false, "UNAVAILABLE_FOR_ORDER", ""},
		{"online not AVAILABLE", &productAvailability{IsOrderable: &yes,
			Online: &availabilityIndication{Status: "OTHER"}}, true, false, "OTHER", ""},
		{"not orderable", &productAvailability{IsOrderable: &no, Online: avail}, true, false, "NOT_ORDERABLE", ""},
		{"no online block, orderable", &productAvailability{IsOrderable: &yes}, true, true, "", ""},
	}
	for _, c := range cases {
		av, known := judgeAvailability(c.in)
		if known != c.known || av.Available != c.available || av.Reason != c.reason || av.AvailableFrom != c.from {
			t.Errorf("%s: got %+v known=%v", c.name, av, known)
		}
	}
}
