// Package crawler fetches a Shopify storefront and produces an audit.CheckContext.
//
// Constraints (per /internal/crawler design):
//   - Honours robots.txt for the ShopifyGMCBot/1.0 user agent.
//   - Per-store rate limit: 2 rps; per-store concurrent: 5.
//   - Global concurrent: 20.
//   - 15s timeout; 3 redirects max.
//   - Page budget: homepage + 25 products + 5 collections + 6 policy pages.
//   - Validates the homepage is a Shopify store (cdn.shopify.com or
//     Shopify.theme reference) before deeper crawl; otherwise returns
//     ErrNotShopify and nothing further is fetched.
package crawler

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/temoto/robotstxt"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
	"golang.org/x/time/rate"

	"github.com/example/gmcauditor/internal/audit"
)

const (
	UserAgent       = "ShopifyGMCBot/1.0 (+https://shopifygmc.com/bot)"
	defaultTimeout  = 15 * time.Second
	maxRedirects    = 3
	perStoreRPS     = 2
	perStoreConcurr = 5
	globalConcurr   = 20

	BudgetProducts    = 25
	BudgetCollections = 5
	BudgetPolicies    = 6
)

var ErrNotShopify = errors.New("crawler: target is not a Shopify storefront")

// Shared global semaphore — one per process, capping cross-store concurrency.
var globalSem = semaphore.NewWeighted(globalConcurr)

// Crawler fetches a single store. Construct one per audit; it carries
// per-store state (rate limiter, concurrency).
type Crawler struct {
	storeURL    *url.URL
	httpClient  *http.Client
	limiter     *rate.Limiter
	storeSem    *semaphore.Weighted
	robots      *robotstxt.Group
	robotsRaw   string
	sitemapHits []string
}

func New(storeURL string) (*Crawler, error) {
	u, err := url.Parse(storeURL)
	if err != nil {
		return nil, fmt.Errorf("crawler: parse store URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("crawler: store URL needs scheme and host: %q", storeURL)
	}
	c := &Crawler{
		storeURL: u,
		limiter:  rate.NewLimiter(rate.Limit(perStoreRPS), perStoreRPS),
		storeSem: semaphore.NewWeighted(perStoreConcurr),
		httpClient: &http.Client{
			Timeout: defaultTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= maxRedirects {
					return http.ErrUseLastResponse
				}
				return nil
			},
		},
	}
	return c, nil
}

