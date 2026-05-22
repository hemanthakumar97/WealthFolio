package parsers

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/hemanthakumar97/wealthfolio/internal/domain"
)

// ZerodhaContractNoteParser parses plain text extracted from a Zerodha equity
// contract note PDF.
//
// Zerodha PDFs list trades in an "Annexure A" section with each trade
// occupying exactly 11 consecutive lines:
//
//	Line 0: Order No          (16-20 digit number)
//	Line 1: Order Time        (HH:MM:SS)
//	Line 2: Trade No          (7-12 digit number)
//	Line 3: Trade Time        (HH:MM:SS)
//	Line 4: Security Desc     (e.g. JUNIORBEES-EQ/INF732E01045)
//	Line 5: B or S
//	Line 6: Exchange          (NSE / BSE)
//	Line 7: Quantity
//	Line 8: Brokerage         (e.g. 0.0000)
//	Line 9: Net Rate per unit (e.g. 668.70)
//	Line 10: Net Total        (e.g. (3343.50) — parens = payable)
type ZerodhaContractNoteParser struct{}

var (
	reOrderNo  = regexp.MustCompile(`^\d{16,20}$`)
	reTime     = regexp.MustCompile(`^\d{2}:\d{2}:\d{2}$`)
	reSecurity = regexp.MustCompile(`^(.+)-[A-Z]{2,3}/([A-Z]{2}[A-Z0-9]{10})$`)
	reBuySell  = regexp.MustCompile(`^[BS]$`)
	reExchange = regexp.MustCompile(`^(NSE|BSE|MCX|NCDEX)$`)
	reQtyInt   = regexp.MustCompile(`^\d+$`)
	reRate     = regexp.MustCompile(`^[\d,]+\.\d{2,4}$`)
)

// ParseContractNote parses extracted contract note text.
// tradeDate is taken from the email subject (e.g. "...for MML156 - April 6, 2026").
func (p *ZerodhaContractNoteParser) ParseContractNote(text, messageID, subject string, tradeDate time.Time) ([]NormalizedTransaction, []error) {
	rows := extractAnnexureTrades(text)
	if len(rows) == 0 {
		preview := strings.TrimSpace(text)
		if len(preview) > 800 {
			preview = preview[:800] + "…"
		}
		return nil, []error{fmt.Errorf("no trade rows found in Annexure A. PDF text preview:\n---\n%s\n---", preview)}
	}

	var txs []NormalizedTransaction
	var errs []error
	for i, row := range rows {
		tx, err := row.toNormalized(tradeDate, messageID, subject)
		if err != nil {
			errs = append(errs, fmt.Errorf("row %d (%s): %w", i+1, row.Symbol, err))
			continue
		}
		txs = append(txs, tx)
	}
	return txs, errs
}

// ParseSubjectDate extracts the trade date from a Zerodha contract note subject.
// e.g. "Combined Equity Contract Note for MML156 - April 6, 2026"
func ParseSubjectDate(subject string) (time.Time, error) {
	// "- Month DD, YYYY" at end of subject
	re := regexp.MustCompile(`[–\-]\s*([A-Za-z]+ \d{1,2},?\s*\d{4})\s*$`)
	if m := re.FindStringSubmatch(subject); len(m) > 1 {
		dateStr := strings.TrimSpace(strings.ReplaceAll(m[1], ",", ""))
		for _, layout := range []string{"January 2 2006", "Jan 2 2006", "January 02 2006", "Jan 02 2006"} {
			if t, err := time.Parse(layout, dateStr); err == nil {
				return t, nil
			}
		}
	}
	// Fallback: DD-MM-YYYY or DD/MM/YYYY anywhere in subject
	re2 := regexp.MustCompile(`(\d{2}[-/]\d{2}[-/]\d{4})`)
	if m := re2.FindStringSubmatch(subject); len(m) > 1 {
		return parseDate(m[1])
	}
	return time.Time{}, fmt.Errorf("could not parse date from subject: %q", subject)
}

// tradeRow holds raw fields for one contract note trade.
type tradeRow struct {
	OrderNo  string
	TradeNo  string
	Time     string
	Symbol   string
	ISIN     string
	BuySell  string // "B" or "S"
	Exchange string
	Qty      string
	NetRate  string
	NetTotal string
}

