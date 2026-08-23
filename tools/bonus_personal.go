package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterPersonalBonusTools registers the personal Bonus Box tools.
func RegisterPersonalBonusTools(s *server.MCPServer, deps Deps) {
	registerGetBonusPeriods(s, deps)
	registerBonusSectionTool(s, deps, "ah_get_personal_bonus", "Albert Heijn: Personal Bonus Box", "personal",
		"Get the member's personalized Albert Heijn Bonus Box offers for a bonus week. "+
			"These are member-specific deals on top of the national bonus (ah_get_bonus_offers). "+
			"See also ah_get_choose_activate_offers for the wider Kies en Activeer selection.")
	registerBonusSectionTool(s, deps, "ah_get_choose_activate_offers", "Albert Heijn: Kies en Activeer Offers", "choose-and-activate",
		"Get the member's Albert Heijn 'Kies en Activeer' offers for a bonus week: "+
			"the full selection of personal offers that can be activated (activation_status ACTIVATABLE), "+
			"a superset of the Bonus Box shown by ah_get_personal_bonus.")
	registerActivateBonusOffer(s, deps)
}

// bonusPeriodsResponse matches /mobile-services/bonuspage/v3/metadata. Each
// period carries the tabs the bonus of that week is split into; the NATIONAL
// entries name the categories resolveBonusWeek fetches.
type bonusPeriodsResponse struct {
	Periods []struct {
		BonusStartDate string `json:"bonusStartDate"`
		BonusEndDate   string `json:"bonusEndDate"`
		Tabs           []struct {
			URLMetadataList []struct {
				BonusType   string `json:"bonusType"`
				Description string `json:"description"`
			} `json:"urlMetadataList"`
		} `json:"tabs"`
	} `json:"periods"`
}

// personalBonusResponse matches /mobile-services/bonuspage/v1/personal. It is
// the same envelope as the national bonus sections, plus personal-only product
// fields such as activationStatus.
type personalBonusResponse struct {
	SectionType          string `json:"sectionType"`
	SectionDescription   string `json:"sectionDescription"`
	BonusGroupOrProducts []struct {
		Product *struct {
			WebshopID        int     `json:"webshopId"`
			OfferID          int     `json:"offerId"`
			Title            string  `json:"title"`
			Brand            string  `json:"brand"`
			SalesUnitSize    string  `json:"salesUnitSize"`
			CurrentPrice     float64 `json:"currentPrice"`
			PriceBeforeBonus float64 `json:"priceBeforeBonus"`
			BonusMechanism   string  `json:"bonusMechanism"`
			ActivationStatus string  `json:"activationStatus"`
			MainCategory     string  `json:"mainCategory"`
		} `json:"product,omitempty"`
		BonusGroup *struct {
			ID                  string  `json:"id"`
			OfferID             int     `json:"offerId"`
			SegmentDescription  string  `json:"segmentDescription"`
			DiscountDescription string  `json:"discountDescription"`
			ActivationStatus    string  `json:"activationStatus"`
			Category            string  `json:"category"`
			SalesUnitSize       string  `json:"salesUnitSize"`
			ExampleFromPrice    float64 `json:"exampleFromPrice"`
			ExampleForPrice     float64 `json:"exampleForPrice"`
		} `json:"bonusGroup,omitempty"`
	} `json:"bonusGroupOrProducts"`
}

func fetchBonusPeriods(ctx context.Context, deps Deps) (*bonusPeriodsResponse, error) {
	c, err := deps.GetClient()
	if err != nil {
		return nil, fmt.Errorf("client error: %w", err)
	}
	var result bonusPeriodsResponse
	if err := c.DoRequest(ctx, http.MethodGet, "/mobile-services/bonuspage/v3/metadata", nil, &result); err != nil {
		return nil, fmt.Errorf("get bonus periods failed: %w", err)
	}
	return &result, nil
}

// fetchBonusSection retrieves a personalized bonus section ("personal" for the
// Bonus Box, "choose-and-activate" for Kies en Activeer) for a bonus week.
// Both endpoints share the same envelope.
func fetchBonusSection(ctx context.Context, deps Deps, section, startDate string) (*personalBonusResponse, error) {
	c, err := deps.GetClient()
	if err != nil {
		return nil, fmt.Errorf("client error: %w", err)
	}
	params := url.Values{}
	params.Set("bonusStartDate", startDate)
	var result personalBonusResponse
	if err := c.DoRequest(ctx, http.MethodGet, "/mobile-services/bonuspage/v1/"+section+"?"+params.Encode(), nil, &result); err != nil {
		return nil, fmt.Errorf("get %s bonus failed: %w", section, err)
	}
	return &result, nil
}

// --- ah_get_bonus_periods ---

