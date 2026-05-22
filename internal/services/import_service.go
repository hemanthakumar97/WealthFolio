package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hemanthakumar97/wealthfolio/internal/domain"
	"github.com/hemanthakumar97/wealthfolio/internal/parsers"
)

// ImportResult summarises the outcome of a batch persist operation.
type ImportResult struct {
	Imported   int
	Duplicates int
	Errors     int
	ErrorMsgs  []string
}

// ImportService is the shared transaction-persistence layer used by both the
// HTTP upload handler and the Gmail email watcher.
type ImportService struct {
	pool        *pgxpool.Pool
	instruments *InstrumentService
	duplicates  *DuplicateDetector
}

func NewImportService(pool *pgxpool.Pool, instruments *InstrumentService, duplicates *DuplicateDetector) *ImportService {
	return &ImportService{pool: pool, instruments: instruments, duplicates: duplicates}
}

// CreateUploadRow inserts a row in upload_history and returns its ID.
// Pass uploadedBy=0 for automated/system imports.
func (s *ImportService) CreateUploadRow(ctx context.Context, filename string, fileSize int64, platform string, uploadedBy int64) (int64, error) {
	var id int64
	var uploadedByArg any = uploadedBy
	if uploadedBy == 0 {
		uploadedByArg = nil
	}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO upload_history (filename, file_size, platform, status, uploaded_by)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		filename, fileSize, platform, domain.UploadProcessing, uploadedByArg,
	).Scan(&id)
	return id, err
}

// FinalizeUploadRow stamps upload_history with final counts and status.
func (s *ImportService) FinalizeUploadRow(ctx context.Context, id int64, status string,
	total, imported, duplicates, errs int, errorMsgs []string) {
	errJSON, _ := json.Marshal(errorMsgs)
	if _, err := s.pool.Exec(ctx,
		`UPDATE upload_history
		    SET status = $2, records_total = $3, records_imported = $4,
		        records_duplicates = $5, records_errors = $6,
		        error_details = $7, processed_at = NOW()
		  WHERE id = $1`,
		id, status, total, imported, duplicates, errs, errJSON,
	); err != nil {
		slog.Error("finalize upload", "err", err, "upload_id", id)
	}
}

// PersistRows is the core import loop: find/create instrument, dedup check, INSERT transaction.
// It is equivalent to the private persistRows method previously on TransactionsHandler.
func (s *ImportService) PersistRows(
	ctx context.Context,
	uploadID int64,
	parsed []parsers.NormalizedTransaction,
	parseErrors []parsers.ParseError,
) ImportResult {
	var result ImportResult

	for _, pe := range parseErrors {
		result.Errors++
		msg := pe.Error()
		result.ErrorMsgs = append(result.ErrorMsgs, msg)
		s.writeLog(ctx, uploadID, pe.Row, domain.ImportError, msg)
	}

	for _, t := range parsed {
		instrID, _, err := s.instruments.FindOrCreate(ctx, InstrumentSpec{
			Name:      t.InstrumentName,
			ISIN:      t.ISIN,
			AMFICode:  t.AMFICode,
			AssetType: t.AssetType,
			Currency:  t.Currency,
			Exchange:  t.Exchange,
		})
		if err != nil {
			result.Errors++
			msg := fmt.Sprintf("instrument %q: %v", t.InstrumentName, err)
			result.ErrorMsgs = append(result.ErrorMsgs, msg)
			s.writeLog(ctx, uploadID, t.RowNumber, domain.ImportError, msg)
			continue
		}

		dup, _, err := s.duplicates.IsDuplicate(ctx, DupCheck{
			InstrumentID:    instrID,
			TransactionDate: t.TransactionDate,
			TransactionType: t.TransactionType,
			Quantity:        t.Quantity,
			Price:           t.Price,
			Platform:        t.Platform,
			TradeID:         t.TradeID,
			OrderID:         t.OrderID,
		})
		if err != nil {
			result.Errors++
			msg := fmt.Sprintf("dup check %q: %v", t.InstrumentName, err)
			result.ErrorMsgs = append(result.ErrorMsgs, msg)
			s.writeLog(ctx, uploadID, t.RowNumber, domain.ImportError, msg)
			continue
		}
		if dup {
			result.Duplicates++
			s.writeLog(ctx, uploadID, t.RowNumber, domain.ImportDuplicate, "")
			continue
		}

		origJSON, _ := json.Marshal(t.OriginalRow)
		_, err = s.pool.Exec(ctx,
			`INSERT INTO transactions
			   (instrument_id, transaction_date, transaction_type, quantity, price, amount,
			    platform, upload_id, order_id, trade_id, original_data)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			instrID, t.TransactionDate, t.TransactionType, t.Quantity, t.Price, t.Amount,
			t.Platform, uploadID, nullableStr(t.OrderID), nullableStr(t.TradeID), origJSON,
		)
		if err != nil {
			result.Errors++
			msg := fmt.Sprintf("insert %q: %v", t.InstrumentName, err)
			result.ErrorMsgs = append(result.ErrorMsgs, msg)
			s.writeLog(ctx, uploadID, t.RowNumber, domain.ImportError, msg)
			continue
		}
		result.Imported++
		s.writeLog(ctx, uploadID, t.RowNumber, domain.ImportImported, "")
	}
	return result
}

func (s *ImportService) writeLog(ctx context.Context, uploadID int64, row int, status, msg string) {
	var rowArg any = row
	if row == 0 {
		rowArg = nil
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO import_logs (upload_id, row_number, status, message) VALUES ($1,$2,$3,$4)`,
		uploadID, rowArg, status, nullableStr(msg),
	); err != nil {
		slog.Error("write import log", "err", err)
	}
}

func nullableStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
