// Package parsers turns broker-exported CSV/XLSX files into NormalizedTransaction rows
// the rest of the app understands. Each broker has its own parser; the Detector picks
// the right one based on header columns.
package parsers

import (
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// NormalizedTransaction is the shape every parser must produce.
type NormalizedTransaction struct {
	InstrumentName  string
	ISIN            string // optional ("")
	AMFICode        string // optional ("")
	AssetType       string // domain.AssetType*
	Currency        string // domain.Currency*
	Exchange        string // optional ("")
	TransactionDate time.Time
	TransactionType string // domain.Tx*
	Quantity        decimal.Decimal
	Price           decimal.Decimal
	Amount          decimal.Decimal
	Platform        string // domain.Platform*
	OrderID         string // optional ("") — broker order; may be shared across partial fills
	TradeID         string // optional ("") — unique per trade execution (e.g. Zerodha trade_id)
	OriginalRow     map[string]string
	RowNumber       int // 1-indexed row in source file (for ImportLog)
}

// Parser is what each broker implementation provides.
type Parser interface {
	Name() string
	Detect(headers []string) bool
	Parse(rows [][]string, headerRow int) (out []NormalizedTransaction, errs []ParseError)
}

type ParseError struct {
	Row     int
	Message string
}

func (e ParseError) Error() string {
	return fmt.Sprintf("row %d: %s", e.Row, e.Message)
}

// --- helpers -----------------------------------------------------------------

// normalize a header cell for fuzzy matching.
func normHeader(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.ReplaceAll(s, "-", " ")
	return strings.Join(strings.Fields(s), " ")
}

// findHeaderRow scans rows top-down for the first row containing all markers.
// markers must be lowercase substrings. Returns -1 if not found.
func findHeaderRow(rows [][]string, markers []string) int {
	for i, row := range rows {
		joined := normHeader(strings.Join(row, " "))
		matched := true
		for _, m := range markers {
			if !strings.Contains(joined, m) {
				matched = false
				break
			}
		}
		if matched {
			return i
		}
	}
	return -1
}

// indexHeaders maps normalized header -> column index for a header row.
func indexHeaders(headers []string) map[string]int {
	out := make(map[string]int, len(headers))
	for i, h := range headers {
		out[normHeader(h)] = i
	}
	return out
}

// pickFirst returns the first column index whose normalized header contains one of the
// candidate substrings; -1 if none match.
func pickFirst(idx map[string]int, candidates ...string) int {
	for header, col := range idx {
		for _, c := range candidates {
			if strings.Contains(header, c) {
				return col
			}
		}
	}
	return -1
}

func get(row []string, col int) string {
	if col < 0 || col >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[col])
}

func parseDecimal(s string) (decimal.Decimal, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	if s == "" {
		return decimal.Zero, nil
	}
	return decimal.NewFromString(s)
}

// parseDate tries a handful of common formats brokers use.
var dateLayouts = []string{
	"2006-01-02",
	"02-01-2006",
	"02/01/2006",
	"01/02/2006",
	"2006-01-02 15:04:05",
	"02-Jan-2006",
	"2-Jan-2006",
	"02 Jan 2006",
	"Jan 02, 2006",
}

func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty date")
	}
	// Strip trailing time portion separated by whitespace if it confuses the layout.
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized date format: %q", s)
}

// inferAssetTypeFromISIN: Indian ISINs starting with INF / INE patterns -> MF / STOCK heuristic.
// INF... -> mutual fund. INE..., IN9..., IND... -> equity / stock.
func inferAssetTypeFromISIN(isin string) string {
	isin = strings.ToUpper(strings.TrimSpace(isin))
	switch {
	case strings.HasPrefix(isin, "INF"):
		return "MF"
	case strings.HasPrefix(isin, "INE"), strings.HasPrefix(isin, "IND"), strings.HasPrefix(isin, "IN9"):
		return "STOCK"
	default:
		return ""
	}
}
