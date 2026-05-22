package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/hemanthhku/wealthfolio-v2/internal/auth"
	"github.com/hemanthhku/wealthfolio-v2/internal/domain"
	"github.com/hemanthhku/wealthfolio-v2/internal/parsers"
	"github.com/hemanthhku/wealthfolio-v2/internal/services"
)

type TransactionsHandler struct {
	pool        *pgxpool.Pool
	instruments *services.InstrumentService
	duplicates  *services.DuplicateDetector
	importSvc   *services.ImportService
	uploadDir   string
	uploadMaxMB int
}

func NewTransactionsHandler(
	pool *pgxpool.Pool,
	instruments *services.InstrumentService,
	duplicates *services.DuplicateDetector,
	importSvc *services.ImportService,
	uploadDir string,
	uploadMaxMB int,
) *TransactionsHandler {
	return &TransactionsHandler{
		pool:        pool,
		instruments: instruments,
		duplicates:  duplicates,
		importSvc:   importSvc,
		uploadDir:   uploadDir,
		uploadMaxMB: uploadMaxMB,
	}
}

type uploadResult struct {
	UploadID          int64    `json:"upload_id"`
	Filename          string   `json:"filename"`
	Platform          string   `json:"platform"`
	RecordsTotal      int      `json:"records_total"`
	RecordsImported   int      `json:"records_imported"`
	RecordsDuplicates int      `json:"records_duplicates"`
	RecordsErrors     int      `json:"records_errors"`
	Errors            []string `json:"errors"`
}

func (h *TransactionsHandler) Upload(w http.ResponseWriter, r *http.Request) {
	maxBytes := int64(h.uploadMaxMB) * 1024 * 1024
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	if err := r.ParseMultipartForm(maxBytes); err != nil {
		writeError(w, http.StatusBadRequest, "file too large or invalid form")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing 'file' field")
		return
	}
	defer file.Close()

	platformOverride := strings.ToUpper(strings.TrimSpace(r.FormValue("platform")))

	identity, _ := auth.IdentityFrom(r.Context())
	storedPath, err := h.saveFile(file, header.Filename)
	if err != nil {
		slog.Error("save upload", "err", err)
		writeError(w, http.StatusInternalServerError, "could not save file")
		return
	}

	rows, err := readUploadedFile(storedPath, header.Filename)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("could not read file: %v", err))
		return
	}

	parser, headerRow, err := parsers.Detect(rows)
	platformDetected := platformOverride
	if err != nil {
		if platformOverride == "" {
			writeError(w, http.StatusBadRequest, "could not detect file format — pass 'platform' to override")
			return
		}
		writeError(w, http.StatusBadRequest, fmt.Sprintf("detect: %v", err))
		return
	}
	if platformDetected == "" {
		platformDetected = guessPlatformFromParser(parser)
	}

	uploadID, err := h.importSvc.CreateUploadRow(r.Context(), header.Filename, header.Size, platformDetected, identity.UserID)
	if err != nil {
		slog.Error("create upload row", "err", err)
		writeError(w, http.StatusInternalServerError, "could not record upload")
		return
	}

	parsed, parseErrors := parser.Parse(rows, headerRow)

	result := h.importSvc.PersistRows(r.Context(), uploadID, parsed, parseErrors)

	status := domain.UploadCompleted
	switch {
	case result.Imported == 0 && len(parsed) > 0:
		status = domain.UploadFailed
	case result.Errors > 0 || result.Duplicates > 0 && result.Imported == 0:
		status = domain.UploadPartial
	case result.Errors > 0 || (result.Duplicates > 0 && result.Imported > 0):
		status = domain.UploadPartial
	}
	total := len(parsed) + len(parseErrors)
	h.importSvc.FinalizeUploadRow(r.Context(), uploadID, status, total, result.Imported, result.Duplicates, result.Errors, result.ErrorMsgs)

	writeJSON(w, http.StatusOK, uploadResult{
		UploadID:          uploadID,
		Filename:          header.Filename,
		Platform:          platformDetected,
		RecordsTotal:      total,
		RecordsImported:   result.Imported,
		RecordsDuplicates: result.Duplicates,
		RecordsErrors:     result.Errors,
		Errors:            result.ErrorMsgs,
	})
}

