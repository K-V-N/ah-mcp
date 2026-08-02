package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// The national bonus endpoints appie-go wraps (GetBonusProducts,
// GetSpotlightBonusProducts, GetBonusGroupProducts) hard-code time.Now() /
// periods[0], so they can only ever return the running bonus week. AH publishes
// next week a few days ahead (see nextPeriodVisibleFrom in the metadata), and
// an order delivered next week should be planned against that week's bonus.
// These helpers call the same endpoints with an explicit bonus week.

// bonusWeek is one published Albert Heijn bonus week plus the NATIONAL
// category names that week's bonus is split into.
type bonusWeek struct {
	StartDate  string
	EndDate    string
	Categories []string
}

// resolveBonusWeek finds the published bonus week covering date (YYYY-MM-DD).
// An empty date selects the current week — AH lists it first. Any day inside a
// week matches, so callers can pass a delivery date rather than a week start.
func resolveBonusWeek(ctx context.Context, deps Deps, date string) (*bonusWeek, error) {
	meta, err := fetchBonusPeriods(ctx, deps)
	if err != nil {
		return nil, err
	}
	if len(meta.Periods) == 0 {
		return nil, fmt.Errorf("no bonus periods published")
	}

	idx := -1
	if date == "" {
		idx = 0
	} else {
		for i, p := range meta.Periods {
			if date >= p.BonusStartDate && date <= p.BonusEndDate {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		published := make([]string, 0, len(meta.Periods))
		for _, p := range meta.Periods {
			published = append(published, p.BonusStartDate+".."+p.BonusEndDate)
		}
		return nil, fmt.Errorf("no bonus week covers %s; AH has published %s", date, strings.Join(published, ", "))
	}

	p := meta.Periods[idx]
	week := &bonusWeek{StartDate: p.BonusStartDate, EndDate: p.BonusEndDate}
	seen := make(map[string]bool)
	for _, tab := range p.Tabs {
		for _, m := range tab.URLMetadataList {
			if m.BonusType == "NATIONAL" && !seen[m.Description] {
				seen[m.Description] = true
				week.Categories = append(week.Categories, m.Description)
			}
		}
	}
	return week, nil
}

// fetchNationalSection retrieves one NATIONAL bonus category for the week
// containing date. The response uses the same envelope as the personal bonus
// sections: products and group deals mixed in bonusGroupOrProducts.
func fetchNationalSection(ctx context.Context, deps Deps, date, category string) (*personalBonusResponse, error) {
	params := url.Values{}
	params.Set("application", "AHWEBSHOP")
	params.Set("date", date)
	params.Set("promotionType", "NATIONAL")
	params.Set("category", category)
	return fetchSectionURL(ctx, deps, "/mobile-services/bonuspage/v2/section?"+params.Encode())
}

// fetchSpotlightSection retrieves the featured deals ("Uit de Bonusfolder") for
// the week containing date.
func fetchSpotlightSection(ctx context.Context, deps Deps, date string) (*personalBonusResponse, error) {
	params := url.Values{}
	params.Set("application", "AHWEBSHOP")
	params.Set("date", date)
	return fetchSectionURL(ctx, deps, "/mobile-services/bonuspage/v2/section/spotlight?"+params.Encode())
}

func fetchSectionURL(ctx context.Context, deps Deps, path string) (*personalBonusResponse, error) {
	c, err := deps.GetClient()
	if err != nil {
		return nil, fmt.Errorf("client error: %w", err)
	}
	var result personalBonusResponse
	if err := c.DoRequest(ctx, http.MethodGet, path, nil, &result); err != nil {
		return nil, fmt.Errorf("get bonus section failed (%s): %w", path, err)
	}
	return &result, nil
}

// bonusGroupProductsQuery is a trimmed copy of appie-go's
// FetchBonusPromotionWithProducts query, kept here only because appie-go's
// GetBonusGroupProducts resolves the period itself and always picks the
// current week. It returns just the fields ah_get_bonus_group_products emits.
const bonusGroupProductsQuery = `query FetchBonusPromotionWithProducts(
  $id: String,
  $periodStart: String,
  $periodEnd: String
) {
  bonusPromotions(
    input: {
      id: $id
      periodStart: $periodStart
      periodEnd: $periodEnd
      filterUnavailableProducts: true
      forcePromotionVisibility: true
      showAllPromotionSegments: true
    }
  ) {
    id
    products {
      id
      title
      salesUnitSize
      availability { isOrderable }
      priceV2(
        periodStart: $periodStart
        periodEnd: $periodEnd
        filterUnavailableProducts: true
        forcePromotionVisibility: true
      ) {
        now { amount }
        was { amount }
        promotionLabel { tiers { mechanism description } }
      }
      imagePack { large { url } }
    }
  }
}`

// bonusGroupProduct is one product inside a bonus promotion group.
type bonusGroupProduct struct {
	ID            int    `json:"id"`
	Title         string `json:"title"`
	SalesUnitSize string `json:"salesUnitSize"`
	Availability  struct {
		IsOrderable bool `json:"isOrderable"`
	} `json:"availability"`
	PriceV2 struct {
		Now struct {
			Amount float64 `json:"amount"`
		} `json:"now"`
		Was struct {
			Amount float64 `json:"amount"`
		} `json:"was"`
		PromotionLabel *struct {
			Tiers []struct {
				Mechanism   string `json:"mechanism"`
				Description string `json:"description"`
			} `json:"tiers"`
		} `json:"promotionLabel"`
	} `json:"priceV2"`
	ImagePack []struct {
		Large *struct {
			URL string `json:"url"`
		} `json:"large"`
	} `json:"imagePack"`
}

// mechanism returns the deal label ("2+1 GRATIS"), preferring the description
// over the raw mechanism, matching appie-go's mapping.
func (p *bonusGroupProduct) mechanism() string {
	if p.PriceV2.PromotionLabel == nil || len(p.PriceV2.PromotionLabel.Tiers) == 0 {
		return ""
	}
	tier := p.PriceV2.PromotionLabel.Tiers[0]
	if tier.Description != "" {
		return tier.Description
	}
	return tier.Mechanism
}

// imageURL returns the first large product image, if any.
func (p *bonusGroupProduct) imageURL() string {
	for _, pack := range p.ImagePack {
		if pack.Large != nil {
			return pack.Large.URL
		}
	}
	return ""
}

// fetchBonusGroupProducts resolves the products in a bonus promotion group for
// an explicit bonus week.
func fetchBonusGroupProducts(ctx context.Context, deps Deps, segmentID string, week *bonusWeek) ([]bonusGroupProduct, error) {
	c, err := deps.GetClient()
	if err != nil {
		return nil, fmt.Errorf("client error: %w", err)
	}
	var resp struct {
		BonusPromotions []struct {
			ID       string              `json:"id"`
			Products []bonusGroupProduct `json:"products"`
		} `json:"bonusPromotions"`
	}
	variables := map[string]any{
		"id":          segmentID,
		"periodStart": week.StartDate,
		"periodEnd":   week.EndDate,
	}
	if err := c.DoGraphQL(ctx, bonusGroupProductsQuery, variables, &resp); err != nil {
		return nil, fmt.Errorf("get bonus group products failed: %w", err)
	}
	if len(resp.BonusPromotions) == 0 {
		return nil, nil
	}
	return resp.BonusPromotions[0].Products, nil
}
