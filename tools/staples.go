package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Staple due-check: tracks the full order history in a JSON DB next to
// tokens.json (the API itself only exposes the last ~10 orders) and reports
// which shelf-stable items are due for restock based on each item's median
// reorder interval. The DB was seeded from Gmail order confirmations
// (Oct 2024 onwards) and is kept current automatically: every call fetches
// open+closed fulfillments and merges any orders not yet in the DB.

type staplesOrder struct {
	Ddate string         `json:"ddate"` // delivery date YYYY-MM-DD
	Items map[string]int `json:"items"` // product name -> quantity
}

type staplesDB map[string]staplesOrder // bestelnummer -> order

// canonicalization rules, applied to lowercased names after generic prefix strips
var canonRules = []struct {
	pat *regexp.Regexp
	rep string
}{
	{regexp.MustCompile(`^lay's (max .*|naturel value pack|naturel)$`), "lay's chips"},
	{regexp.MustCompile(`^99% fruitijs .*`), "99% fruitijs"},
	{regexp.MustCompile(`^perssinaasappel(en)?$`), "perssinaasappel"},
	{regexp.MustCompile(`^krieltjes( mix)? vastkokend$`), "krieltjes"},
	{regexp.MustCompile(`^(eieren s m l|blije kip eieren.*|scharreleieren.*)$`), "eieren"},
	{regexp.MustCompile(`^doritos sweet chilli.*`), "doritos sweet chilli"},
	{regexp.MustCompile(`^(warmgerookte zalmfilet|zalmfilet.*)$`), "zalmfilet (vers/gerookt)"},
	{regexp.MustCompile(`.*toiletpapier.*`), "toiletpapier"},
	{regexp.MustCompile(`^(eco )?pedaalemmerzak.*`), "pedaalemmerzakken"},
	{regexp.MustCompile(`^(528 premium ice balls|ijsblokjes)$`), "ijsblokjes"},
	{regexp.MustCompile(`.*(espresso bonen|espresso koffiebonen|koffiebonen).*`), "espressobonen"},
	{regexp.MustCompile(`.*extra vierge olijfolie.*`), "olijfolie extra vierge"},
	{regexp.MustCompile(`.*tandpasta.*`), "tandpasta"},
	{regexp.MustCompile(`.*(mondwater|mondspoeling).*`), "mondwater/-spoeling"},
	{regexp.MustCompile(`.*deodorant.*`), "deodorant"},
	{regexp.MustCompile(`.*shampoo.*`), "shampoo"},
	{regexp.MustCompile(`.*(vaatwaspoeder|machinereiniger|machine reiniger|spoelglans).*`), "vaatwas-onderhoud"},
	{regexp.MustCompile(`.*keukenpapier.*`), "keukenpapier"},
	{regexp.MustCompile(`.*stroopwafel.*`), "stroopwafels"},
	{regexp.MustCompile(`.*zwarte bonen.*`), "zwarte bonen"},
	{regexp.MustCompile(`^(iets kruimige|kruimige|vastkokende) aardappelen$`), "aardappelen"},
	{regexp.MustCompile(`^(paracetamol|ibuprofen|aleve).*`), "pijnstillers"},
}

var (
	stripBio   = regexp.MustCompile(`\bah biologisch(e)? |\bbiologische? `)
	stripAH    = regexp.MustCompile(`^ah `)
	stripGroot = regexp.MustCompile(` grootverpakking$`)
	wsRe       = regexp.MustCompile(`\s+`)

	pantryPat = regexp.MustCompile(
		`chips|crackers|scrocchi|pepsels|kroepoek|emping|tortilla|nacho|pringles` +
			`|borrelno|noten|noot|pinda|studentenhaver` +
			`|stroopwafel|biscoff|koek|reep |reep$|chocolade|snoep|drop|matties|katja|klene|venco|heksehyl|kruidnoten` +
			`|bonen|kikkererwten|kapucijners|linzen|tuinerwten|maiskorrels|mais$|tomatenblokjes|tomatenpulp` +
			`|tomatenconcentraat|pomodoro|san marzano tomaten|passata|kokosmelk|soep|cup-a-soup` +
			`|kappertjes|augurken|gurkentopf|cornichons|olijf|olijven|jalapeno plakjes|zuurkool|kimchi|wijnbladeren|artisjok` +
			`|rijst|pasta$|spaghett|tortellini|noodles|noedels|udon|ramen|couscous|meel|muesli|havermout` +
			`|olie|azijn|mayo|mosterd|saus|sambal|sriracha|salsa|harissa|hoisin|miso|ketjap|trassie|curry` +
			`|bouillon|kruiden|peper$|keukenzout|specerij|karwijzaad|oregano|smaakverfijner|maizena|tahin|spread` +
			`|confiture|jam$|hazelnootpasta|honing|siroop|pindakaas` +
			`|koffie|espresso|thee|earl grey` +
			`|cola|sprite|fanta|tonic|ginger beer|bitter lemon|royal club|mineraal water|bier|radler|cidre|wijn$|veltliner|nebbiolo` +
			`|alpro protein (pudding|sojadrink)|barista haver|creatine|vitamine`)
	householdPat = regexp.MustCompile(
		`toiletpapier|keukenpapier|pedaalemmerzak|vuilniszak|treksluitzak|zipper|folie|bakpapier` +
			`|vaatwas|afwasmiddel|spoelglans|machinereiniger|allesreiniger|schoonmaak|bleek|toiletblok` +
			`|wasmiddel|waspoeder|geurbooster|wasverzachter` +
			`|tandpasta|mondwater|mondspoeling|dental|deodorant|shampoo|conditioner|zeep|soap|lippenbalsem|text clay` +
			`|pijnstiller|paracetamol|ibuprofen|aleve|allegra|tissues|zakdoek` +
			`|batterij|lucifer|kaars|borstel|spons`)
	freshOverride = regexp.MustCompile(
		`snoepgroente|snijbonen|sperziebonen|bleekselderij|roomboter|vioblock` +
			`|^verse |vers geperst|gazpacho`)
)

