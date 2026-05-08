// Package billing wires Gumroad webhooks into the tenant plan model.
//
// Five products live in Gumroad:
//   - GMC_PROD_STARTER  — $19/mo subscription → plan='pro' (= "Starter")
//   - GMC_PROD_GROWTH   — $49/mo subscription → plan='growth'
//   - GMC_PROD_AGENCY   — $199/mo subscription → plan='agency'
//   - GMC_PROD_RESCUE   — $99 one-time charge — triggers a one-shot internal task
//   - GMC_PROD_DFY      — $499 one-time charge — operator notification
//
// The mapping from Gumroad product_id to one of the above is configured at
// runtime via env vars (set per environment in Gumroad's dashboard); we
// don't ship a hard-coded list, but we do ship a Catalog struct so callers
// can introspect what's configured.
package billing

import (
	"os"
	"strings"
)

type ProductKind string

const (
	KindStarter   ProductKind = "starter"
	KindGrowth    ProductKind = "growth"
	KindAgency    ProductKind = "agency"
	KindRescue    ProductKind = "rescue"
	KindDFY       ProductKind = "dfy"
	KindUnknown   ProductKind = "unknown"
)

// Product carries the metadata the webhook handler + billing UI need.
// PlanTier is the canonical tenants.plan value; empty for one-time charges.
type Product struct {
	Kind        ProductKind
	GumroadID   string
	Title       string
	PlanTier    string // "" for non-subscription products
	IsRecurring bool
	PriceCents  int    // for the comparison grid
	OverlayURL  string // pricing page launches Gumroad's overlay against this
}

// Catalog holds everything we know about Gumroad-side products.
//
// Construction reads env vars per kind. Missing values produce a "configured=false"
// product so the UI can render an upgrade button as a non-functional placeholder
// (or omit it) until the env is set.
type Catalog struct {
	ByKind     map[ProductKind]Product
	ByGumroadID map[string]Product
}

// LoadCatalog returns the runtime-configured catalog. The expected env keys
// are GUMROAD_PRODUCT_STARTER / _GROWTH / _AGENCY / _RESCUE / _DFY for the
// product permalinks (e.g. "gmc-starter") plus optional _URL overrides for
// the overlay URL.
func LoadCatalog() Catalog {
	defaults := []Product{
		{Kind: KindStarter, Title: "Starter", PlanTier: "pro",     IsRecurring: true, PriceCents: 1900},
		{Kind: KindGrowth,  Title: "Growth",  PlanTier: "growth",  IsRecurring: true, PriceCents: 4900},
		{Kind: KindAgency,  Title: "Agency",  PlanTier: "agency",  IsRecurring: true, PriceCents: 19900},
		{Kind: KindRescue,  Title: "Rescue Audit",        PlanTier: "", IsRecurring: false, PriceCents: 9900},
		{Kind: KindDFY,     Title: "DFY Reinstatement",   PlanTier: "", IsRecurring: false, PriceCents: 49900},
	}
	envKey := map[ProductKind]string{
		KindStarter: "GUMROAD_PRODUCT_STARTER",
		KindGrowth:  "GUMROAD_PRODUCT_GROWTH",
		KindAgency:  "GUMROAD_PRODUCT_AGENCY",
		KindRescue:  "GUMROAD_PRODUCT_RESCUE",
		KindDFY:     "GUMROAD_PRODUCT_DFY",
	}
	c := Catalog{
		ByKind:      map[ProductKind]Product{},
		ByGumroadID: map[string]Product{},
	}
	for _, p := range defaults {
		p.GumroadID = strings.TrimSpace(os.Getenv(envKey[p.Kind]))
		p.OverlayURL = strings.TrimSpace(os.Getenv(envKey[p.Kind] + "_URL"))
		if p.OverlayURL == "" && p.GumroadID != "" {
			// Standard Gumroad overlay URL pattern.
			p.OverlayURL = "https://gumroad.com/l/" + p.GumroadID
		}
		c.ByKind[p.Kind] = p
		if p.GumroadID != "" {
			c.ByGumroadID[p.GumroadID] = p
		}
	}
	return c
}

// LookupByGumroadID returns the matching product, or KindUnknown if the
// configured catalog doesn't include this Gumroad ID.
func (c Catalog) LookupByGumroadID(id string) Product {
	if p, ok := c.ByGumroadID[id]; ok {
		return p
	}
	return Product{Kind: KindUnknown, GumroadID: id}
}

// SubscriptionTiers returns the three subscription products in display order.
// Used by the comparison grid on the pricing + billing pages.
func (c Catalog) SubscriptionTiers() []Product {
	return []Product{
		c.ByKind[KindStarter],
		c.ByKind[KindGrowth],
		c.ByKind[KindAgency],
	}
}
