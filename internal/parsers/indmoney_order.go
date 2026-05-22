package parsers

import (
	"fmt"
	"strings"

	"github.com/hemanthakumar97/wealthfolio/internal/domain"
)

// IndMoneyOrderParser parses INDMoney US-stock order reports exported as .xls.
//
// Header row (after a few account-detail rows at the top):
//
//	Stock Name | Stock Symbol | Order Placed Time | Order Execution Time |
//	Broker Reference Id | Transaction Type | Order Type | Quantity | Price ($) |
//	Order Amount ($) | Brokerage ($)
type IndMoneyOrderParser struct{}

func (p *IndMoneyOrderParser) Name() string { return "INDMoney Order Report" }

func (p *IndMoneyOrderParser) Detect(headers []string) bool {
	joined := normHeader(strings.Join(headers, " "))
	return strings.Contains(joined, "stock symbol") &&
		strings.Contains(joined, "broker reference id") &&
		strings.Contains(joined, "order amount")
}

func (p *IndMoneyOrderParser) Parse(rows [][]string, headerRow int) ([]NormalizedTransaction, []ParseError) {
	if headerRow < 0 || headerRow >= len(rows) {
		return nil, []ParseError{{Row: 0, Message: "header row not found"}}
	}
	idx := indexHeaders(rows[headerRow])

	colSymbol := pickFirst(idx, "stock symbol")
	colName := pickFirst(idx, "stock name")
	colExecTime := pickFirst(idx, "order execution time")
	colPlacedTime := pickFirst(idx, "order placed time")
	colTxType := pickFirst(idx, "transaction type")
	colQty := pickFirst(idx, "quantity")
	colPrice := pickFirst(idx, "price")
	colAmount := pickFirst(idx, "order amount")
	colOrderID := pickFirst(idx, "broker reference id")

	if colSymbol < 0 || colTxType < 0 || colQty < 0 || colAmount < 0 {
		return nil, []ParseError{{Row: headerRow, Message: "required columns missing from INDMoney order report"}}
	}

	var out []NormalizedTransaction
	var errs []ParseError

	for rowIdx := headerRow + 1; rowIdx < len(rows); rowIdx++ {
		row := rows[rowIdx]
		symbol := get(row, colSymbol)
		if symbol == "" {
			continue
		}

		// Parse execution date — "09 Apr 2025, 09:54 PM"; take date portion only.
		dateStr := get(row, colExecTime)
		if dateStr == "" {
			dateStr = get(row, colPlacedTime)
		}
		// Strip the time part (everything after the comma) for date parsing.
		if comma := strings.Index(dateStr, ","); comma > 0 {
			dateStr = strings.TrimSpace(dateStr[:comma])
		}
		txDate, err := parseDate(dateStr)
		if err != nil {
			errs = append(errs, ParseError{Row: rowIdx + 1, Message: fmt.Sprintf("bad date %q: %v", dateStr, err)})
			continue
		}

		rawType := strings.ToUpper(strings.TrimSpace(get(row, colTxType)))
		var txType string
		switch rawType {
		case "BUY":
			txType = domain.TxBuy
		case "SELL":
			txType = domain.TxSell
		default:
			errs = append(errs, ParseError{Row: rowIdx + 1, Message: fmt.Sprintf("unknown transaction type %q", rawType)})
			continue
		}

		qty, err := parseDecimal(get(row, colQty))
		if err != nil || qty.IsZero() {
			errs = append(errs, ParseError{Row: rowIdx + 1, Message: fmt.Sprintf("invalid quantity: %v", err)})
			continue
		}

		amount, err := parseDecimal(get(row, colAmount))
		if err != nil {
			errs = append(errs, ParseError{Row: rowIdx + 1, Message: fmt.Sprintf("invalid amount: %v", err)})
			continue
		}
		if amount.IsNegative() {
			amount = amount.Neg()
		}

		priceVal, _ := parseDecimal(get(row, colPrice))
		if priceVal.IsZero() && !qty.IsZero() {
			priceVal = amount.Div(qty).Round(6)
		}

		instrumentName := get(row, colName)
		if instrumentName == "" {
			instrumentName = symbol
		}

		out = append(out, NormalizedTransaction{
			InstrumentName:  instrumentName,
			AssetType:       domain.AssetTypeUSFund,
			Currency:        domain.CurrencyUSD,
			TransactionDate: txDate,
			TransactionType: txType,
			Quantity:        qty,
			Price:           priceVal,
			Amount:          amount,
			Platform:        domain.PlatformINDMoney,
			OrderID:         get(row, colOrderID),
			RowNumber:       rowIdx + 1,
			OriginalRow: map[string]string{
				"stock_name":          instrumentName,
				"stock_symbol":        symbol,
				"order_execution_time": get(row, colExecTime),
				"transaction_type":    rawType,
				"quantity":            get(row, colQty),
				"price":               get(row, colPrice),
				"order_amount":        get(row, colAmount),
				"broker_reference_id": get(row, colOrderID),
			},
		})
	}

	return out, errs
}
