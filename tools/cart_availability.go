package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	appie "github.com/gwillem/appie-go"
)

// Slot-level availability of the items in the active order.
//
// Source: the GraphQL basket (the query behind the app's cart screen), whose
// items carry Product.availability. Field names come from AH's GraphQL
// schema, and the known-good values were captured from a live REOPENED order
// (see testdata/basket_available.json):
//
//	isOrderable: true, online.status: "AVAILABLE", unavailableForOrder: null
//
// An item counts as available only when it shows exactly those values. Any
// deviation marks it unavailable and passes AH's own value through as the
// reason, so an unfamiliar enum value is reported rather than hidden.
//
// availabilityLabel is deliberately not a signal: live data shows it carries
// order limits too ("Maximaal 8 stuks") on perfectly available items.
//
// allocatedQuantity is not used either: it is 0 for every item until AH
// allocates stock after the order closes.

const basketAvailabilityQuery = `query BasketAvailability {
  basket {
    itemsInOrder {
      product {
        id
        availability {
          isOrderable
          availabilityLabel
          online { status availableFrom }
          unavailableForOrder { status availableFrom }
        }
      }
    }
  }
}`

type availabilityIndication struct {
	Status        string `json:"status"`
	AvailableFrom string `json:"availableFrom"`
}

type productAvailability struct {
	IsOrderable         *bool                   `json:"isOrderable"`
	AvailabilityLabel   string                  `json:"availabilityLabel"`
	Online              *availabilityIndication `json:"online"`
	UnavailableForOrder *availabilityIndication `json:"unavailableForOrder"`
}

type basketAvailabilityResponse struct {
	Basket struct {
		ItemsInOrder []struct {
			Product struct {
				ID           int                  `json:"id"`
				Availability *productAvailability `json:"availability"`
			} `json:"product"`
		} `json:"itemsInOrder"`
	} `json:"basket"`
}

// itemAvailability is the verdict for one cart item.
type itemAvailability struct {
	Available     bool   `json:"available"`
	Reason        string `json:"unavailable_reason,omitempty"`
	Label         string `json:"unavailable_label,omitempty"`
	AvailableFrom string `json:"available_from,omitempty"`
}

// judgeAvailability returns the verdict, and false when AH sent no
// availability data to judge.
func judgeAvailability(a *productAvailability) (itemAvailability, bool) {
	if a == nil || a.IsOrderable == nil {
		return itemAvailability{}, false
	}
	var reason, from string
	switch {
	case a.UnavailableForOrder != nil:
		reason, from = a.UnavailableForOrder.Status, a.UnavailableForOrder.AvailableFrom
		if reason == "" {
			reason = "UNAVAILABLE_FOR_ORDER"
		}
	case a.Online != nil && a.Online.Status != "AVAILABLE":
		reason, from = a.Online.Status, a.Online.AvailableFrom
	case !*a.IsOrderable:
		reason = "NOT_ORDERABLE"
	default:
		return itemAvailability{Available: true}, true
	}
	return itemAvailability{Reason: reason, Label: a.AvailabilityLabel, AvailableFrom: from}, true
}

// parseBasketAvailability maps product ID to verdict. Products without
// availability data are left out, so callers can tell "unknown" apart.
func parseBasketAvailability(resp basketAvailabilityResponse) map[int]itemAvailability {
	out := map[int]itemAvailability{}
	for _, it := range resp.Basket.ItemsInOrder {
		av, ok := judgeAvailability(it.Product.Availability)
		if !ok || it.Product.ID == 0 {
			continue
		}
		out[it.Product.ID] = av
		if !av.Available {
			// Log the raw fields: no unavailable item has been captured yet, so
			// the first real one is worth keeping.
			raw, _ := json.Marshal(it.Product.Availability)
			fmt.Fprintf(os.Stderr, "[Albert Heijn MCP] cart item %d unavailable: %s\n", it.Product.ID, raw)
		}
	}
	return out
}

func fetchBasketAvailability(ctx context.Context, c *appie.Client) (map[int]itemAvailability, error) {
	var resp basketAvailabilityResponse
	if err := c.DoGraphQL(ctx, basketAvailabilityQuery, nil, &resp); err != nil {
		return nil, err
	}
	return parseBasketAvailability(resp), nil
}