func registerGetBonusPeriods(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_get_bonus_periods",
		mcp.WithTitleAnnotation("Albert Heijn: Bonus Periods"),
		mcp.WithDescription(
			"Get the Albert Heijn bonus weeks (periods), current week first. "+
				"AH publishes NEXT week's bonus a few days ahead (usually from the Friday before): "+
				"when it is available a second entry with label=\"next\" is present, and its offers "+
				"can already be read in full — pass its start_date to ah_get_bonus_offers (national bonus), "+
				"ah_get_personal_bonus or ah_get_choose_activate_offers. Do this whenever an order is "+
				"delivered in a later week: plan it against that week's bonus, not the running one. "+
				"Returns label (current/next), start_date and end_date per period (YYYY-MM-DD).",
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !deps.IsAuthenticated() {
			return notAuthResult(), nil
		}
		if err := refreshTokens(ctx, deps); err != nil {
			return errResult(fmt.Sprintf("Token refresh failed: %v", err)), nil
		}

		result, err := fetchBonusPeriods(ctx, deps)
		if err != nil {
			return errResult(fmt.Sprintf("Failed to get bonus periods: %v", err)), nil
		}

		type period struct {
			Label     string `json:"label"`
			StartDate string `json:"start_date"`
			EndDate   string `json:"end_date"`
		}
		periods := make([]period, 0, len(result.Periods))
		for i, p := range result.Periods {
			// AH lists the running week first, then any already-published
			// later weeks. Label them so the lookahead is obvious.
			label := fmt.Sprintf("+%d weeks", i)
			switch i {
			case 0:
				label = "current"
			case 1:
				label = "next"
			}
			periods = append(periods, period{Label: label, StartDate: p.BonusStartDate, EndDate: p.BonusEndDate})
		}
		return jsonResult(periods)
	})
}

// --- ah_get_personal_bonus ---

func registerBonusSectionTool(s *server.MCPServer, deps Deps, name, title, section, intro string) {
	tool := mcp.NewTool(name,
		mcp.WithTitleAnnotation(title),
		mcp.WithDescription(
			intro+" "+
				"Defaults to the current bonus week; pass bonus_start_date from ah_get_bonus_periods "+
				"to look ahead to next week. "+
				"Returns id, offer_id, title, brand, original_price, bonus_price, bonus_mechanism, activation_status. "+
				"Non-active offers can be enabled with ah_activate_bonus_offer (pass offer_id).",
		),
		mcp.WithString("bonus_start_date",
			mcp.Description("First day of the bonus week (YYYY-MM-DD) from ah_get_bonus_periods; empty = current week"),
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !deps.IsAuthenticated() {
			return notAuthResult(), nil
		}
		if err := refreshTokens(ctx, deps); err != nil {
			return errResult(fmt.Sprintf("Token refresh failed: %v", err)), nil
		}
		startDate := req.GetString("bonus_start_date", "")
		if startDate == "" {
			periods, err := fetchBonusPeriods(ctx, deps)
			if err != nil || len(periods.Periods) == 0 {
				return errResult(fmt.Sprintf("Failed to resolve current bonus week: %v", err)), nil
			}
			startDate = periods.Periods[0].BonusStartDate
		}

		result, err := fetchBonusSection(ctx, deps, section, startDate)
		if err != nil {
			return errResult(fmt.Sprintf("Failed to get %s offers (bonusStartDate=%s): %v", section, startDate, err)), nil
		}

		type item struct {
			ID               int     `json:"id,omitempty"`
			OfferID          int     `json:"offer_id,omitempty"`
			BonusSegmentID   string  `json:"bonus_segment_id,omitempty"`
			Title            string  `json:"title"`
			Brand            string  `json:"brand,omitempty"`
			Unit             string  `json:"unit,omitempty"`
			OriginalPrice    float64 `json:"original_price,omitempty"`
			BonusPrice       float64 `json:"bonus_price,omitempty"`
			BonusMechanism   string  `json:"bonus_mechanism,omitempty"`
			ActivationStatus string  `json:"activation_status,omitempty"`
			Category         string  `json:"category,omitempty"`
		}
		type response struct {
			BonusStartDate string `json:"bonus_start_date"`
			Items          []item `json:"items"`
		}

		items := make([]item, 0, len(result.BonusGroupOrProducts))
		for _, e := range result.BonusGroupOrProducts {
			if e.Product != nil {
				p := e.Product
				items = append(items, item{
					ID:               p.WebshopID,
					OfferID:          p.OfferID,
					Title:            p.Title,
					Brand:            p.Brand,
					Unit:             p.SalesUnitSize,
					OriginalPrice:    p.PriceBeforeBonus,
					BonusPrice:       p.CurrentPrice,
					BonusMechanism:   p.BonusMechanism,
					ActivationStatus: p.ActivationStatus,
					Category:         p.MainCategory,
				})
			}
			if e.BonusGroup != nil {
				g := e.BonusGroup
				items = append(items, item{
					OfferID:          g.OfferID,
					BonusSegmentID:   g.ID,
					Title:            g.SegmentDescription,
					Unit:             g.SalesUnitSize,
					OriginalPrice:    g.ExampleFromPrice,
					BonusPrice:       g.ExampleForPrice,
					BonusMechanism:   g.DiscountDescription,
					ActivationStatus: g.ActivationStatus,
					Category:         g.Category,
				})
			}
		}
		return jsonResult(response{BonusStartDate: startDate, Items: items})
	})
}

