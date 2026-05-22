package parsers

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/hemanthakumar97/wealthfolio/internal/domain"
)

// GrowwEmailParser extracts NormalizedTransactions from Groww MF allotment
// notification emails. It does not implement the Parser interface (which is
// CSV/[][]string-based); it works directly on decoded HTML email bodies.
//
// Groww allotment email format (subject: "SIP: Units allocated"):
//
//	SCHEME NAME          → Motilal Oswal Midcap Fund Direct Growth
//	SIP AMOUNT           → ₹8,749.56
//	UNITS ALLOCATED      → 83.0220
//	NAV                  → 105.3881
//	ORDER ID             → GME260503000000HTK7UGU1R
//
// Labels and values are in sequential single-cell table rows, not side-by-side.
type GrowwEmailParser struct{}

// ParseEmail parses the HTML body of a Groww allotment email.
// receivedAt is used as the transaction date when no date appears in the body.
func (p *GrowwEmailParser) ParseEmail(htmlBody, messageID, subject string, receivedAt time.Time) ([]NormalizedTransaction, []error) {
	records := extractEmailRecords(htmlBody)

	// Fallback to regex if DOM yielded nothing.
	if len(records) == 0 {
		if r := regexFallback(htmlBody); r != nil {
			records = []emailRecord{*r}
		}
	}

	var txs []NormalizedTransaction
	var errs []error
	for i, rec := range records {
		tx, err := rec.toNormalized(messageID, subject, receivedAt)
		if err != nil {
			errs = append(errs, fmt.Errorf("block %d: %w", i+1, err))
			continue
		}
		txs = append(txs, tx)
	}
	return txs, errs
}

// emailRecord holds raw string fields extracted from one allotment block.
type emailRecord struct {
	FundName string
	Units    string
	NAV      string
	Amount   string
	Date     string
	OrderID  string
}

func (r emailRecord) toNormalized(messageID, subject string, fallbackDate time.Time) (NormalizedTransaction, error) {
	if r.FundName == "" {
		return NormalizedTransaction{}, fmt.Errorf("could not find fund name in email")
	}

	units, err := parseDecimal(r.Units)
	if err != nil || units.IsZero() {
		return NormalizedTransaction{}, fmt.Errorf("invalid units %q", r.Units)
	}

	nav, err := parseDecimal(r.NAV)
	if err != nil {
		nav, _ = parseDecimal("0")
	}

	amount, err := parseDecimal(r.Amount)
	if err != nil || amount.IsZero() {
		if !units.IsZero() && !nav.IsZero() {
			amount = units.Mul(nav).Round(2)
		}
	}

	// Use body date if present, otherwise fall back to email received timestamp.
	var txDate time.Time
	if r.Date != "" {
		if d, err := parseDate(r.Date); err == nil {
			txDate = d
		}
	}
	if txDate.IsZero() {
		if fallbackDate.IsZero() {
			return NormalizedTransaction{}, fmt.Errorf("no transaction date available")
		}
		txDate = fallbackDate.UTC().Truncate(24 * time.Hour)
	}

	price := nav
	if price.IsZero() && !units.IsZero() && !amount.IsZero() {
		price = amount.Div(units).Round(4)
	}

	return NormalizedTransaction{
		InstrumentName:  cleanFundName(r.FundName),
		AssetType:       domain.AssetTypeMF,
		TransactionDate: txDate,
		TransactionType: domain.TxBuy,
		Quantity:        units,
		Price:           price,
		Amount:          amount,
		Platform:        domain.PlatformGroww,
		OrderID:         strings.TrimSpace(r.OrderID),
		RowNumber:       1,
		OriginalRow: map[string]string{
			"gmail_message_id": messageID,
			"subject":          subject,
			"fund_name":        r.FundName,
			"units":            r.Units,
			"nav":              r.NAV,
			"amount":           r.Amount,
			"date":             r.Date,
			"order_id":         r.OrderID,
		},
	}, nil
}

