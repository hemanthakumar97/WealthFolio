package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const mfapiBase = "https://api.mfapi.in/mf"

type MFAPIClient struct {
	client *http.Client
}

func NewMFAPIClient() *MFAPIClient {
	return &MFAPIClient{client: &http.Client{Timeout: 15 * time.Second}}
}

type MFAPIMatch struct {
	SchemeCode int    `json:"schemeCode"`
	SchemeName string `json:"schemeName"`
}

// Search queries the MFAPI search endpoint and returns up to limit matches.
func (m *MFAPIClient) Search(ctx context.Context, query string, limit int) ([]MFAPIMatch, error) {
	u := fmt.Sprintf("%s/search?q=%s", mfapiBase, url.QueryEscape(query))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mfapi search: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mfapi search: HTTP %d", resp.StatusCode)
	}
	var results []MFAPIMatch
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("mfapi search decode: %w", err)
	}
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

type mfapiHistoryResp struct {
	Data []struct {
		Date string `json:"date"`
		NAV  string `json:"nav"`
	} `json:"data"`
}

// FetchHistory returns full NAV history for the given AMFI scheme code as SheetRows.
func (m *MFAPIClient) FetchHistory(ctx context.Context, amfiCode string) ([]SheetRow, error) {
	u := fmt.Sprintf("%s/%s", mfapiBase, amfiCode)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mfapi history: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mfapi history: HTTP %d", resp.StatusCode)
	}
	var payload mfapiHistoryResp
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("mfapi history decode: %w", err)
	}

	rows := make([]SheetRow, 0, len(payload.Data))
	for _, d := range payload.Data {
		// MFAPI date format: "02-01-2006" (DD-MM-YYYY)
		t, err := time.Parse("02-01-2006", d.Date)
		if err != nil {
			continue
		}
		navStr := strings.TrimSpace(d.NAV)
		if navStr == "" || navStr == "N.A." {
			continue
		}
		nav, err := strconv.ParseFloat(navStr, 64)
		if err != nil || nav <= 0 {
			continue
		}
		rows = append(rows, SheetRow{Date: t, Close: nav})
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("mfapi: no valid NAV rows in response")
	}
	return rows, nil
}