type manualRequest struct {
	Symbol          string `json:"symbol"`
	Segment         string `json:"segment"`
	TransactionDate string `json:"transaction_date"`
	TransactionType string `json:"transaction_type"`
	OrderID         string `json:"order_id"`
	Amount          string `json:"amount"`
	Quantity        string `json:"quantity"`
	Platform        string `json:"platform"`
}

func (h *TransactionsHandler) Manual(w http.ResponseWriter, r *http.Request) {
	var req manualRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	symbol := strings.TrimSpace(req.Symbol)
	if symbol == "" {
		writeError(w, http.StatusBadRequest, "symbol required")
		return
	}
	date, err := time.Parse("2006-01-02", req.TransactionDate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "transaction_date must be YYYY-MM-DD")
		return
	}
	txType := strings.ToUpper(strings.TrimSpace(req.TransactionType))
	if !isValidTransactionType(txType) {
		writeError(w, http.StatusBadRequest, "invalid transaction_type")
		return
	}
	platform := strings.ToUpper(strings.TrimSpace(req.Platform))
	if !isValidPlatform(platform) {
		platform = domain.PlatformManual
	}
	qty, err := decimal.NewFromString(req.Quantity)
	if err != nil || qty.IsZero() {
		writeError(w, http.StatusBadRequest, "quantity invalid")
		return
	}
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil {
		writeError(w, http.StatusBadRequest, "amount invalid")
		return
	}
	price := decimal.Zero
	if !qty.IsZero() {
		price = amount.Div(qty)
	}

	assetType := assetTypeFromSegment(req.Segment)

	instrID, _, err := h.instruments.FindOrCreate(r.Context(), services.InstrumentSpec{
		Name:      symbol,
		AssetType: assetType,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not find/create instrument: "+err.Error())
		return
	}

	dup, _, err := h.duplicates.IsDuplicate(r.Context(), services.DupCheck{
		InstrumentID:    instrID,
		TransactionDate: date,
		TransactionType: txType,
		Quantity:        qty,
		Price:           price,
		Platform:        platform,
		OrderID:         strings.TrimSpace(req.OrderID),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if dup {
		writeError(w, http.StatusConflict, "duplicate transaction")
		return
	}

	var id int64
	err = h.pool.QueryRow(r.Context(),
		`INSERT INTO transactions
		   (instrument_id, transaction_date, transaction_type, quantity, price, amount, platform, order_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		 RETURNING id`,
		instrID, date, txType, qty, price, amount, platform, nullableString(req.OrderID),
	).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "insert: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":      id,
		"message": "transaction recorded",
	})
}

