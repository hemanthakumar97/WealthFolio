package parsers_test

import (
	"strings"
	"testing"

	"github.com/hemanthakumar97/wealthfolio/internal/parsers"
)

const zerodhaCSV = `Symbol,ISIN,Exchange,Segment,Series,Trade Date,Trade Type,Auction,Quantity,Price,Trade ID,Order ID,Order Execution Time
RELIANCE,INE002A01018,NSE,EQ,EQ,2024-01-15,buy,,10,2500.00,TR001,ORD001,2024-01-15 10:30:00
INFY,INE009A01021,NSE,EQ,EQ,2024-01-20,sell,,5,1800.50,TR002,ORD002,2024-01-20 11:00:00`

const growwCSV = `Date,Folio Number,Scheme Name,Transaction Type,Units,NAV,Amount,ISIN,AMC
15-Jan-2024,1234567,HDFC Top 100 Fund - Growth,Purchase,100.000,750.5000,75050,INF179K01BB3,HDFC
20-Jan-2024,1234567,HDFC Top 100 Fund - Growth,Redemption,50.000,760.0000,38000,INF179K01BB3,HDFC`

func mustRows(t *testing.T, csv string) [][]string {
	t.Helper()
	r, err := parsers.LoadRows(strings.NewReader(csv), "test.csv")
	if err != nil {
		t.Fatalf("LoadRows: %v", err)
	}
	return r
}

func TestZerodhaDetectAndParse(t *testing.T) {
	rows := mustRows(t, zerodhaCSV)
	p, hdr, err := parsers.Detect(rows)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if p.Name() != "Zerodha Tradebook" {
		t.Fatalf("parser name: got %q, want Zerodha Tradebook", p.Name())
	}

	out, errs := p.Parse(rows, hdr)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	if len(out) != 2 {
		t.Fatalf("row count: got %d, want 2", len(out))
	}

	buy := out[0]
	if buy.TransactionType != "BUY" {
		t.Errorf("[0] type: got %q, want BUY", buy.TransactionType)
	}
	if buy.ISIN != "INE002A01018" {
		t.Errorf("[0] ISIN: got %q", buy.ISIN)
	}
	if buy.Quantity.String() != "10" {
		t.Errorf("[0] qty: got %s, want 10", buy.Quantity.String())
	}
	if buy.Platform != "ZERODHA" {
		t.Errorf("[0] platform: got %q", buy.Platform)
	}
	if buy.OrderID != "ORD001" {
		t.Errorf("[0] order_id: got %q", buy.OrderID)
	}

	sell := out[1]
	if sell.TransactionType != "SELL" {
		t.Errorf("[1] type: got %q, want SELL", sell.TransactionType)
	}
}

func TestGrowwDetectAndParse(t *testing.T) {
	rows := mustRows(t, growwCSV)
	p, hdr, err := parsers.Detect(rows)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if p.Name() != "Groww Mutual Funds" {
		t.Fatalf("parser name: got %q, want Groww Mutual Funds", p.Name())
	}

	out, errs := p.Parse(rows, hdr)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	if len(out) != 2 {
		t.Fatalf("row count: got %d, want 2", len(out))
	}

	buy := out[0]
	if buy.TransactionType != "BUY" {
		t.Errorf("[0] type: got %q, want BUY", buy.TransactionType)
	}
	if buy.AssetType != "MF" {
		t.Errorf("[0] asset_type: got %q, want MF", buy.AssetType)
	}
	if buy.Platform != "GROWW" {
		t.Errorf("[0] platform: got %q", buy.Platform)
	}
	if buy.ISIN != "INF179K01BB3" {
		t.Errorf("[0] ISIN: got %q", buy.ISIN)
	}
	if buy.Quantity.String() != "100" {
		t.Errorf("[0] units: got %s", buy.Quantity.String())
	}

	if out[1].TransactionType != "SELL" {
		t.Errorf("[1] type: got %q, want SELL", out[1].TransactionType)
	}
}

func TestUnknownFormatErrors(t *testing.T) {
	junk := [][]string{{"col1", "col2"}, {"a", "b"}}
	_, _, err := parsers.Detect(junk)
	if err == nil {
		t.Fatal("expected ErrNoParser, got nil")
	}
}