// --- ah_activate_bonus_offer ---

func registerActivateBonusOffer(s *server.MCPServer, deps Deps) {
	tool := mcp.NewTool("ah_activate_bonus_offer",
		mcp.WithTitleAnnotation("Albert Heijn: Activate Bonus Box Offer"),
		mcp.WithDescription(
			"Activate a personal Albert Heijn bonus offer so the discount applies to the member's purchases. "+
				"Get offer_id from ah_get_personal_bonus or ah_get_choose_activate_offers "+
				"(activation_status ACTIVATABLE or NOT_ACTIVATED). "+
				"The bonus week and segment are resolved automatically; pass bonus_start_date only to disambiguate. "+
				"Activation is idempotent — activating an already-active offer is a no-op.",
		),
		mcp.WithString("offer_id",
			mcp.Required(),
			mcp.Description("Numeric offer_id from ah_get_personal_bonus"),
		),
		mcp.WithString("bonus_start_date",
			mcp.Description("Bonus week start date (YYYY-MM-DD); empty = search current and next week"),
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !deps.IsAuthenticated() {
			return notAuthResult(), nil
		}
		if err := refreshTokens(ctx, deps); err != nil {
			return errResult(fmt.Sprintf("Token refresh failed: %v", err)), nil
		}
		c, err := deps.GetClient()
		if err != nil {
			return errResult(fmt.Sprintf("Client error: %v", err)), nil
		}

		offerID := req.GetInt("offer_id", 0)
		if offerID == 0 {
			return errResult("offer_id is required and must be a number"), nil
		}

		// Resolve the offer's bonus week and segment by scanning the personal
		// Bonus Box for the requested week, or all published weeks.
		var dates []string
		if d := req.GetString("bonus_start_date", ""); d != "" {
			dates = []string{d}
		} else {
			periods, err := fetchBonusPeriods(ctx, deps)
			if err != nil {
				return errResult(fmt.Sprintf("Failed to get bonus periods: %v", err)), nil
			}
			for _, p := range periods.Periods {
				dates = append(dates, p.BonusStartDate)
			}
		}

		var segmentID, startDate string
		for _, d := range dates {
			for _, section := range []string{"choose-and-activate", "personal"} {
				listing, err := fetchBonusSection(ctx, deps, section, d)
				if err != nil {
					continue
				}
				for _, e := range listing.BonusGroupOrProducts {
					if e.BonusGroup != nil && e.BonusGroup.OfferID == offerID {
						segmentID, startDate = e.BonusGroup.ID, d
					}
					if e.Product != nil && e.Product.OfferID == offerID {
						startDate = d
					}
				}
				if startDate != "" {
					break
				}
			}
			if startDate != "" {
				break
			}
		}
		if startDate == "" {
			return errResult(fmt.Sprintf("Offer %d not found in the personal or Kies en Activeer offers for weeks %v", offerID, dates)), nil
		}

		params := url.Values{}
		params.Set("segmentId", segmentID)
		params.Set("startDate", startDate)
		path := fmt.Sprintf("/mobile-services/bonuspage/v1/activate/%d?%s", offerID, params.Encode())

		// Body must be non-nil: AH's CDN rejects PATCH without Content-Length.
		var result struct {
			BonusGroup *struct {
				OfferID          int    `json:"offerId"`
				ActivationStatus string `json:"activationStatus"`
			} `json:"bonusGroup"`
		}
		if err := c.DoRequest(ctx, http.MethodPatch, path, map[string]any{}, &result); err != nil {
			return errResult(fmt.Sprintf("Failed to activate offer %d: %v", offerID, err)), nil
		}

		status := "ACTIVATED"
		if result.BonusGroup != nil && result.BonusGroup.ActivationStatus != "" {
			status = result.BonusGroup.ActivationStatus
		}
		return jsonResult(map[string]any{
			"offer_id":          offerID,
			"bonus_start_date":  startDate,
			"activation_status": status,
		})
	})
}
