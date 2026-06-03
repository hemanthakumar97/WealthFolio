// mf-data-explorer: diagnostic tool to find which external sources return
// live TER / AUM / expense data for Indian mutual funds.
//
// Usage: go run ./cmd/mf-data-explorer <amfi_code>
// Example: go run ./cmd/mf-data-explorer 120828
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

const ua = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: go run ./cmd/mf-data-explorer <amfi_code>")
		os.Exit(1)
	}
	amfi := os.Args[1]
	fmt.Printf("\n=== MF Data Explorer: AMFI Code %s ===\n\n", amfi)

	client := &http.Client{Timeout: 15 * time.Second}

	probeMFAPI(client, amfi)
	schemeName, isin := probeMFAPI(client, amfi)
	probeGrowwPage(client, schemeName)
	probeGrowwSessionPage(client, schemeName)
	probeGrowwSearchAPI(client, schemeName)
	probeAMFINavAll(client, amfi)
	probeAMFITERFile(client)
	probeTickertapeMF(client, schemeName)
	if isin != "" {
		probeByISIN(client, isin)
	}
}

// ─── mfapi.in ────────────────────────────────────────────────────────────────

func probeMFAPI(client *http.Client, amfi string) (schemeName, isin string) {
	fmt.Println("[A] mfapi.in")
	resp, err := get(client, "https://api.mfapi.in/mf/"+amfi, nil)
	if err != nil {
		fmt.Printf("    ERROR: %v\n\n", err)
		return
	}
	var d struct {
		Meta struct {
			FundHouse   string `json:"fund_house"`
			Category    string `json:"scheme_category"`
			SchemeName  string `json:"scheme_name"`
			SchemeCode  int    `json:"scheme_code"`
			ISINGrowth  string `json:"isin_growth"`
		} `json:"meta"`
	}
	json.Unmarshal(resp, &d)
	fmt.Printf("    scheme_name : %s\n", d.Meta.SchemeName)
	fmt.Printf("    fund_house  : %s\n", d.Meta.FundHouse)
	fmt.Printf("    category    : %s\n", d.Meta.Category)
	fmt.Printf("    isin_growth : %s\n", d.Meta.ISINGrowth)
	fmt.Printf("    TER         : NOT AVAILABLE in mfapi.in\n\n")
	return d.Meta.SchemeName, d.Meta.ISINGrowth
}

// ─── Groww page (slug from scheme name) ──────────────────────────────────────

func probeGrowwPage(client *http.Client, schemeName string) {
	fmt.Println("[B] Groww fund page (slug from mfapi scheme_name)")
	slug := toSlug(schemeName)
	pageURL := "https://groww.in/mutual-funds/" + slug
	fmt.Printf("    slug        : %s\n", slug)
	fmt.Printf("    url         : %s\n", pageURL)

	body, status, err := getWithStatus(client, pageURL, map[string]string{
		"Accept":          "text/html,application/xhtml+xml",
		"Accept-Language": "en-US,en;q=0.9",
	})
	fmt.Printf("    http_status : %d\n", status)
	if err != nil {
		fmt.Printf("    ERROR: %v\n\n", err)
		return
	}
	parseGrowwNextData(body)
	fmt.Println()
}

// ─── Groww page (with session cookie) ────────────────────────────────────────

func probeGrowwSessionPage(client *http.Client, schemeName string) {
	fmt.Println("[C] Groww fund page (with session cookie from homepage pre-visit)")
	slug := toSlug(schemeName)

	// Step 1: get cookies from homepage
	jar := newCookieJar()
	clientWithJar := &http.Client{Timeout: 15 * time.Second, Jar: jar}
	req1, _ := http.NewRequest(http.MethodGet, "https://groww.in", nil)
	req1.Header.Set("User-Agent", ua)
	req1.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp1, err := clientWithJar.Do(req1)
	if err != nil {
		fmt.Printf("    homepage pre-visit failed: %v\n\n", err)
		return
	}
	resp1.Body.Close()
	fmt.Printf("    homepage visit : %d\n", resp1.StatusCode)

	// Step 2: fetch fund page with cookies + referer
	pageURL := "https://groww.in/mutual-funds/" + slug
	req2, _ := http.NewRequest(http.MethodGet, pageURL, nil)
	req2.Header.Set("User-Agent", ua)
	req2.Header.Set("Accept", "text/html,application/xhtml+xml")
	req2.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req2.Header.Set("Referer", "https://groww.in/")
	req2.Header.Set("sec-fetch-site", "same-origin")
	req2.Header.Set("sec-fetch-mode", "navigate")
	req2.Header.Set("sec-ch-ua", `"Chromium";v="124", "Google Chrome";v="124"`)
	resp2, err := clientWithJar.Do(req2)
	if err != nil {
		fmt.Printf("    fund page fetch failed: %v\n\n", err)
		return
	}
	defer resp2.Body.Close()
	body, _ := io.ReadAll(resp2.Body)
	fmt.Printf("    fund page status : %d\n", resp2.StatusCode)
	parseGrowwNextData(body)
	fmt.Println()
}