func (r tradeRow) toNormalized(tradeDate time.Time, messageID, subject string) (NormalizedTransaction, error) {
	qty, err := parseDecimal(r.Qty)
	if err != nil || qty.IsZero() {
		return NormalizedTransaction{}, fmt.Errorf("invalid qty %q", r.Qty)
	}

	price, err := parseDecimal(r.NetRate)
	if err != nil || price.IsZero() {
		return NormalizedTransaction{}, fmt.Errorf("invalid net rate %q", r.NetRate)
	}

	// NetTotal may be in parentheses: "(3343.50)" → 3343.50
	totalStr := strings.Trim(r.NetTotal, "()")
	totalStr = strings.ReplaceAll(totalStr, ",", "")
	amount, _ := parseDecimal(totalStr)
	if amount.IsZero() {
		amount = price.Mul(qty).Round(2)
	}

	txType := domain.TxBuy
	if r.BuySell == "S" {
		txType = domain.TxSell
	}

	assetType := inferAssetType(r.Symbol, r.ISIN)

	return NormalizedTransaction{
		InstrumentName:  r.Symbol,
		ISIN:            r.ISIN,
		AssetType:       assetType,
		TransactionDate: tradeDate,
		TransactionType: txType,
		Quantity:        qty,
		Price:           price,
		Amount:          amount,
		Platform:        domain.PlatformZerodha,
		OrderID:         r.OrderNo,
		TradeID:         r.TradeNo,
		RowNumber:       1,
		OriginalRow: map[string]string{
			"gmail_message_id": messageID,
			"subject":          subject,
			"order_no":         r.OrderNo,
			"trade_no":         r.TradeNo,
			"trade_time":       r.Time,
			"symbol":           r.Symbol,
			"isin":             r.ISIN,
			"exchange":         r.Exchange,
			"buy_sell":         r.BuySell,
			"qty":              r.Qty,
			"net_rate":         r.NetRate,
			"net_total":        r.NetTotal,
		},
	}, nil
}

// extractAnnexureTrades finds the "Annexure A" section and parses trade blocks.
func extractAnnexureTrades(text string) []tradeRow {
	lines := cleanLines(text)

	// Find Annexure A section.
	annexure := -1
	for i, l := range lines {
		if strings.Contains(l, "Annexure A") || strings.Contains(l, "Annexure") {
			annexure = i
			break
		}
	}
	if annexure < 0 {
		// No Annexure A header found — try whole document.
		annexure = 0
	}

	var rows []tradeRow
	i := annexure
	for i < len(lines) {
		line := lines[i]
		if !reOrderNo.MatchString(line) {
			i++
			continue
		}

		// Need at least 11 more lines for a complete trade block.
		if i+10 >= len(lines) {
			break
		}

		orderNo := line
		orderTime := lines[i+1]
		tradeNo := lines[i+2]
		// lines[i+3] = trade time (same as order time usually, skip)
		security := lines[i+4]
		bs := lines[i+5]
		exchange := lines[i+6]
		qty := lines[i+7]
		// lines[i+8] = brokerage (skip)
		netRate := lines[i+9]
		netTotal := lines[i+10]

		// Validate required fields.
		if !reTime.MatchString(orderTime) ||
			!reBuySell.MatchString(bs) ||
			!reExchange.MatchString(exchange) ||
			!reQtyInt.MatchString(qty) ||
			!reRate.MatchString(netRate) {
			i++
			continue
		}

		// Parse symbol and ISIN from security description.
		// Format: SYMBOL-SERIES/ISIN (e.g. JUNIORBEES-EQ/INF732E01045)
		symbol, isin := parseSecurityDesc(security)
		if symbol == "" {
			i++
			continue
		}

		rows = append(rows, tradeRow{
			OrderNo:  orderNo,
			TradeNo:  tradeNo,
			Time:     orderTime,
			Symbol:   symbol,
			ISIN:     isin,
			BuySell:  bs,
			Exchange: exchange,
			Qty:      qty,
			NetRate:  strings.ReplaceAll(netRate, ",", ""),
			NetTotal: strings.ReplaceAll(netTotal, ",", ""),
		})

		i += 11 // advance past this block; remarks line (if any) will be skipped in next iteration
	}
	return rows
}

// parseSecurityDesc extracts symbol and ISIN from "JUNIORBEES-EQ/INF732E01045".
func parseSecurityDesc(s string) (symbol, isin string) {
	m := reSecurity.FindStringSubmatch(s)
	if len(m) == 3 {
		return strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
	}
	// Fallback: just use the whole string as symbol if it looks like an instrument name.
	if len(s) > 2 && len(s) < 30 && !strings.ContainsAny(s, " 0123456789") {
		return s, ""
	}
	return "", ""
}

// inferAssetType classifies a symbol by ISIN prefix and known ETF suffixes.
func inferAssetType(symbol, isin string) string {
	upper := strings.ToUpper(symbol)
	if strings.HasSuffix(upper, "BEES") || strings.HasSuffix(upper, "ETF") {
		if strings.Contains(upper, "GOLD") || strings.Contains(upper, "SILVER") {
			return domain.AssetTypeMetal
		}
		return domain.AssetTypeETF
	}
	if strings.HasPrefix(isin, "INF") {
		return domain.AssetTypeMF
	}
	return domain.AssetTypeStock
}

// cleanLines splits text into non-empty trimmed lines.
func cleanLines(text string) []string {
	raw := strings.Split(text, "\n")
	out := make([]string, 0, len(raw))
	for _, l := range raw {
		l = strings.TrimSpace(l)
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}
