// Package gmc is a thin client for Google's Content API for Shopping
// (Merchant Center). We use stdlib net/http rather than google.golang.org/api
// to keep the dependency surface tiny and the request shape obvious.
//
// All operations take a *Client whose token-supplier reads the
// AES-256-GCM-encrypted refresh token from store_gmc_connections, exchanges
// it for an access token only in memory, and never persists access tokens.
package gmc

import "time"

// AccountStatus is the slim view of accountstatuses.get we care about.
// Google returns a much larger payload; we only extract what the audit
// pipeline + UI consume.
type AccountStatus struct {
	MerchantID         string             `json:"merchantId"`
	WebsiteClaimed     bool               `json:"websiteClaimed"`
	AccountLevelIssues []AccountIssue     `json:"accountLevelIssues"`
	Products           AccountProductStat `json:"products"`
	// Status synthesised by the client from issue severities.
	Status string `json:"-"` // active|warning|suspended
}

type AccountIssue struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Country       string `json:"country,omitempty"`
	Severity      string `json:"severity"` // critical|error|suggestion
	Detail        string `json:"detail,omitempty"`
	Documentation string `json:"documentation,omitempty"`
	Destination   string `json:"destination,omitempty"`
}

type AccountProductStat struct {
	Channel        string `json:"channel,omitempty"`
	Country        string `json:"country,omitempty"`
	Active         int    `json:"active"`
	Pending        int    `json:"pending"`
	Disapproved    int    `json:"disapproved"`
	Expiring       int    `json:"expiring"`
}

// ProductStatus mirrors productstatuses.get response items.
type ProductStatus struct {
	ProductID            string                `json:"productId"`
	Title                string                `json:"title"`
	Link                 string                `json:"link"`
	GoogleExpirationDate string                `json:"googleExpirationDate,omitempty"`
	DestinationStatuses  []DestinationStatus   `json:"destinationStatuses"`
	ItemLevelIssues      []ItemLevelIssue      `json:"itemLevelIssues"`
}

type DestinationStatus struct {
	Destination     string   `json:"destination"`
	ApprovedCountries []string `json:"approvedCountries,omitempty"`
	PendingCountries  []string `json:"pendingCountries,omitempty"`
	DisapprovedCountries []string `json:"disapprovedCountries,omitempty"`
	Status string `json:"status,omitempty"`
}

// ItemLevelIssue carries Google's machine-readable code that we surface as
// `external_issue_code` on the audit's Issue rows.
type ItemLevelIssue struct {
	Code            string `json:"code"`
	Description     string `json:"description"`
	Detail          string `json:"detail,omitempty"`
	Documentation   string `json:"documentation,omitempty"`
	Resolution      string `json:"resolution,omitempty"`
	Servability     string `json:"servability,omitempty"`
	Destination     string `json:"destination,omitempty"`
	AttributeName   string `json:"attributeName,omitempty"`
	ApplicableCountries []string `json:"applicableCountries,omitempty"`
}

// DatafeedStatus from datafeedstatuses.list.
type DatafeedStatus struct {
	DatafeedID    string         `json:"datafeedId"`
	Country       string         `json:"country,omitempty"`
	Language      string         `json:"language,omitempty"`
	ProcessingStatus string      `json:"processingStatus"` // success|failure|in progress
	ItemsTotal    int            `json:"itemsTotal,omitempty"`
	ItemsValid    int            `json:"itemsValid,omitempty"`
	LastUploadDate string        `json:"lastUploadDate,omitempty"`
	Errors        []DatafeedErr  `json:"errors,omitempty"`
	Warnings      []DatafeedErr  `json:"warnings,omitempty"`
}

type DatafeedErr struct {
	Code        string `json:"code"`
	Count       int    `json:"count,omitempty"`
	Message     string `json:"message,omitempty"`
}

// Account is the entry in accounts.list for the picker UI.
type Account struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	WebsiteURL   string    `json:"websiteUrl,omitempty"`
	BusinessType string    `json:"-"` // not in v2.1 response, derived for UI
}

// Token bundles an OAuth response. Refresh token may be empty on a
// refresh-grant response (Google only returns it on the initial exchange).
type Token struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	Scope        string
}