// ─── Groww internal search API ────────────────────────────────────────────────

func probeGrowwSearchAPI(client *http.Client, schemeName string) {
	fmt.Println("[D] Groww internal search APIs")

	words := strings.Fields(schemeName)
	query := strings.Join(words[:min(4, len(words))], " ")

	endpoints := []string{
		"https://groww.in/v2/api/search/v3/query/universal/v3/?q=" + url.QueryEscape(query) + "&entity_type=FUND&page=0&count=5",
		"https://groww.in/v2/api/search/v2/entity?entity_type=fund&query=" + url.QueryEscape(query),
		"https://groww.in/v2/api/data/fund/search/v1?q=" + url.QueryEscape(query),
	}

	for _, ep := range endpoints {
		body, status, err := getWithStatus(client, ep, map[string]string{
			"Accept":  "application/json",
			"Referer": "https://groww.in/",
		})
		fmt.Printf("    %s\n", ep)
		fmt.Printf("    status: %d", status)
		if err != nil {
			fmt.Printf("  err: %v\n", err)
			continue
		}
		snippet := string(body)
		if len(snippet) > 400 {
			snippet = snippet[:400] + "..."
		}
		// Check if it looks like JSON fund data
		if strings.Contains(snippet, "expense_ratio") || strings.Contains(snippet, "expenseRatio") {
			fmt.Printf("  ✓ CONTAINS EXPENSE RATIO\n")
		} else if strings.Contains(snippet, "search_id") || strings.Contains(snippet, "scheme") {
			fmt.Printf("  ✓ contains fund-like fields\n")
		} else if strings.HasPrefix(strings.TrimSpace(snippet), "<") {
			fmt.Printf("  ✗ returned HTML (bot-blocked)\n")
		} else {
			fmt.Printf("\n")
		}
		fmt.Printf("    response: %s\n\n", snippet)
	}
}

// ─── AMFI NAVAll ──────────────────────────────────────────────────────────────

func probeAMFINavAll(client *http.Client, amfi string) {
	fmt.Println("[E] AMFI NAVAll.txt")
	body, status, err := getWithStatus(client, "https://www.amfiindia.com/spages/NAVAll.txt", nil)
	fmt.Printf("    http_status : %d\n", status)
	if err != nil || status != 200 {
		fmt.Printf("    ERROR: %v\n\n", err)
		return
	}
	// Find line with our AMFI code
	lines := strings.Split(string(body), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, amfi+";") || strings.HasPrefix(line, amfi+"\t") {
			fmt.Printf("    line found  : %s\n", strings.TrimSpace(line))
			parts := strings.Split(line, ";")
			fmt.Printf("    fields      : %d fields\n", len(parts))
			for i, p := range parts {
				fmt.Printf("    [%d] %s\n", i, strings.TrimSpace(p))
			}
			fmt.Printf("    TER         : NOT IN NAVAll\n\n")
			return
		}
	}
	fmt.Printf("    AMFI code %s NOT found in NAVAll\n\n", amfi)
}

// ─── AMFI TER quarterly file ──────────────────────────────────────────────────

func probeAMFITERFile(client *http.Client) {
	fmt.Println("[F] AMFI TER quarterly file (portal.amfiindia.com)")
	now := time.Now()
	month := now.Month()
	year := now.Year()

	// Determine financial year and quarter
	var fyStart int
	if month >= 4 {
		fyStart = year
	} else {
		fyStart = year - 1
	}
	fyEnd := (fyStart + 1) % 100 // last 2 digits of end year

	var quarter string
	switch {
	case month >= 1 && month <= 3:
		quarter = "Q3"
	case month >= 4 && month <= 6:
		quarter = "Q4" // Apr-Jun = Q4 of previous FY? Actually Q1 of new FY
		quarter = "Q1"
	case month >= 7 && month <= 9:
		quarter = "Q2"
	default:
		quarter = "Q3"
	}

	// Try multiple filename patterns
	patterns := []string{
		fmt.Sprintf("https://portal.amfiindia.com/spages/ATERQ%dFY%02d%02d.txt", fyStart%100, fyStart%100, fyEnd),
		fmt.Sprintf("https://portal.amfiindia.com/spages/ATER%sFY%d%02d.txt", quarter, fyStart, fyEnd),
		fmt.Sprintf("https://portal.amfiindia.com/spages/ATERQ4FY%d%02d.txt", fyStart%100, fyEnd),
		fmt.Sprintf("https://portal.amfiindia.com/spages/ATER%sFY%d%02d.txt", quarter, fyStart%100, fyEnd),
	}

	for _, u := range patterns {
		_, status, err := getWithStatus(client, u, nil)
		fmt.Printf("    %s → %d", u, status)
		if err != nil {
			fmt.Printf(" (%v)", err)
		}
		if status == 200 {
			fmt.Printf(" ✓ EXISTS")
		}
		fmt.Println()
	}
	fmt.Println()
}