func (h *TransactionsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	tag, err := h.pool.Exec(r.Context(), `DELETE FROM transactions WHERE id = $1`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "delete: "+err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type uploadHistoryItem struct {
	ID                int64      `json:"id"`
	Filename          string     `json:"filename"`
	Platform          string     `json:"platform"`
	Status            string     `json:"status"`
	RecordsTotal      int        `json:"records_total"`
	RecordsImported   int        `json:"records_imported"`
	RecordsDuplicates int        `json:"records_duplicates"`
	RecordsErrors     int        `json:"records_errors"`
	UploadedAt        time.Time  `json:"uploaded_at"`
	ProcessedAt       *time.Time `json:"processed_at"`
}

func (h *TransactionsHandler) History(w http.ResponseWriter, r *http.Request) {
	rows, err := h.pool.Query(r.Context(),
		`SELECT id, filename, platform, status, records_total, records_imported,
		        records_duplicates, records_errors, uploaded_at, processed_at
		   FROM upload_history
		  ORDER BY uploaded_at DESC
		  LIMIT 100`,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query: "+err.Error())
		return
	}
	defer rows.Close()
	out := []uploadHistoryItem{}
	for rows.Next() {
		var item uploadHistoryItem
		if err := rows.Scan(&item.ID, &item.Filename, &item.Platform, &item.Status,
			&item.RecordsTotal, &item.RecordsImported, &item.RecordsDuplicates,
			&item.RecordsErrors, &item.UploadedAt, &item.ProcessedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "scan: "+err.Error())
			return
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, out)
}

type uploadDetail struct {
	uploadHistoryItem
	Errors []string `json:"errors"`
}

func (h *TransactionsHandler) HistoryItem(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var item uploadDetail
	var errorsJSON []byte
	err = h.pool.QueryRow(r.Context(),
		`SELECT id, filename, platform, status, records_total, records_imported,
		        records_duplicates, records_errors, uploaded_at, processed_at, error_details
		   FROM upload_history WHERE id = $1`,
		id,
	).Scan(&item.ID, &item.Filename, &item.Platform, &item.Status,
		&item.RecordsTotal, &item.RecordsImported, &item.RecordsDuplicates,
		&item.RecordsErrors, &item.UploadedAt, &item.ProcessedAt, &errorsJSON)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(errorsJSON) > 0 {
		_ = json.Unmarshal(errorsJSON, &item.Errors)
	}
	writeJSON(w, http.StatusOK, item)
}

type importLogItem struct {
	ID        int64  `json:"id"`
	RowNumber *int   `json:"row_number"`
	Status    string `json:"status"`
	Message   string `json:"message"`
}

func (h *TransactionsHandler) Logs(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	rows, err := h.pool.Query(r.Context(),
		`SELECT id, row_number, status, COALESCE(message, '') FROM import_logs WHERE upload_id = $1 ORDER BY id`,
		id,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []importLogItem{}
	for rows.Next() {
		var l importLogItem
		if err := rows.Scan(&l.ID, &l.RowNumber, &l.Status, &l.Message); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, l)
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *TransactionsHandler) Formats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, parsers.SupportedFormats())
}

// --- helpers ---

func (h *TransactionsHandler) saveFile(src io.Reader, originalName string) (string, error) {
	dir := filepath.Join(h.uploadDir, "transactions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	ext := strings.ToLower(filepath.Ext(originalName))
	if ext == "" {
		ext = ".csv"
	}
	dest := filepath.Join(dir, uuid.NewString()+ext)
	out, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	defer out.Close()
	if _, err := io.Copy(out, src); err != nil {
		return "", err
	}
	return dest, nil
}

func readUploadedFile(path, originalName string) ([][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parsers.LoadRows(f, originalName)
}

func guessPlatformFromParser(p parsers.Parser) string {
	switch strings.ToLower(p.Name()) {
	case "zerodha tradebook":
		return domain.PlatformZerodha
	case "groww mutual funds":
		return domain.PlatformGroww
	default:
		return domain.PlatformManual
	}
}

func isValidTransactionType(t string) bool {
	switch t {
	case domain.TxBuy, domain.TxSell, domain.TxSwitchIn, domain.TxSwitchOut,
		domain.TxDividend, domain.TxBonus, domain.TxSplit:
		return true
	}
	return false
}

func isValidPlatform(p string) bool {
	switch p {
	case domain.PlatformZerodha, domain.PlatformGroww, domain.PlatformINDMoney, domain.PlatformManual:
		return true
	}
	return false
}

func assetTypeFromSegment(seg string) string {
	switch strings.ToLower(strings.TrimSpace(seg)) {
	case "mf", "mutual fund", "mutualfund":
		return domain.AssetTypeMF
	case "etf":
		return domain.AssetTypeETF
	case "us equity", "us stock", "usequity":
		return domain.AssetTypeStock
	case "equity", "stock", "shares":
		return domain.AssetTypeStock
	case "gold":
		return domain.AssetTypeMetal
	case "bond":
		return domain.AssetTypeBond
	case "":
		return domain.AssetTypeStock
	}
	return domain.AssetTypeOther
}