// Crawl runs the full pipeline and returns a populated CheckContext.
func (c *Crawler) Crawl(ctx context.Context) (audit.CheckContext, error) {
	cx := audit.CheckContext{StoreURL: c.storeURL.String(), PolicyPages: map[string]*audit.Page{}}

	if err := c.loadRobots(ctx); err != nil {
		return cx, err
	}
	cx.RobotsTxt = c.robotsRaw

	home, err := c.fetchPage(ctx, c.storeURL.String())
	if err != nil {
		return cx, fmt.Errorf("crawler: fetch homepage: %w", err)
	}
	cx.Homepage = home

	if !looksLikeShopify(home) {
		return cx, ErrNotShopify
	}

	// Discover URLs from the homepage links.
	productURLs, collectionURLs, policyURLs := c.discover(home)

	// Sitemap fetch (best-effort).
	cx.SitemapURLs = c.fetchSitemapURLs(ctx)

	// Fan out fetches in parallel under per-store + global semaphores.
	g, gctx := errgroup.WithContext(ctx)

	// Products
	productPages := make([]*audit.Page, 0, len(productURLs))
	var productMu sync.Mutex
	for i, u := range productURLs {
		if i >= BudgetProducts {
			break
		}
		u := u
		g.Go(func() error {
			p, err := c.fetchPage(gctx, u)
			if err != nil {
				return nil // soft-fail individual fetches
			}
			productMu.Lock()
			productPages = append(productPages, p)
			productMu.Unlock()
			return nil
		})
	}

	// Collections
	collectionPages := make([]*audit.Page, 0, len(collectionURLs))
	var collectionMu sync.Mutex
	for i, u := range collectionURLs {
		if i >= BudgetCollections {
			break
		}
		u := u
		g.Go(func() error {
			p, err := c.fetchPage(gctx, u)
			if err != nil {
				return nil
			}
			collectionMu.Lock()
			collectionPages = append(collectionPages, p)
			collectionMu.Unlock()
			return nil
		})
	}

	// Policy pages
	policyPages := map[string]*audit.Page{}
	var policyMu sync.Mutex
	for i, u := range policyURLs {
		if i >= BudgetPolicies {
			break
		}
		u := u
		slug := policySlug(u)
		g.Go(func() error {
			p, err := c.fetchPage(gctx, u)
			if err != nil {
				return nil
			}
			policyMu.Lock()
			policyPages[slug] = p
			policyMu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return cx, err
	}

	cx.ProductPages = productPages
	cx.CollectionPages = collectionPages
	cx.PolicyPages = policyPages
	return cx, nil
}

// ----------------------------------------------------------------------------
// Internals
// ----------------------------------------------------------------------------

func (c *Crawler) loadRobots(ctx context.Context) error {
	robotsURL := c.storeURL.Scheme + "://" + c.storeURL.Host + "/robots.txt"
	body, err := c.fetchRaw(ctx, robotsURL)
	if err != nil {
		// Missing robots.txt isn't fatal — assume allow-all but record empty content.
		c.robotsRaw = ""
		c.robots = nil
		return nil
	}
	c.robotsRaw = string(body)
	parsed, err := robotstxt.FromBytes(body)
	if err != nil {
		c.robots = nil
		return nil
	}
	c.robots = parsed.FindGroup(UserAgent)
	if c.robots == nil {
		c.robots = parsed.FindGroup("*")
	}
	c.sitemapHits = parsed.Sitemaps
	return nil
}

func (c *Crawler) allowed(u string) bool {
	if c.robots == nil {
		return true
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return true
	}
	return c.robots.Test(parsed.Path)
}

func (c *Crawler) fetchPage(ctx context.Context, u string) (*audit.Page, error) {
	if !c.allowed(u) {
		return nil, fmt.Errorf("crawler: blocked by robots.txt: %s", u)
	}
	body, hdr, status, err := c.fetchWithHeaders(ctx, u)
	if err != nil {
		return nil, err
	}
	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	return &audit.Page{
		URL:        u,
		StatusCode: status,
		HTML:       string(body),
		Doc:        doc,
		Headers:    hdr,
		FetchedAt:  time.Now(),
	}, nil
}

func (c *Crawler) fetchRaw(ctx context.Context, u string) ([]byte, error) {
	body, _, _, err := c.fetchWithHeaders(ctx, u)
	return body, err
}

func (c *Crawler) fetchWithHeaders(ctx context.Context, u string) ([]byte, http.Header, int, error) {
	if err := globalSem.Acquire(ctx, 1); err != nil {
		return nil, nil, 0, err
	}
	defer globalSem.Release(1)

	if err := c.storeSem.Acquire(ctx, 1); err != nil {
		return nil, nil, 0, err
	}
	defer c.storeSem.Release(1)

	if err := c.limiter.Wait(ctx); err != nil {
		return nil, nil, 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, nil, 0, err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, resp.Header, resp.StatusCode, fmt.Errorf("crawler: %s returned %d", u, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, resp.Header, resp.StatusCode, err
	}
	return body, resp.Header, resp.StatusCode, nil
}

// looksLikeShopify checks the homepage HTML for tell-tale signs that the
// site is built on Shopify. We require either a cdn.shopify.com asset or
// the Shopify.theme JS reference.
func looksLikeShopify(home *audit.Page) bool {
	if home == nil {
		return false
	}
	hay := strings.ToLower(home.HTML)
	return strings.Contains(hay, "cdn.shopify.com") || strings.Contains(hay, "shopify.theme")
}

// discover scans homepage links for product / collection / policy URLs and
// rewrites them as absolute URLs on the store host.
func (c *Crawler) discover(home *audit.Page) (products, collections, policies []string) {
	if home == nil || home.Doc == nil {
		return
	}
	seen := map[string]bool{}
	push := func(slice *[]string, abs string) {
		if seen[abs] {
			return
		}
		seen[abs] = true
		*slice = append(*slice, abs)
	}
	home.Doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		abs := c.absolute(href)
		if abs == "" {
			return
		}
		path := strings.ToLower(parsePath(abs))
		switch {
		case strings.HasPrefix(path, "/products/"):
			push(&products, abs)
		case strings.HasPrefix(path, "/collections/"):
			push(&collections, abs)
		case strings.HasPrefix(path, "/policies/") || strings.HasPrefix(path, "/pages/about") || strings.HasPrefix(path, "/pages/contact"):
			push(&policies, abs)
		}
	})
	return
}

func (c *Crawler) absolute(href string) string {
	if href == "" {
		return ""
	}
	parsed, err := url.Parse(href)
	if err != nil {
		return ""
	}
	abs := c.storeURL.ResolveReference(parsed)
	if abs.Host != c.storeURL.Host {
		return "" // off-domain links are ignored
	}
	return abs.String()
}

func parsePath(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return p.Path
}

func policySlug(u string) string {
	p, _ := url.Parse(u)
	if p == nil {
		return u
	}
	parts := strings.Split(strings.Trim(p.Path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "policies" {
		return parts[1]
	}
	if len(parts) >= 2 && parts[0] == "pages" {
		return parts[1]
	}
	return p.Path
}

// ----------------------------------------------------------------------------
// Sitemap discovery
// ----------------------------------------------------------------------------

type sitemapIndex struct {
	XMLName xml.Name `xml:"sitemapindex"`
	Entries []struct {
		Loc string `xml:"loc"`
	} `xml:"sitemap"`
}

type urlset struct {
	XMLName xml.Name `xml:"urlset"`
	URLs    []struct {
		Loc string `xml:"loc"`
	} `xml:"url"`
}

func (c *Crawler) fetchSitemapURLs(ctx context.Context) []string {
	candidates := append([]string{}, c.sitemapHits...)
	if len(candidates) == 0 {
		candidates = append(candidates, c.storeURL.Scheme+"://"+c.storeURL.Host+"/sitemap.xml")
	}
	var urls []string
	for _, sm := range candidates {
		urls = append(urls, c.crawlSitemap(ctx, sm, 0)...)
	}
	return urls
}

func (c *Crawler) crawlSitemap(ctx context.Context, smURL string, depth int) []string {
	if depth > 3 {
		return nil
	}
	body, err := c.fetchRaw(ctx, smURL)
	if err != nil {
		return nil
	}
	// Try sitemap index first.
	var idx sitemapIndex
	if err := xml.Unmarshal(body, &idx); err == nil && len(idx.Entries) > 0 {
		var out []string
		for _, e := range idx.Entries {
			out = append(out, c.crawlSitemap(ctx, e.Loc, depth+1)...)
		}
		return out
	}
	// Fall back to urlset.
	var us urlset
	if err := xml.Unmarshal(body, &us); err == nil {
		out := make([]string, 0, len(us.URLs))
		for _, u := range us.URLs {
			if strings.TrimSpace(u.Loc) != "" {
				out = append(out, u.Loc)
			}
		}
		return out
	}
	return nil
}
