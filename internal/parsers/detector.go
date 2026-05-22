package parsers

import "errors"

var ErrNoParser = errors.New("no parser matches this file")

// Registered parsers, ordered most-specific to least-specific.
var registry = []Parser{
	&ZerodhaParser{},
	&GrowwMFParser{},
	&IndMoneyOrderParser{},
}

// Detect returns the parser that matches the supplied rows, plus the index of the header row.
func Detect(rows [][]string) (Parser, int, error) {
	for _, p := range registry {
		// Try first 30 rows as possible header rows.
		limit := len(rows)
		if limit > 30 {
			limit = 30
		}
		for i := 0; i < limit; i++ {
			if p.Detect(rows[i]) {
				return p, i, nil
			}
		}
	}
	return nil, -1, ErrNoParser
}

// SupportedFormats returns metadata about each known parser for the UI.
type FormatDescriptor struct {
	Name     string   `json:"name"`
	Platform string   `json:"platform"`
	Markers  []string `json:"markers"`
	Notes    string   `json:"notes"`
}

func SupportedFormats() []FormatDescriptor {
	return []FormatDescriptor{
		{
			Name:     "Zerodha Tradebook",
			Platform: "ZERODHA",
			Markers:  []string{"ISIN", "Trade Date", "Quantity"},
			Notes:    "Tradebook / P&L statement exported from Console (CSV or XLSX).",
		},
		{
			Name:     "Groww Mutual Funds",
			Platform: "GROWW",
			Markers:  []string{"Scheme Name", "Units", "NAV"},
			Notes:    "MF transaction statement (CSV or XLSX).",
		},
		{
			Name:     "INDMoney Order Report",
			Platform: "INDMONEY",
			Markers:  []string{"Stock Symbol", "Broker Reference Id", "Order Amount ($)"},
			Notes:    "US stock order report exported from INDMoney (XLS format).",
		},
	}
}
