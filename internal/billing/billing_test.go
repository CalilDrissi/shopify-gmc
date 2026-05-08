package billing

import (
	"net/url"
	"testing"
	"time"
)

// ----------------------------------------------------------------------------
// VerifySignature
// ----------------------------------------------------------------------------

func TestVerifySignature_GoodAndBad(t *testing.T) {
	secret := []byte("the-shared-secret")
	body := []byte("sale_id=abc&product_permalink=gmc-growth&tenant_id=00000000-0000-0000-0000-000000000001")
	good := SignBody(secret, body)

	if !VerifySignature(secret, body, good) {
		t.Fatal("VerifySignature(secret, body, good) = false; want true")
	}
	if VerifySignature(secret, body, "deadbeef") {
		t.Fatal("VerifySignature with junk hex = true; want false")
	}
	if VerifySignature(secret, body, "") {
		t.Fatal("VerifySignature with empty signature = true; want false")
	}
	if VerifySignature(nil, body, good) {
		t.Fatal("VerifySignature with nil secret = true; want false")
	}
	// Tampered body must fail with the original signature.
	tampered := []byte("sale_id=abc&product_permalink=gmc-AGENCY&tenant_id=00000000-0000-0000-0000-000000000001")
	if VerifySignature(secret, tampered, good) {
		t.Fatal("tampered body verified true; should be false")
	}
}

// ----------------------------------------------------------------------------
// ParseForm
// ----------------------------------------------------------------------------

func TestParseForm_SaleSubscription(t *testing.T) {
	v := url.Values{}
	v.Set("sale_id", "sale-123")
	v.Set("product_permalink", "gmc-growth")
	v.Set("price_cents", "4900")
	v.Set("currency", "USD")
	v.Set("recurrence", "monthly")
	v.Set("subscription_id", "sub-9")
	v.Set("tenant_id", "00000000-0000-0000-0000-000000000001")
	v.Set("user_email", "buyer@example.com")
	v.Set("sale_timestamp", time.Now().UTC().Format(time.RFC3339))

	e := ParseForm(v)
	if e.Type != "sale" {
		t.Errorf("Type=%q want sale", e.Type)
	}
	if e.SaleID != "sale-123" {
		t.Errorf("SaleID=%q", e.SaleID)
	}
	if e.ProductID != "gmc-growth" {
		t.Errorf("ProductID=%q", e.ProductID)
	}
	if e.PriceCents != 4900 {
		t.Errorf("PriceCents=%d", e.PriceCents)
	}
	if !e.IsSubscription {
		t.Error("IsSubscription=false; want true (recurrence set)")
	}
	if e.TenantID.String() != "00000000-0000-0000-0000-000000000001" {
		t.Errorf("TenantID=%v", e.TenantID)
	}
	if e.UserEmail != "buyer@example.com" {
		t.Errorf("UserEmail=%q", e.UserEmail)
	}
}

func TestParseForm_RefundFlag(t *testing.T) {
	v := url.Values{}
	v.Set("resource_name", "refund")
	v.Set("sale_id", "sale-123")
	v.Set("refunded", "true")
	v.Set("refunded_at", "2026-05-08T04:00:00Z")
	v.Set("tenant_id", "00000000-0000-0000-0000-000000000001")
	e := ParseForm(v)
	if e.Type != "refund" {
		t.Errorf("Type=%q", e.Type)
	}
	if !e.Refunded {
		t.Error("Refunded=false; want true")
	}
	if e.RefundedAt == nil {
		t.Error("RefundedAt=nil; want set")
	}
}

func TestParseForm_AcceptsURLParamPrefixedTenantID(t *testing.T) {
	// Gumroad sometimes prefixes URL params with `url_params[...]`.
	v := url.Values{}
	v.Set("sale_id", "sale-456")
	v.Set("product_permalink", "gmc-starter")
	v.Set("url_params[tenant_id]", "00000000-0000-0000-0000-000000000099")
	e := ParseForm(v)
	if e.TenantID.String() != "00000000-0000-0000-0000-000000000099" {
		t.Errorf("TenantID=%v; want resolved from url_params[tenant_id]", e.TenantID)
	}
}

// ----------------------------------------------------------------------------
// Catalog tier mapping
// ----------------------------------------------------------------------------

func TestCatalog_LookupByGumroadID(t *testing.T) {
	t.Setenv("GUMROAD_PRODUCT_STARTER", "gmc-starter")
	t.Setenv("GUMROAD_PRODUCT_GROWTH", "gmc-growth")
	t.Setenv("GUMROAD_PRODUCT_AGENCY", "gmc-agency")
	t.Setenv("GUMROAD_PRODUCT_RESCUE", "gmc-rescue")
	t.Setenv("GUMROAD_PRODUCT_DFY", "gmc-dfy")

	c := LoadCatalog()

	cases := []struct {
		id   string
		kind ProductKind
		plan string
		recurring bool
	}{
		{"gmc-starter", KindStarter, "pro",     true},
		{"gmc-growth",  KindGrowth,  "growth",  true},
		{"gmc-agency",  KindAgency,  "agency",  true},
		{"gmc-rescue",  KindRescue,  "",        false},
		{"gmc-dfy",     KindDFY,     "",        false},
		{"unknown",     KindUnknown, "",        false},
	}
	for _, tc := range cases {
		got := c.LookupByGumroadID(tc.id)
		if got.Kind != tc.kind {
			t.Errorf("%s: kind=%s want %s", tc.id, got.Kind, tc.kind)
		}
		if got.PlanTier != tc.plan {
			t.Errorf("%s: plan=%q want %q", tc.id, got.PlanTier, tc.plan)
		}
		if got.IsRecurring != tc.recurring {
			t.Errorf("%s: recurring=%v want %v", tc.id, got.IsRecurring, tc.recurring)
		}
	}
}

func TestCatalog_SubscriptionTiersOrder(t *testing.T) {
	c := LoadCatalog()
	tiers := c.SubscriptionTiers()
	if len(tiers) != 3 {
		t.Fatalf("got %d tiers; want 3", len(tiers))
	}
	if tiers[0].Kind != KindStarter || tiers[1].Kind != KindGrowth || tiers[2].Kind != KindAgency {
		t.Errorf("tier order = %+v; want starter, growth, agency", tiers)
	}
}
