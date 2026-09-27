package tools

import (
	"encoding/json"
	"os"
	"testing"
)

// basket_available.json is trimmed from a live REOPENED order (2026-09-27).
// Every item in it was available; one carries the order-limit label.
func TestBasketAvailability_LiveAllAvailable(t *testing.T) {
	data, err := os.ReadFile("testdata/basket_available.json")
	if err != nil {
		t.Fatal(err)
	}
	var resp basketAvailabilityResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatal(err)
	}
	got := parseBasketAvailability(resp)
	if len(got) != 3 {
		t.Fatalf("got %d verdicts, want 3", len(got))
	}
	for id, av := range got {
		if !av.Available {
			t.Errorf("product %d: want available, got %+v", id, av)
		}
	}
	// "Maximaal 8 stuks" is an order limit, not unavailability.
	if !got[171607].Available {
		t.Error("order-limit label must not mark an item unavailable")
	}
}

// The cases below are not captured from AH (no unavailable item has been
// seen live yet); they check the rule on the schema's own fields.
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
