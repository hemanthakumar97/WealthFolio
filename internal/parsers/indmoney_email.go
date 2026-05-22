package parsers

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/hemanthakumar97/wealthfolio/internal/domain"
)

// IndMoneyEmailParser extracts NormalizedTransactions from IndMoney US stock
// SIP confirmation emails.
//
// Subject: "Your US Stocks a/c | SIP instalment of ₹3500 in TQQQ is successful"
// From:    transactions@transactions.indmoney.com
//
// Email body fields:
//
//	Stock name          → TQQQ
//	SIP amount          → ₹3500
//	Quantity            → 0.532163
//	SIP Instalment date → 2026-05-05
//	SIP order id        → IND-2-4412837-SIP
type IndMoneyEmailParser struct{}

// ParseEmail parses the HTML body of an IndMoney US stock SIP email.
func (p *IndMoneyEmailParser) ParseEmail(htmlBody, messageID, subject string, receivedAt time.Time) ([]NormalizedTransaction, []error) {
	// Strip HTML to plain text and use regex — more reliable than DOM walking
	// since IndMoney emails use div/span layouts, not tables.
	plain := htmlToPlain(htmlBody)
	rec := regexExtractIndMoney(plain)

	// Subject fallback for symbol and amount.
	if rec.Symbol == "" {
		rec.Symbol = symbolFromSubject(subject)
	}
	if rec.Amount == "" {
		rec.Amount = amountFromSubject(subject)
	}

	tx, err := rec.toIndMoneyNormalized(messageID, subject, receivedAt)
	if err != nil {
		return nil, []error{err}
	}
	return []NormalizedTransaction{tx}, nil
}

type indMoneyRecord struct {
	Symbol  string
	Amount  string
	Qty     string
	Date    string
	OrderID string
}

// --- regex extraction --------------------------------------------------------

var (
	reIMStock  = regexp.MustCompile(`(?i)stock\s*name[\s:\n]+([A-Z]{1,10})`)
	reIMAmount = regexp.MustCompile(`(?i)sip\s*amount[\s:\n]+[₹Rs\. ]*([0-9,]+(?:\.[0-9]+)?)`)
	reIMQty    = regexp.MustCompile(`(?i)quantity[\s:\n]+([0-9]+\.[0-9]+)`)
	reIMDate   = regexp.MustCompile(`(?i)sip\s*instalment\s*date[\s:\n]+([0-9]{4}-[0-9]{2}-[0-9]{2})`)
	reIMOrder  = regexp.MustCompile(`(?i)sip\s*order\s*id[\s:\n]+([A-Z0-9_\-]+)`)
)

func regexExtractIndMoney(plain string) indMoneyRecord {
	match := func(re *regexp.Regexp) string {
		m := re.FindStringSubmatch(plain)
		if len(m) > 1 {
			return strings.TrimSpace(m[1])
		}
		return ""
	}
	return indMoneyRecord{
		Symbol:  match(reIMStock),
		Amount:  strings.ReplaceAll(match(reIMAmount), ",", ""),
		Qty:     match(reIMQty),
		Date:    match(reIMDate),
		OrderID: match(reIMOrder),
	}
}

// htmlToPlain strips all HTML tags and returns normalised plain text,
// preserving newlines between block-level elements so regex patterns work.
func htmlToPlain(src string) string {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return src
	}
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			t := strings.TrimSpace(n.Data)
			if t != "" {
				sb.WriteString(t)
				sb.WriteByte('\n')
			}
			return
		}
		if n.Type == html.ElementNode {
			tag := strings.ToLower(n.Data)
			// Insert blank line before block elements so adjacent labels/values
			// are on separate lines for the regex to match cleanly.
			switch tag {
			case "div", "p", "tr", "li", "h1", "h2", "h3", "h4", "br", "td":
				sb.WriteByte('\n')
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	// Collapse multiple blank lines.
	result := regexp.MustCompile(`\n{3,}`).ReplaceAllString(sb.String(), "\n\n")
	return strings.TrimSpace(result)
}

// --- NormalizedTransaction builder ------------------------------------------

func (r indMoneyRecord) toIndMoneyNormalized(messageID, subject string, fallbackDate time.Time) (NormalizedTransaction, error) {
	if r.Symbol == "" {
		return NormalizedTransaction{}, fmt.Errorf("could not find stock symbol in email")
	}
	qty, err := parseDecimal(r.Qty)
	if err != nil || qty.IsZero() {
		return NormalizedTransaction{}, fmt.Errorf("invalid quantity %q", r.Qty)
	}
	amount, err := parseDecimal(r.Amount)
	if err != nil || amount.IsZero() {
		return NormalizedTransaction{}, fmt.Errorf("invalid amount %q", r.Amount)
	}

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

	price := amount.Div(qty).Round(4)

	return NormalizedTransaction{
		InstrumentName:  strings.TrimSpace(r.Symbol),
		AssetType:       domain.AssetTypeUSFund,
		Currency:        domain.CurrencyUSD,
		TransactionDate: txDate,
		TransactionType: domain.TxBuy,
		Quantity:        qty,
		Price:           price,
		Amount:          amount,
		Platform:        domain.PlatformINDMoney,
		OrderID:         strings.TrimSpace(r.OrderID),
		RowNumber:       1,
		OriginalRow: map[string]string{
			"gmail_message_id": messageID,
			"subject":          subject,
			"symbol":           r.Symbol,
			"amount":           r.Amount,
			"quantity":         r.Qty,
			"date":             r.Date,
			"order_id":         r.OrderID,
		},
	}, nil
}

// --- Subject-line fallbacks --------------------------------------------------

var reSubjectSymbol = regexp.MustCompile(`\bin\s+([A-Z]{1,10})\b`)
var reSubjectAmount = regexp.MustCompile(`[₹Rs\.]+\s*([\d,]+(?:\.\d+)?)`)

func symbolFromSubject(subject string) string {
	m := reSubjectSymbol.FindStringSubmatch(subject)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

func amountFromSubject(subject string) string {
	m := reSubjectAmount.FindStringSubmatch(subject)
	if len(m) > 1 {
		return strings.ReplaceAll(m[1], ",", "")
	}
	return ""
}
