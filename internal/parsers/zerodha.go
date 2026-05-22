package parsers

import (
	"fmt"
	"strings"

	"github.com/hemanthakumar97/wealthfolio/internal/domain"
)

// ZerodhaParser handles Zerodha tradebook / P&L exports.
//
// Headers vary by report type but reliably contain: ISIN, Trade Date, Quantity.
// Other columns we use: Symbol, Trade Type (buy/sell), Price, Order ID, Exchange / Segment.
type ZerodhaParser struct{}

func (z *ZerodhaParser) Name() string { return "Zerodha Tradebook" }

func (z *ZerodhaParser) Detect(headers []string) bool {
	joined := normHeader(strings.Join(headers, " "))
	return strings.Contains(joined, "isin") &&
		strings.Contains(joined, "trade date") &&
		strings.Contains(joined, "quantity")
}

func (z *ZerodhaParser) Parse(rows [][]string, headerRow int) ([]NormalizedTransaction, []ParseError) {
	if headerRow < 0 || headerRow >= len(rows) {
		return nil, []ParseError{{Row: 0, Message: "header row not found"}}
	}
	idx := indexHeaders(rows[headerRow])

	colSymbol := pickFirst(idx, "symbol", "tradingsymbol")
	colISIN := pickFirst(idx, "isin")
	colExchange := pickFirst(idx, "exchange", "segment")
	colTradeDate := pickFirst(idx, "trade date", "tradedate")
	colTradeType := pickFirst(idx, "trade type", "tradetype", "buy/sell")
	colQty := pickFirst(idx, "quantity", "qty")
	colPrice := pickFirst(idx, "price", "rate", "avg price")
	colOrderID := pickFirst(idx, "order id", "orderid")
	colTradeID := pickFirst(idx, "trade id", "tradeid")

	if colISIN == -1 || colTradeDate == -1 || colQty == -1 {
		return nil, []ParseError{{Row: headerRow + 1, Message: "missing required columns (ISIN / Trade Date / Quantity)"}}
	}

	out := make([]NormalizedTransaction, 0, len(rows)-headerRow-1)
	errs := make([]ParseError, 0)

	for i := headerRow + 1; i < len(rows); i++ {
		row := rows[i]
		rowNum := i + 1 // 1-indexed in source file

		isin := get(row, colISIN)
		if isin == "" {
			// Skip blank rows / footer.
			continue
		}

		dateStr := get(row, colTradeDate)
		date, err := parseDate(strings.Fields(dateStr)[0])
		if err != nil {
			errs = append(errs, ParseError{Row: rowNum, Message: err.Error()})
			continue
		}

		qty, err := parseDecimal(get(row, colQty))
		if err != nil {
			errs = append(errs, ParseError{Row: rowNum, Message: fmt.Sprintf("quantity: %v", err)})
			continue
		}
		price, err := parseDecimal(get(row, colPrice))
		if err != nil {
			errs = append(errs, ParseError{Row: rowNum, Message: fmt.Sprintf("price: %v", err)})
			continue
		}

		txType := normalizeZerodhaType(get(row, colTradeType))
		if txType == "" {
			errs = append(errs, ParseError{Row: rowNum, Message: "could not infer trade type"})
			continue
		}

		amount := qty.Mul(price)
		exchange := get(row, colExchange)
		assetType := inferAssetTypeFromISIN(isin)
		if assetType == "" {
			// Equity reports often imply STOCK; MF segment column would say MF/MUTUAL.
			if strings.Contains(strings.ToLower(exchange), "mf") {
				assetType = domain.AssetTypeMF
			} else {
				assetType = domain.AssetTypeStock
			}
		}

		out = append(out, NormalizedTransaction{
			InstrumentName:  get(row, colSymbol),
			ISIN:            isin,
			AssetType:       assetType,
			Exchange:        exchange,
			TransactionDate: date,
			TransactionType: txType,
			Quantity:        qty.Abs(),
			Price:           price.Abs(),
			Amount:          amount.Abs().Round(2),
			Platform:        domain.PlatformZerodha,
			OrderID:         get(row, colOrderID),
			TradeID:         get(row, colTradeID),
			OriginalRow:     captureRow(rows[headerRow], row),
			RowNumber:       rowNum,
		})
	}

	return out, errs
}

func normalizeZerodhaType(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "buy", "b":
		return domain.TxBuy
	case "sell", "s":
		return domain.TxSell
	case "":
		return ""
	}
	if strings.Contains(s, "buy") {
		return domain.TxBuy
	}
	if strings.Contains(s, "sell") {
		return domain.TxSell
	}
	return ""
}

func captureRow(headers, row []string) map[string]string {
	out := make(map[string]string, len(headers))
	for i, h := range headers {
		if i >= len(row) {
			break
		}
		key := strings.TrimSpace(h)
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(row[i])
	}
	return out
}
