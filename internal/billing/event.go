package billing

import (
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// Event is the slim, parsed view of a Gumroad webhook payload.
//
// Gumroad ships its webhooks as application/x-www-form-urlencoded, with
// custom fields (the ones we configured on the product page) flattened
// into the form. We parse the form once and project the fields the
// dispatcher actually consumes.
type Event struct {
	Type        string // "sale" | "subscription_updated" | "subscription_cancelled" | "subscription_ended" | "refund"
	SaleID      string
	ProductID   string
	LicenseKey  string
	Email       string
	PriceCents  int
	Currency    string
	SubscriptionID string

	// Custom fields configured on the Gumroad product page. We require
	// tenant_id (hidden, prefilled from URL) on every checkout; user_email
	// is informational.
	TenantID  uuid.UUID
	UserEmail string

	// Sub fields for subscription_* events.
	IsSubscription bool
	Recurrence     string // "monthly" | "yearly" | ""
	EndsAt         *time.Time

	// Refund fields.
	Refunded   bool
	RefundedAt *time.Time

	// PurchasedAt is best-effort — Gumroad sometimes sends `sale_timestamp`,
	// sometimes only `created_at`. Defaults to now() if neither is present.
	PurchasedAt time.Time
}

// ParseForm decodes a Gumroad webhook form into an Event. The "type" of
// event is inferred from the explicit `type` field if Gumroad sends one,
// otherwise from which fields are populated.
func ParseForm(form url.Values) Event {
	e := Event{
		Type:           form.Get("resource_name"),
		SaleID:         form.Get("sale_id"),
		ProductID:      firstNonEmpty(form.Get("product_permalink"), form.Get("product_id"), form.Get("permalink")),
		LicenseKey:     form.Get("license_key"),
		Email:          form.Get("email"),
		Currency:       form.Get("currency"),
		SubscriptionID: form.Get("subscription_id"),
		Recurrence:     form.Get("recurrence"),
		PurchasedAt:    parseTime(form.Get("sale_timestamp"), form.Get("created_at")),
	}
	// Gumroad only sets `resource_name` on subscription events; sale events
	// come without it, so detect "sale" by absence + sale_id presence.
	if e.Type == "" && e.SaleID != "" {
		e.Type = "sale"
	}
	if v := form.Get("price_cents"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			e.PriceCents = n
		}
	}
	if v := form.Get("recurrence"); v != "" {
		e.IsSubscription = true
	}
	// Custom fields. Gumroad flattens these as `url_params[tenant_id]` OR
	// `tenant_id` depending on integration; we accept either.
	tenantID := firstNonEmpty(form.Get("tenant_id"), form.Get("url_params[tenant_id]"))
	if id, err := uuid.Parse(tenantID); err == nil {
		e.TenantID = id
	}
	e.UserEmail = firstNonEmpty(form.Get("user_email"), form.Get("url_params[user_email]"), form.Get("email"))

	// Refund.
	if v := form.Get("refunded"); v == "true" || v == "1" {
		e.Refunded = true
		t := parseTime(form.Get("refunded_at"))
		e.RefundedAt = &t
	}
	// Subscription-period end.
	if v := form.Get("ends_at"); v != "" {
		t := parseTime(v)
		e.EndsAt = &t
	}
	return e
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// parseTime accepts the first parseable RFC3339 / unix-seconds value from
// the supplied list; falls back to now() so callers can keep working with
// an Event even when Gumroad omitted the timestamp.
func parseTime(values ...string) time.Time {
	for _, v := range values {
		if v == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t
		}
		if t, err := time.Parse("2006-01-02 15:04:05 UTC", v); err == nil {
			return t
		}
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return time.Unix(n, 0)
		}
	}
	return time.Now().UTC()
}