// ─── Tickertape MF search ────────────────────────────────────────────────────

func probeTickertapeMF(client *http.Client, schemeName string) {
	fmt.Println("[G] Tickertape search (MF check)")
	words := strings.Fields(schemeName)
	query := strings.Join(words[:min(3, len(words))], " ")
	searchURL := "https://api.tickertape.in/search?text=" + url.QueryEscape(query)

	body, status, err := getWithStatus(client, searchURL, map[string]string{"Accept": "application/json"})
	fmt.Printf("    status : %d\n", status)
	if err != nil {
		fmt.Printf("    ERROR: %v\n\n", err)
		return
	}

	var d struct {
		Data struct {
			Total        int             `json:"total"`
			Stocks       json.RawMessage `json:"stocks"`
			MutualFunds  json.RawMessage `json:"mutualFunds"`
			Brands       json.RawMessage `json:"brands"`
			Indices      json.RawMessage `json:"indices"`
		} `json:"data"`
	}
	json.Unmarshal(body, &d)
	fmt.Printf("    top-level keys : total=%d\n", d.Data.Total)
	fmt.Printf("    mutualFunds    : %s\n", nullOrSnippet(d.Data.MutualFunds, 200))
	fmt.Printf("    stocks[0]      : %s\n\n", nullOrSnippet(d.Data.Stocks, 200))
}

// ─── ISIN-based lookup ────────────────────────────────────────────────────────

func probeByISIN(client *http.Client, isin string) {
	fmt.Printf("[H] ISIN-based lookup: %s\n", isin)

	// BSE India ISIN lookup
	bseURL := fmt.Sprintf("https://api.bseindia.com/BseIndiaAPI/api/MFSchemeDetails/w?ISIN=%s", isin)
	body, status, err := getWithStatus(client, bseURL, map[string]string{
		"Accept":  "application/json",
		"Referer": "https://www.bseindia.com/",
	})
	fmt.Printf("    BSE ISIN API: %d", status)
	if err != nil {
		fmt.Printf(" err: %v", err)
	}
	snippet := string(body)
	if len(snippet) > 300 {
		snippet = snippet[:300]
	}
	if strings.Contains(snippet, "expense") || strings.Contains(snippet, "Expense") || strings.Contains(snippet, "TER") {
		fmt.Printf(" ✓ EXPENSE RATIO FOUND\n")
	}
	fmt.Printf("\n    response: %s\n\n", snippet)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func get(client *http.Client, rawURL string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", ua)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func getWithStatus(client *http.Client, rawURL string, headers map[string]string) ([]byte, int, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", ua)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return body, resp.StatusCode, nil
}

func toSlug(name string) string {
	s := strings.ToLower(name)
	re := regexp.MustCompile(`[^a-z0-9]+`)
	s = re.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

var rexpNextData = regexp.MustCompile(`<script id="__NEXT_DATA__"[^>]*>([\s\S]*?)</script>`)

func parseGrowwNextData(body []byte) {
	m := rexpNextData.FindSubmatch(body)
	if m == nil {
		fmt.Println("    __NEXT_DATA__ : NOT FOUND in response")
		return
	}
	var nd map[string]any
	if err := json.Unmarshal(m[1], &nd); err != nil {
		fmt.Printf("    __NEXT_DATA__ : parse error: %v\n", err)
		return
	}
	// Check if pageProps has real data
	props, _ := nd["props"].(map[string]any)
	pageProps, _ := props["pageProps"].(map[string]any)
	if len(pageProps) == 0 {
		fmt.Println("    __NEXT_DATA__ : FOUND but pageProps is EMPTY (bot-blocked)")
		return
	}
	fmt.Printf("    __NEXT_DATA__ : FOUND with %d pageProps keys: %v\n", len(pageProps), keys(pageProps))
	if mfData, ok := pageProps["mfServerSideData"].(map[string]any); ok {
		fmt.Printf("    mfServerSideData keys: %v\n", keys(mfData))
		if er, ok := mfData["expense_ratio"]; ok {
			fmt.Printf("    ✓ expense_ratio: %v\n", er)
		}
		if aum, ok := mfData["fund_size"]; ok {
			fmt.Printf("    fund_size: %v\n", aum)
		}
		if si, ok := mfData["search_id"]; ok {
			fmt.Printf("    search_id (slug): %v\n", si)
		}
	}
}

func keys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

func nullOrSnippet(raw json.RawMessage, n int) string {
	if raw == nil || string(raw) == "null" {
		return "null / not returned"
	}
	s := string(raw)
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// simpleCookieJar implements http.CookieJar for session cookie carry-over.
type simpleCookieJar struct {
	cookies map[string][]*http.Cookie
}

func newCookieJar() *simpleCookieJar { return &simpleCookieJar{cookies: map[string][]*http.Cookie{}} }

func (j *simpleCookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.cookies[u.Host] = append(j.cookies[u.Host], cookies...)
}

func (j *simpleCookieJar) Cookies(u *url.URL) []*http.Cookie { return j.cookies[u.Host] }
