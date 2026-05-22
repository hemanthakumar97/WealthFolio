package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

type YahooClient struct {
	client *http.Client
}

func NewYahooClient() *YahooClient {
	return &YahooClient{client: &http.Client{Timeout: 20 * time.Second}}
}

type YahooMatch struct {
	Symbol   string `json:"symbol"`
	Name     string `json:"name"`
	Exchange string `json:"exchange"`
	ExchDisp string `json:"exch_disp"`
	Type     string `json:"type"` // EQUITY, ETF, MUTUALFUND
}

func (y *YahooClient) do(ctx context.Context, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json,text/html,*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	return y.client.Do(req)
}

// Search finds securities on Yahoo Finance matching the query.
// If usOnly is true, results are not filtered by exchange suffix (used for US-listed tickers).
// Otherwise only .NS / .BO (Indian exchange) results are returned.
func (y *YahooClient) Search(ctx context.Context, query string, limit int, usOnly bool) ([]YahooMatch, error) {
	country := "IN"
	if usOnly {
		country = "US"
	}
	u := fmt.Sprintf(
		"https://query2.finance.yahoo.com/v1/finance/search?q=%s&quotesCount=%d&newsCount=0&enableFuzzyQuery=true&country=%s",
		url.QueryEscape(query), limit, country,
	)
	resp, err := y.do(ctx, u)
	if err != nil {
		return nil, fmt.Errorf("yahoo search: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("yahoo search: HTTP %d", resp.StatusCode)
	}

	var body struct {
		Quotes []struct {
			Symbol    string `json:"symbol"`
			Shortname string `json:"shortname"`
			Longname  string `json:"longname"`
			Exchange  string `json:"exchange"`
			ExchDisp  string `json:"exchDisp"`
			QuoteType string `json:"quoteType"`
		} `json:"quotes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("yahoo search decode: %w", err)
	}

	out := make([]YahooMatch, 0, len(body.Quotes))
	indianResults := make([]YahooMatch, 0)

	for _, q := range body.Quotes {
		if len(q.Symbol) < 1 {
			continue
		}
		name := q.Longname
		if name == "" {
			name = q.Shortname
		}
		match := YahooMatch{
			Symbol:   q.Symbol,
			Name:     name,
			Exchange: q.Exchange,
			ExchDisp: q.ExchDisp,
			Type:     q.QuoteType,
		}

		if !usOnly {
			// Check for Indian exchange suffix (.NS or .BO)
			if len(q.Symbol) >= 3 {
				suffix := q.Symbol[len(q.Symbol)-3:]
				if suffix == ".NS" || suffix == ".BO" {
					indianResults = append(indianResults, match)
				}
			}
		}
		out = append(out, match)
	}

	// If we were looking for Indian results specifically and found some, return only those.
	if !usOnly && len(indianResults) > 0 {
		return indianResults, nil
	}

	// Otherwise return all matches (either usOnly=true case, or no Indian results found).
	return out, nil
}

// FetchQuote returns the latest price for a Yahoo Finance symbol (e.g. "GC=F", "SI=F", "USDINR=X").
func (y *YahooClient) FetchQuote(ctx context.Context, symbol string) (float64, error) {
	u := fmt.Sprintf(
		"https://query1.finance.yahoo.com/v8/finance/chart/%s?interval=1d&range=5d",
		url.PathEscape(symbol),
	)
	resp, err := y.do(ctx, u)
	if err != nil {
		return 0, fmt.Errorf("yahoo quote: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("yahoo quote: HTTP %d for %s", resp.StatusCode, symbol)
	}

	var body struct {
		Chart struct {
			Result []struct {
				Meta struct {
					RegularMarketPrice float64 `json:"regularMarketPrice"`
				} `json:"meta"`
			} `json:"result"`
			Err *struct{ Description string } `json:"error"`
		} `json:"chart"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, fmt.Errorf("yahoo quote decode: %w", err)
	}
	if body.Chart.Err != nil {
		return 0, fmt.Errorf("yahoo quote: %s", body.Chart.Err.Description)
	}
	if len(body.Chart.Result) == 0 || body.Chart.Result[0].Meta.RegularMarketPrice == 0 {
		return 0, fmt.Errorf("yahoo quote: no price for %s", symbol)
	}
	return body.Chart.Result[0].Meta.RegularMarketPrice, nil
}

// FetchHistory returns daily close prices for a Yahoo Finance symbol.
func (y *YahooClient) FetchHistory(ctx context.Context, symbol string) ([]SheetRow, error) {
	u := fmt.Sprintf(
		"https://query1.finance.yahoo.com/v8/finance/chart/%s?interval=1d&range=max&events=history",
		url.PathEscape(symbol),
	)
	resp, err := y.do(ctx, u)
	if err != nil {
		return nil, fmt.Errorf("yahoo history: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("yahoo history: HTTP %d for %s", resp.StatusCode, symbol)
	}

	var body struct {
		Chart struct {
			Result []struct {
				Timestamps []int64 `json:"timestamp"`
				Indicators struct {
					Quote []struct {
						Close []float64 `json:"close"`
					} `json:"quote"`
					AdjClose []struct {
						AdjClose []float64 `json:"adjclose"`
					} `json:"adjclose"`
				} `json:"indicators"`
			} `json:"result"`
			Err *struct{ Description string } `json:"error"`
		} `json:"chart"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("yahoo history decode: %w", err)
	}
	if body.Chart.Err != nil {
		return nil, fmt.Errorf("yahoo history: %s", body.Chart.Err.Description)
	}
	if len(body.Chart.Result) == 0 {
		return nil, fmt.Errorf("yahoo history: no data returned for %s", symbol)
	}

	result := body.Chart.Result[0]
	timestamps := result.Timestamps

	// Prefer adjusted close; fall back to regular close.
	var prices []float64
	if len(result.Indicators.AdjClose) > 0 {
		prices = result.Indicators.AdjClose[0].AdjClose
	} else if len(result.Indicators.Quote) > 0 {
		prices = result.Indicators.Quote[0].Close
	}
	if len(prices) == 0 {
		return nil, fmt.Errorf("yahoo history: no price data for %s", symbol)
	}

	rows := make([]SheetRow, 0, len(timestamps))
	for i, ts := range timestamps {
		if i >= len(prices) {
			break
		}
		price := prices[i]
		if price <= 0 {
			continue
		}
		rows = append(rows, SheetRow{
			Date:  time.Unix(ts, 0).UTC().Truncate(24 * time.Hour),
			Close: price,
		})
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("yahoo history: no valid rows for %s", symbol)
	}
	return rows, nil
}