func canonName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = wsRe.ReplaceAllString(s, " ")
	s = stripBio.ReplaceAllString(s, "")
	s = stripAH.ReplaceAllString(s, "")
	s = stripGroot.ReplaceAllString(s, "")
	for _, r := range canonRules {
		if r.pat.MatchString(s) {
			return r.rep
		}
	}
	return s
}

func isShelfStable(item string) bool {
	if freshOverride.MatchString(item) {
		return false
	}
	return pantryPat.MatchString(item) || householdPat.MatchString(item)
}

func staplesDBPath(deps Deps) string {
	return filepath.Join(filepath.Dir(deps.TokensPath), "staples_orders.json")
}

func loadStaplesDB(deps Deps) (staplesDB, error) {
	db := staplesDB{}
	b, err := os.ReadFile(staplesDBPath(deps))
	if err != nil {
		if os.IsNotExist(err) {
			return db, nil
		}
		return nil, err
	}
	return db, json.Unmarshal(b, &db)
}

func saveStaplesDB(deps Deps, db staplesDB) error {
	b, err := json.MarshalIndent(db, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(staplesDBPath(deps), b, 0o600)
}

// refreshStaplesDB merges every fulfillment (open + closed) not yet in the DB.
// Returns the number of newly added orders.
func refreshStaplesDB(ctx context.Context, deps Deps, db staplesDB) (int, error) {
	c, err := deps.GetClient()
	if err != nil {
		return 0, err
	}

	type idDate struct {
		id   int
		date string
	}
	var all []idDate

	open, err := c.GetFulfillments(ctx)
	if err != nil {
		return 0, fmt.Errorf("open fulfillments: %w", err)
	}
	for _, f := range open {
		all = append(all, idDate{f.OrderID, f.Delivery.Slot.Date})
	}

	const closedQuery = `query OrderFulfillmentsClosed {
  orderFulfillments(status: CLOSED) {
    result {
      orderId
      delivery { slot { date } }
    }
  }
}`
	var cr struct {
		OrderFulfillments struct {
			Result []struct {
				OrderID  int `json:"orderId"`
				Delivery struct {
					Slot struct {
						Date string `json:"date"`
					} `json:"slot"`
				} `json:"delivery"`
			} `json:"result"`
		} `json:"orderFulfillments"`
	}
	if err := c.DoGraphQL(ctx, closedQuery, nil, &cr); err == nil {
		for _, f := range cr.OrderFulfillments.Result {
			all = append(all, idDate{f.OrderID, f.Delivery.Slot.Date})
		}
	}

	added := 0
	for _, f := range all {
		key := fmt.Sprintf("%d", f.id)
		if _, ok := db[key]; ok {
			continue
		}
		if _, err := time.Parse("2006-01-02", f.date); err != nil {
			continue
		}
		order, err := c.GetOrderDetails(ctx, f.id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[Albert Heijn MCP] staples: could not fetch order %d: %v\n", f.id, err)
			continue
		}
		items := map[string]int{}
		for _, it := range order.Items {
			if it.Product != nil && it.Product.Title != "" {
				items[it.Product.Title] += it.Quantity
			}
		}
		if len(items) == 0 { // cancelled or empty
			continue
		}
		db[key] = staplesOrder{Ddate: f.date, Items: items}
		added++
	}
	return added, nil
}

func median(xs []int) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]int(nil), xs...)
	sort.Ints(s)
	n := len(s)
	if n%2 == 1 {
		return float64(s[n/2])
	}
	return float64(s[n/2-1]+s[n/2]) / 2
}

