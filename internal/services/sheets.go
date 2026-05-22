package services

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// SheetRow is one parsed row from the CSV: date + close price.
type SheetRow struct {
	Date  time.Time `json:"date"`
	Close float64   `json:"close"`
}

// SheetsService downloads a public Google Sheet as CSV and parses it.
type SheetsService struct {
	client *http.Client
}

func NewSheetsService() *SheetsService {
	return &SheetsService{client: &http.Client{Timeout: 30 * time.Second}}
}

// sheetIDRe extracts the spreadsheet ID from any share/edit URL.
var sheetIDRe = regexp.MustCompile(`/spreadsheets/d/([a-zA-Z0-9_-]+)`)

// gidRe extracts the gid (tab) from a URL fragment.
var gidRe = regexp.MustCompile(`gid=(\d+)`)

// FetchCSV downloads the sheet and returns all rows.
// sheetURL can be any Google Sheets share/edit/pub URL.
func (s *SheetsService) FetchCSV(ctx context.Context, sheetURL string) ([]SheetRow, error) {
	csvURL, err := toCSVExportURL(sheetURL)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, csvURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible)")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sheet fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sheet: HTTP %d", resp.StatusCode)
	}

	return parseSheetCSV(resp.Body)
}

// ParseUploadedCSV parses a CSV/TSV reader with Date+Close columns.
func ParseUploadedCSV(r io.Reader) ([]SheetRow, error) {
	return parseSheetCSV(r)
}

// toCSVExportURL converts any Google Sheets URL to its CSV export URL.
func toCSVExportURL(u string) (string, error) {
	m := sheetIDRe.FindStringSubmatch(u)
	if len(m) < 2 {
		return "", fmt.Errorf("cannot extract spreadsheet ID from URL: %q", u)
	}
	id := m[1]

	gid := "0"
	if gm := gidRe.FindStringSubmatch(u); len(gm) >= 2 {
		gid = gm[1]
	}
	return fmt.Sprintf(
		"https://docs.google.com/spreadsheets/d/%s/export?format=csv&gid=%s", id, gid,
	), nil
}

// parseSheetCSV reads a CSV with flexible column detection for Date and Close.
// Acceptable header names (case-insensitive):
//   Date: "date", "Date", "DATE", "Day"
//   Close: "close", "Close", "CLOSE", "Price", "NAV", "nav_price"
func parseSheetCSV(r io.Reader) ([]SheetRow, error) {
	cr := csv.NewReader(r)
	cr.TrimLeadingSpace = true
	cr.FieldsPerRecord = -1

	records, err := cr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("csv read: %w", err)
	}
	if len(records) < 2 {
		return nil, fmt.Errorf("csv: need at least a header row and one data row")
	}

	dateCol, closeCol := -1, -1
	for i, h := range records[0] {
		h = strings.ToLower(strings.TrimSpace(h))
		switch h {
		case "date", "day", "trade_date":
			if dateCol < 0 {
				dateCol = i
			}
		case "close", "price", "nav", "nav_price", "close price", "closing price":
			if closeCol < 0 {
				closeCol = i
			}
		}
	}
	if dateCol < 0 {
		return nil, fmt.Errorf("csv: could not find a Date column (tried: date, day, trade_date)")
	}
	if closeCol < 0 {
		return nil, fmt.Errorf("csv: could not find a Close/Price column (tried: close, price, nav, nav_price)")
	}

	var rows []SheetRow
	dateFormats := []string{
		"2006-01-02", "02-01-2006", "01/02/2006",
		"2/1/2006", "02/01/2006", "Jan 2, 2006", "2 Jan 2006",
	}

	for i, rec := range records[1:] {
		if dateCol >= len(rec) || closeCol >= len(rec) {
			continue
		}
		dateStr := strings.TrimSpace(rec[dateCol])
		closeStr := strings.TrimSpace(rec[closeCol])
		if dateStr == "" || closeStr == "" {
			continue
		}
		// Remove commas from numbers like "1,234.56"
		closeStr = strings.ReplaceAll(closeStr, ",", "")

		var t time.Time
		var parseErr error
		for _, fmt := range dateFormats {
			t, parseErr = time.Parse(fmt, dateStr)
			if parseErr == nil {
				break
			}
		}
		if parseErr != nil {
			_ = i // silently skip unparseable dates
			continue
		}

		var price float64
		if _, err := fmt.Sscanf(closeStr, "%f", &price); err != nil {
			continue
		}
		if price <= 0 {
			continue
		}
		rows = append(rows, SheetRow{Date: t, Close: price})
	}

	if len(rows) == 0 {
		return nil, fmt.Errorf("csv: no valid rows found (date + positive price)")
	}
	return rows, nil
}