// extractEmailRecords walks the HTML DOM and collects label→value pairs from
// sequential single-cell table rows (Groww's actual email format).
func extractEmailRecords(htmlBody string) []emailRecord {
	doc, err := html.Parse(strings.NewReader(htmlBody))
	if err != nil {
		return nil
	}

	// Collect all <td> text values in document order.
	var cells []string
	var walkCells func(*html.Node)
	walkCells = func(n *html.Node) {
		if n.Type == html.ElementNode && strings.ToLower(n.Data) == "td" {
			text := strings.TrimSpace(nodeText(n))
			if text != "" {
				cells = append(cells, text)
			}
			return // don't recurse into td children for cell text
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walkCells(c)
		}
	}
	walkCells(doc)

	// Slide through consecutive pairs: cells[i] = label, cells[i+1] = value.
	pairs := map[string]string{}
	for i := 0; i+1 < len(cells); i++ {
		label := strings.ToLower(strings.TrimSpace(cells[i]))
		value := strings.TrimSpace(cells[i+1])
		if isKnownLabel(label) && value != "" && !isKnownLabel(strings.ToLower(value)) {
			pairs[label] = value
		}
	}

	rec := pairsToRecord(pairs)

	// Fund name may appear in a prominent heading element rather than a table cell.
	if rec.FundName == "" {
		var walkHeadings func(*html.Node)
		walkHeadings = func(n *html.Node) {
			if n.Type == html.ElementNode {
				tag := strings.ToLower(n.Data)
				if tag == "h1" || tag == "h2" || tag == "h3" || tag == "h4" || tag == "strong" || tag == "b" {
					text := strings.TrimSpace(nodeText(n))
					if looksLikeFundName(text) && rec.FundName == "" {
						rec.FundName = text
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walkHeadings(c)
			}
		}
		walkHeadings(doc)
	}

	if rec.Units == "" {
		return nil
	}
	return []emailRecord{rec}
}

// isKnownLabel returns true if a string looks like one of Groww's field labels.
func isKnownLabel(s string) bool {
	knownLabels := []string{
		"scheme name", "fund name", "sip amount", "investment amount", "amount",
		"units allocated", "units allotted", "units alloted", "units",
		"nav", "applicable nav",
		"order id", "order no", "reference no",
		"allotment date", "transaction date", "date",
	}
	for _, kl := range knownLabels {
		if strings.Contains(s, kl) {
			return true
		}
	}
	return false
}

// nodeText returns all text content within a node (recursively).
func nodeText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		sb.WriteString(nodeText(c))
	}
	return sb.String()
}

// pairsToRecord maps label→value pairs to an emailRecord using fuzzy key matching.
func pairsToRecord(pairs map[string]string) emailRecord {
	var r emailRecord
	for label, value := range pairs {
		l := label
		switch {
		case containsAny(l, "units allocated", "units allotted", "units alloted"):
			r.Units = cleanNumber(value)
		case l == "units" && r.Units == "":
			r.Units = cleanNumber(value)
		case containsAny(l, "applicable nav", "nav"):
			r.NAV = cleanNumber(value)
		case containsAny(l, "sip amount", "investment amount", "amount invested", "amount"):
			r.Amount = cleanNumber(value)
		case containsAny(l, "allotment date", "transaction date", "date of allotment"):
			r.Date = value
		case strings.HasSuffix(l, "date") && r.Date == "":
			r.Date = value
		case containsAny(l, "order id", "order no", "reference no"):
			r.OrderID = value
		case containsAny(l, "scheme name", "fund name") && r.FundName == "":
			r.FundName = value
		}
	}
	return r
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// looksLikeFundName returns true if the text resembles a mutual fund scheme name.
func looksLikeFundName(s string) bool {
	if len(s) < 8 || len(s) > 200 {
		return false
	}
	upper := strings.ToUpper(s)
	keywords := []string{"FUND", "GROWTH", "DIRECT", "PLAN", "FLEXI", "EQUITY", "DEBT", "HYBRID", "INDEX", "SMALL", "MID", "LARGE", "ELSS", "LIQUID"}
	for _, kw := range keywords {
		if strings.Contains(upper, kw) {
			return true
		}
	}
	return false
}

func cleanNumber(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "₹")
	s = strings.TrimPrefix(s, "Rs.")
	s = strings.TrimPrefix(s, "Rs")
	s = strings.ReplaceAll(s, ",", "")
	return strings.TrimSpace(s)
}

func cleanFundName(s string) string { return strings.TrimSpace(s) }

// --- regex fallback ----------------------------------------------------------

var (
	reFundName = regexp.MustCompile(`(?i)(?:scheme\s*name|fund\s*name)[:\s]*\n?\s*([A-Z][^\n<]{8,120})`)
	reUnits    = regexp.MustCompile(`(?i)units\s*(?:allocated|allotted|alloted)?[:\s]+([\d.]+)`)
	reNAV      = regexp.MustCompile(`(?i)(?:applicable\s+)?nav[:\s]+(?:₹|rs\.?\s*)?([\d.,]+)`)
	reAmount   = regexp.MustCompile(`(?i)(?:sip\s+)?amount[:\s]+(?:₹|rs\.?\s*)?([\d.,]+)`)
	reDate     = regexp.MustCompile(`(?i)(?:allotment|transaction)\s+date[:\s]+(\d{1,2}[-/]\d{1,2}[-/]\d{2,4})`)
	reOrderID  = regexp.MustCompile(`(?i)order\s*(?:id|no|number)?[:\s]+([A-Z0-9_-]{8,40})`)
)

func regexFallback(body string) *emailRecord {
	stripped := stripTags(body)

	match := func(re *regexp.Regexp) string {
		m := re.FindStringSubmatch(stripped)
		if len(m) > 1 {
			return strings.TrimSpace(m[1])
		}
		return ""
	}

	r := &emailRecord{
		FundName: match(reFundName),
		Units:    cleanNumber(match(reUnits)),
		NAV:      cleanNumber(match(reNAV)),
		Amount:   cleanNumber(match(reAmount)),
		Date:     match(reDate),
		OrderID:  match(reOrderID),
	}
	if r.Units == "" {
		return nil
	}
	return r
}

func stripTags(s string) string {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return s
	}
	return nodeText(doc)
}