// RegisterStaplesTools registers the staple due-check MCP tool.
func RegisterStaplesTools(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_staples_due",
		mcp.WithTitleAnnotation("Albert Heijn: Staples Due for Restock"),
		mcp.WithDescription(
			"Check which shelf-stable staples (pantry + household items) are due for restock, "+
				"based on each item's median reorder interval over the full order history "+
				"(tracked server-side since Oct 2024; the AH API alone only exposes ~10 orders). "+
				"ALWAYS call this before composing or editing an AH order, and surface the DUE/soon "+
				"items to the user. History auto-refreshes from the AH API on every call. "+
				"Fresh produce is excluded by design (bought per-menu, not restocked).",
		),
		mcp.WithString("asof",
			mcp.Description("Evaluate as of this date (YYYY-MM-DD, default today). Use the planned delivery date."),
		),
		mcp.WithString("scope",
			mcp.Description("'shelf_stable' (default) or 'all' to include fresh/frozen items too."),
		),
		mcp.WithString("min_orders",
			mcp.Description("Minimum times an item must have been ordered to qualify (default 4)."),
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !deps.IsAuthenticated() {
			return notAuthResult(), nil
		}
		if err := refreshTokens(ctx, deps); err != nil {
			return errResult(fmt.Sprintf("Token refresh failed: %v", err)), nil
		}

		asof := time.Now()
		if v := req.GetString("asof", ""); v != "" {
			t, err := time.Parse("2006-01-02", v)
			if err != nil {
				return errResult("asof must be YYYY-MM-DD"), nil
			}
			asof = t
		}
		scope := req.GetString("scope", "shelf_stable")
		minOrders := req.GetInt("min_orders", 4)

		db, err := loadStaplesDB(deps)
		if err != nil {
			return errResult(fmt.Sprintf("staples DB error: %v", err)), nil
		}
		added, err := refreshStaplesDB(ctx, deps, db)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[Albert Heijn MCP] staples refresh failed (using stored history): %v\n", err)
		}
		if added > 0 {
			if err := saveStaplesDB(deps, db); err != nil {
				fmt.Fprintf(os.Stderr, "[Albert Heijn MCP] staples DB save failed: %v\n", err)
			}
		}

		// item -> sorted unique order dates
		byItem := map[string]map[string]bool{}
		for _, o := range db {
			for name := range o.Items {
				c := canonName(name)
				if byItem[c] == nil {
					byItem[c] = map[string]bool{}
				}
				byItem[c][o.Ddate] = true
			}
		}

		type dueEntry struct {
			Item       string  `json:"item"`
			Orders     int     `json:"orders"`
			MedianDays float64 `json:"median_interval_days"`
			Last       string  `json:"last_ordered"`
			DaysSince  int     `json:"days_since"`
			Status     string  `json:"status"` // due | soon
		}
		var entries []dueEntry
		for item, dset := range byItem {
			if len(dset) < minOrders {
				continue
			}
			if scope != "all" && !isShelfStable(item) {
				continue
			}
			var ds []time.Time
			for d := range dset {
				t, err := time.Parse("2006-01-02", d)
				if err == nil {
					ds = append(ds, t)
				}
			}
			sort.Slice(ds, func(i, j int) bool { return ds[i].Before(ds[j]) })
			var gaps []int
			for i := 1; i < len(ds); i++ {
				gaps = append(gaps, int(ds[i].Sub(ds[i-1]).Hours()/24))
			}
			med := median(gaps)
			if med <= 0 {
				continue
			}
			last := ds[len(ds)-1]
			since := int(asof.Sub(last).Hours() / 24)
			// lapsed: unseen for 3x its own cadence (min 90d) -> abandoned, skip
			lapse := 3 * med
			if lapse < 90 {
				lapse = 90
			}
			if float64(since) > lapse {
				continue
			}
			ratio := float64(since) / med
			status := ""
			if ratio >= 1.0 {
				status = "due"
			} else if ratio >= 0.75 {
				status = "soon"
			} else {
				continue
			}
			entries = append(entries, dueEntry{
				Item: item, Orders: len(dset), MedianDays: med,
				Last: last.Format("2006-01-02"), DaysSince: since, Status: status,
			})
		}
		sort.Slice(entries, func(i, j int) bool {
			ri := float64(entries[i].DaysSince) / entries[i].MedianDays
			rj := float64(entries[j].DaysSince) / entries[j].MedianDays
			return ri > rj
		})

		type result struct {
			Asof         string     `json:"asof"`
			OrdersInDB   int        `json:"orders_in_history"`
			OrdersAdded  int        `json:"orders_added_this_refresh"`
			Scope        string     `json:"scope"`
			Due          []dueEntry `json:"due"`
			Note         string     `json:"note"`
		}
		return jsonResult(result{
			Asof:        asof.Format("2006-01-02"),
			OrdersInDB:  len(db),
			OrdersAdded: added,
			Scope:       scope,
			Due:         entries,
			Note: "status 'due' = past its median reorder interval, 'soon' = past 75% of it. " +
				"Items unseen for 3x their cadence are treated as lapsed and omitted.",
		})
	})
}
