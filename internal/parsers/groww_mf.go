package parsers

import (
	"fmt"
	"strings"

	"github.com/hemanthhku/wealthfolio-v2/internal/domain"
)

// GrowwMFParser handles Groww mutual-fund transaction statements.
//
// Required headers: Scheme Name, Units, NAV. Plus typically: Transaction Type, Amount, Date, ISIN.
type GrowwMFParser struct{}

func (g *GrowwMFParser) Name() string { return "Groww Mutual Funds" }

func (g *GrowwMFParser) Detect(headers []string) bool {
	joined := normHeader(strings.Join(headers, " "))
	return strings.Contains(joined, "scheme name") &&
		strings.Contains(joined, "units") &&
		strings.Contains(joined, "nav")
}

func (g *GrowwMFParser) Parse(rows [][]string, headerRow int) ([]NormalizedTransaction, []ParseError) {
	if headerRow < 0 || headerRow >= len(rows) {
		return nil, []ParseError{{Row: 0, Message: "header row not found"}}
	}
	idx := indexHeaders(rows[headerRow])

	colScheme := pickFirst(idx, "scheme name")
	colISIN := pickFirst(idx, "isin")
	colAMFI := pickFirst(idx, "amfi", "scheme code")
	colDate := pickFirst(idx, "date")
	colType := pickFirst(idx, "transaction type", "type")
	colUnits := pickFirst(idx, "units")
	colNAV := pickFirst(idx, "nav")
	colAmount := pickFirst(idx, "amount")
	colOrderID := pickFirst(idx, "order id", "folio")

	if colScheme == -1 || colDate == -1 || colUnits == -1 || colNAV == -1 {
		return nil, []ParseError{{Row: headerRow + 1, Message: "missing required columns (Scheme Name / Date / Units / NAV)"}}
	}

	out := make([]NormalizedTransaction, 0, len(rows)-headerRow-1)
	errs := make([]ParseError, 0)

	for i := headerRow + 1; i < len(rows); i++ {
		row := rows[i]
		rowNum := i + 1

		scheme := get(row, colScheme)
		if scheme == "" {
			continue
		}

		date, err := parseDate(get(row, colDate))
		if err != nil {
			errs = append(errs, ParseError{Row: rowNum, Message: err.Error()})
			continue
		}

		units, err := parseDecimal(get(row, colUnits))
		if err != nil {
			errs = append(errs, ParseError{Row: rowNum, Message: fmt.Sprintf("units: %v", err)})
			continue
		}
		nav, err := parseDecimal(get(row, colNAV))
		if err != nil {
			errs = append(errs, ParseError{Row: rowNum, Message: fmt.Sprintf("nav: %v", err)})
			continue
		}
		amount, err := parseDecimal(get(row, colAmount))
		if err != nil || amount.IsZero() {
			amount = units.Mul(nav)
		}

		txType := normalizeGrowwType(get(row, colType))
		if txType == "" {
			// Default: positive units -> BUY, negative -> SELL.
			if units.IsNegative() {
				txType = domain.TxSell
			} else {
				txType = domain.TxBuy
			}
		}

		out = append(out, NormalizedTransaction{
			InstrumentName:  scheme,
			ISIN:            get(row, colISIN),
			AMFICode:        get(row, colAMFI),
			AssetType:       domain.AssetTypeMF,
			TransactionDate: date,
			TransactionType: txType,
			Quantity:        units.Abs(),
			Price:           nav.Abs(),
			Amount:          amount.Abs().Round(2),
			Platform:        domain.PlatformGroww,
			OrderID:         get(row, colOrderID),
			OriginalRow:     captureRow(rows[headerRow], row),
			RowNumber:       rowNum,
		})
	}

	return out, errs
}

func normalizeGrowwType(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case s == "":
		return ""
	case strings.Contains(s, "purchase"), strings.Contains(s, "buy"), strings.Contains(s, "invest"), strings.Contains(s, "sip"):
		return domain.TxBuy
	case strings.Contains(s, "redemption"), strings.Contains(s, "redeem"), strings.Contains(s, "sell"), strings.Contains(s, "withdraw"):
		return domain.TxSell
	case strings.Contains(s, "switch in"):
		return domain.TxSwitchIn
	case strings.Contains(s, "switch out"), strings.Contains(s, "switch"):
		return domain.TxSwitchOut
	case strings.Contains(s, "dividend reinvest"), strings.Contains(s, "reinvest"):
		return domain.TxBonus
	case strings.Contains(s, "dividend"):
		return domain.TxDividend
	case strings.Contains(s, "bonus"):
		return domain.TxBonus
	}
	return ""
}
