package services

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hemanthakumar97/wealthfolio/internal/domain"
	"github.com/hemanthakumar97/wealthfolio/internal/parsers"
)

// GmailConfig holds runtime settings for the Gmail watcher.
// The OAuth2 refresh token is stored in app_settings (key: gmail_refresh_token),
// set via the Settings → Integrations OAuth flow — not from env vars.
type GmailConfig struct {
	LookbackDays int
	ZerodhaPDFPassword string // Password for Zerodha contract note PDFs
}

// GmailWatcher polls Gmail for Groww MF allotment and Zerodha contract note
// emails, and auto-imports the extracted transactions.
type GmailWatcher struct {
	pool          *pgxpool.Pool
	importSvc     *ImportService
	cfg           GmailConfig
	growwParser   *parsers.GrowwEmailParser
	zerodhaParser *parsers.ZerodhaContractNoteParser
	indmoneyParser *parsers.IndMoneyEmailParser
}

func NewGmailWatcher(pool *pgxpool.Pool, importSvc *ImportService, cfg GmailConfig) *GmailWatcher {
	return &GmailWatcher{
		pool:           pool,
		importSvc:      importSvc,
		cfg:            cfg,
		growwParser:    &parsers.GrowwEmailParser{},
		zerodhaParser:  &parsers.ZerodhaContractNoteParser{},
		indmoneyParser: &parsers.IndMoneyEmailParser{},
	}
}

// EmailWatchRule mirrors the email_watch_rules DB row.
type EmailWatchRule struct {
	ID           int64
	Name         string
	Platform     string
	FromEmail    string
	SubjectQuery string
	ParserType   string
	Enabled      bool
}

// Run is the entry point called by the cron scheduler.
// It reads enabled rules from the DB and processes each one.
func (w *GmailWatcher) Run(ctx context.Context) error {
	slog.Info("gmail watcher: starting run")

	svc, err := w.gmailClient(ctx)
	if err != nil {
		return fmt.Errorf("gmail auth: %w", err)
	}

	rules, err := w.loadRules(ctx)
	if err != nil {
		return fmt.Errorf("load rules: %w", err)
	}
	if len(rules) == 0 {
		slog.Info("gmail watcher: no enabled rules")
		return nil
	}

	totalProcessed, totalSkipped, totalErrors := 0, 0, 0
	for _, rule := range rules {
		query := w.buildRuleQuery(ctx, rule)
		p, sk, er := w.runQuery(ctx, svc, query, func(ctx context.Context, svc *gmail.Service, msgID string) error {
			return w.processWithRule(ctx, svc, msgID, rule)
		})
		totalProcessed += p; totalSkipped += sk; totalErrors += er
	}

	slog.Info("gmail watcher: run complete",
		"processed", totalProcessed, "skipped", totalSkipped, "errors", totalErrors)
	return nil
}

func (w *GmailWatcher) runQuery(
	ctx context.Context,
	svc *gmail.Service,
	query string,
	handler func(context.Context, *gmail.Service, string) error,
) (processed, skipped, errors int) {
	resp, err := svc.Users.Messages.List("me").Q(query).MaxResults(50).Context(ctx).Do()
	if err != nil {
		slog.Error("gmail watcher: list failed", "query", query, "err", err)
		errors++
		return
	}
	for _, msg := range resp.Messages {
		already, err := w.isProcessed(ctx, msg.Id)
		if err != nil {
			slog.Warn("gmail watcher: isProcessed failed", "msg_id", msg.Id, "err", err)
			continue
		}
		if already {
			skipped++
			continue
		}
		if err := handler(ctx, svc, msg.Id); err != nil {
			slog.Warn("gmail watcher: handler failed", "msg_id", msg.Id, "err", err)
			errors++
			continue
		}
		processed++
	}
	return
}

// gmailClient builds an authenticated *gmail.Service using the refresh token
// stored in app_settings (key: gmail_refresh_token), set via the OAuth UI.
func (w *GmailWatcher) gmailClient(ctx context.Context) (*gmail.Service, error) {
	var refreshToken string
	_ = w.pool.QueryRow(ctx,
		`SELECT value FROM app_settings WHERE key = 'gmail_refresh_token'`,
	).Scan(&refreshToken)
	if refreshToken == "" {
		return nil, fmt.Errorf("no Gmail refresh token configured — connect via Settings → Integrations")
	}

	var clientID, clientSecret string
	_ = w.pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key = 'gmail_client_id'`).Scan(&clientID)
	_ = w.pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key = 'gmail_client_secret'`).Scan(&clientSecret)

	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("Gmail Client ID or Client Secret not configured — configure them in Settings → Integrations")
	}

	cfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     google.Endpoint,
		Scopes:       []string{gmail.GmailReadonlyScope},
	}
	tok := &oauth2.Token{RefreshToken: refreshToken}
	ts := cfg.TokenSource(ctx, tok)
	svc, err := gmail.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, err
	}
	return svc, nil
}

// --- Dry-run / test ---------------------------------------------------------

// DryRunResult is returned by DryRun, showing what would be imported per rule.
type DryRunResult struct {
	RuleID   int64         `json:"rule_id"`
	RuleName string        `json:"rule_name"`
	Parser   string        `json:"parser_type"`
	Query    string        `json:"query"`
	Emails   []DryRunEmail `json:"emails"`
}

type DryRunEmail struct {
	MessageID       string              `json:"message_id"`
	Subject         string              `json:"subject"`
	ReceivedAt      string              `json:"received_at"`
	AlreadyImported bool                `json:"already_imported"`
	Transactions    []DryRunTransaction `json:"transactions"`
	ParseErrors     []string            `json:"parse_errors,omitempty"`
}

type DryRunTransaction struct {
	Instrument string `json:"instrument"`
	Type       string `json:"type"`
	Quantity   string `json:"quantity"`
	Price      string `json:"price"`
	Amount     string `json:"amount"`
	Date       string `json:"date"`
	OrderID    string `json:"order_id"`
}

// DryRunResult is returned by DryRun, showing what would be imported per rule.
// Query field shows the exact Gmail search string used.
type dryRunMeta struct {
	Query string `json:"query"`
}

// DryRun fetches and parses emails for all enabled rules without writing anything to the DB.
func (w *GmailWatcher) DryRun(ctx context.Context) ([]DryRunResult, error) {
	svc, err := w.gmailClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("gmail auth: %w", err)
	}

	rules, err := w.loadRules(ctx)
	if err != nil {
		return nil, fmt.Errorf("load rules: %w", err)
	}

	var results []DryRunResult
	for _, rule := range rules {
		query := w.buildRuleQuery(ctx, rule)
		res := DryRunResult{
			RuleID:   rule.ID,
			RuleName: rule.Name,
			Parser:   rule.ParserType,
			Query:    query,
		}

		resp, err := svc.Users.Messages.List("me").Q(query).MaxResults(10).Context(ctx).Do()
		if err != nil {
			slog.Warn("dry-run: list failed", "rule", rule.Name, "err", err)
			results = append(results, res)
			continue
		}

		for _, m := range resp.Messages {
			already, _ := w.isProcessed(ctx, m.Id)

			msg, err := svc.Users.Messages.Get("me", m.Id).Format("full").Context(ctx).Do()
			if err != nil {
				continue
			}
			subject := headerValue(msg.Payload.Headers, "Subject")
			receivedAt := time.Unix(msg.InternalDate/1000, 0)

			email := DryRunEmail{
				MessageID:       m.Id,
				Subject:         subject,
				ReceivedAt:      receivedAt.Format("2006-01-02 15:04"),
				AlreadyImported: already,
			}

			// Parse without importing.
			var txs []parsers.NormalizedTransaction
			var parseErrs []error

			switch rule.ParserType {
			case "groww_mf":
				html := extractHTMLPart(msg.Payload)
				txs, parseErrs = w.growwParser.ParseEmail(html, m.Id, subject, receivedAt)

			case "zerodha_contract_note":
				tradeDate, _ := parsers.ParseSubjectDate(subject)
				if tradeDate.IsZero() {
					tradeDate = receivedAt.UTC().Truncate(24 * time.Hour)
				}
				pdfBytes, pdfName := findPDFAttachment(msg.Payload, svc, m.Id)
				if pdfBytes == nil {
					parseErrs = []error{fmt.Errorf("no PDF attachment found in email")}
				} else {
					slog.Debug("dry-run: found PDF", "name", pdfName, "bytes", len(pdfBytes))
					text, err := ExtractPDFText(pdfBytes, w.cfg.ZerodhaPDFPassword)
					if err != nil {
						parseErrs = []error{fmt.Errorf("PDF decrypt/extract failed (check ZERODHA_PDF_PASSWORD): %w", err)}
					} else if strings.TrimSpace(text) == "" {
						parseErrs = []error{fmt.Errorf("PDF decrypted but no text extracted (file: %s, size: %d bytes)", pdfName, len(pdfBytes))}
					} else {
						txs, parseErrs = w.zerodhaParser.ParseContractNote(text, m.Id, subject, tradeDate)
						if len(txs) == 0 && len(parseErrs) == 0 {
							// Show first 500 chars of extracted text to help debug regex
							preview := text
							if len(preview) > 500 {
								preview = preview[:500] + "…"
							}
							parseErrs = []error{fmt.Errorf("PDF text extracted but no trade rows matched. Text preview:\n%s", preview)}
						}
					}
				}

			case "indmoney_us":
				html := extractHTMLPart(msg.Payload)
				txs, parseErrs = w.indmoneyParser.ParseEmail(html, m.Id, subject, receivedAt)
			}

			for _, pe := range parseErrs {
				email.ParseErrors = append(email.ParseErrors, pe.Error())
			}
			for _, tx := range txs {
				email.Transactions = append(email.Transactions, DryRunTransaction{
					Instrument: tx.InstrumentName,
					Type:       tx.TransactionType,
					Quantity:   tx.Quantity.String(),
					Price:      tx.Price.Round(4).String(),
					Amount:     tx.Amount.Round(2).String(),
					Date:       tx.TransactionDate.Format("2006-01-02"),
					OrderID:    tx.OrderID,
				})
			}
			res.Emails = append(res.Emails, email)
		}
		results = append(results, res)
	}
	return results, nil
}

// loadRules fetches all enabled email watch rules from the DB.
func (w *GmailWatcher) loadRules(ctx context.Context) ([]EmailWatchRule, error) {
	rows, err := w.pool.Query(ctx,
		`SELECT id, name, platform, from_email, subject_query, parser_type
		   FROM email_watch_rules WHERE enabled = TRUE ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rules []EmailWatchRule
	for rows.Next() {
		var r EmailWatchRule
		if err := rows.Scan(&r.ID, &r.Name, &r.Platform, &r.FromEmail, &r.SubjectQuery, &r.ParserType); err != nil {
			return nil, err
		}
		r.Enabled = true
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

// buildRuleQuery builds the Gmail search query for a rule.
func (w *GmailWatcher) buildRuleQuery(ctx context.Context, rule EmailWatchRule) string {
	days := w.cfg.LookbackDays
	var stored string
	_ = w.pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key = 'gmail_lookback_days'`).Scan(&stored)
	if n, err := strconv.Atoi(stored); err == nil && n > 0 {
		days = n
	}
	if days <= 0 {
		days = 7
	}
	after := time.Now().AddDate(0, 0, -days).Format("2006/01/02")
	return fmt.Sprintf("from:%s %s after:%s", rule.FromEmail, rule.SubjectQuery, after)
}

// processWithRule dispatches a single message to the correct parser based on rule.ParserType.
func (w *GmailWatcher) processWithRule(ctx context.Context, svc *gmail.Service, msgID string, rule EmailWatchRule) error {
	switch rule.ParserType {
	case "groww_mf":
		return w.processGrowwMessage(ctx, svc, msgID)
	case "zerodha_contract_note":
		return w.processZerodhaMessage(ctx, svc, msgID)
	case "indmoney_us":
		return w.processIndMoneyMessage(ctx, svc, msgID)
	default:
		return fmt.Errorf("unknown parser_type: %s", rule.ParserType)
	}
}

// processGrowwMessage fetches, parses, and imports a Groww allotment email.
func (w *GmailWatcher) processGrowwMessage(ctx context.Context, svc *gmail.Service, msgID string) error {
	msg, err := svc.Users.Messages.Get("me", msgID).
		Format("full").
		Context(ctx).
		Do()
	if err != nil {
		return fmt.Errorf("fetch message: %w", err)
	}

	subject := headerValue(msg.Payload.Headers, "Subject")
	sender := headerValue(msg.Payload.Headers, "From")
	receivedAt := time.Unix(msg.InternalDate/1000, 0)

	htmlBody := extractHTMLPart(msg.Payload)
	if htmlBody == "" {
		slog.Debug("gmail watcher: no HTML body", "msg_id", msgID, "subject", subject)
		return w.recordImport(ctx, msgID, msg.ThreadId, sender, subject, receivedAt, "SKIPPED", 0, "no HTML body")
	}

	txs, parseErrs := w.growwParser.ParseEmail(htmlBody, msgID, subject, receivedAt)
	if len(parseErrs) > 0 {
		for _, pe := range parseErrs {
			slog.Warn("gmail watcher: parse error", "msg_id", msgID, "err", pe)
		}
	}

	if len(txs) == 0 {
		slog.Info("gmail watcher: email not parseable, skipping", "msg_id", msgID, "subject", subject)
		return w.recordImport(ctx, msgID, msg.ThreadId, sender, subject, receivedAt, "SKIPPED", 0, "no transactions extracted")
	}

	// Create an upload_history row for traceability.
	uploadID, err := w.importSvc.CreateUploadRow(ctx,
		fmt.Sprintf("gmail:%s", msgID),
		0,
		domain.PlatformGroww,
		0, // system import, no user
	)
	if err != nil {
		return fmt.Errorf("create upload row: %w", err)
	}

	result := w.importSvc.PersistRows(ctx, uploadID, txs, nil)

	status := domain.UploadCompleted
	if result.Imported == 0 && result.Errors > 0 {
		status = domain.UploadFailed
	} else if result.Errors > 0 || result.Duplicates > 0 {
		status = domain.UploadPartial
	}
	total := result.Imported + result.Duplicates + result.Errors
	w.importSvc.FinalizeUploadRow(ctx, uploadID, status, total, result.Imported, result.Duplicates, result.Errors, result.ErrorMsgs)

	slog.Info("gmail watcher: imported email",
		"msg_id", msgID, "subject", subject,
		"imported", result.Imported, "duplicates", result.Duplicates, "errors", result.Errors)

	recStatus := "OK"
	errMsg := ""
	if result.Errors > 0 && result.Imported == 0 {
		recStatus = "ERROR"
		errMsg = strings.Join(result.ErrorMsgs, "; ")
	}
	return w.recordImport(ctx, msgID, msg.ThreadId, sender, subject, receivedAt, recStatus, uploadID, errMsg)
}


// processZerodhaMessage downloads the PDF attachment from a Zerodha contract
// note email, decrypts it with the PAN, extracts text, and imports trades.
func (w *GmailWatcher) processZerodhaMessage(ctx context.Context, svc *gmail.Service, msgID string) error {
	msg, err := svc.Users.Messages.Get("me", msgID).Format("full").Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("fetch message: %w", err)
	}

	subject := headerValue(msg.Payload.Headers, "Subject")
	sender := headerValue(msg.Payload.Headers, "From")
	receivedAt := time.Unix(msg.InternalDate/1000, 0)

	// Parse trade date from email subject.
	tradeDate, err := parsers.ParseSubjectDate(subject)
	if err != nil {
		slog.Warn("gmail watcher: zerodha date parse failed", "subject", subject, "err", err)
		tradeDate = receivedAt.UTC().Truncate(24 * time.Hour)
	}

	// Find and download the PDF attachment.
	pdfBytes, pdfName := findPDFAttachment(msg.Payload, svc, msgID)
	if pdfBytes == nil {
		slog.Info("gmail watcher: no PDF attachment found", "msg_id", msgID, "subject", subject)
		return w.recordImport(ctx, msgID, msg.ThreadId, sender, subject, receivedAt, "SKIPPED", 0, "no PDF attachment")
	}
	slog.Info("gmail watcher: downloaded PDF", "msg_id", msgID, "filename", pdfName, "bytes", len(pdfBytes))

	// Decrypt and extract text.
	text, err := ExtractPDFText(pdfBytes, w.cfg.ZerodhaPDFPassword)
	if err != nil {
		slog.Warn("gmail watcher: PDF extract failed", "msg_id", msgID, "err", err)
		return w.recordImport(ctx, msgID, msg.ThreadId, sender, subject, receivedAt, "ERROR", 0, "pdf extract: "+err.Error())
	}

	txs, parseErrs := w.zerodhaParser.ParseContractNote(text, msgID, subject, tradeDate)
	for _, pe := range parseErrs {
		slog.Warn("gmail watcher: zerodha parse error", "msg_id", msgID, "err", pe)
	}
	if len(txs) == 0 {
		slog.Info("gmail watcher: no trades found in contract note", "msg_id", msgID, "subject", subject)
		return w.recordImport(ctx, msgID, msg.ThreadId, sender, subject, receivedAt, "SKIPPED", 0, "no trades extracted")
	}

	uploadID, err := w.importSvc.CreateUploadRow(ctx,
		fmt.Sprintf("zerodha-contract-note:%s", msgID),
		int64(len(pdfBytes)),
		domain.PlatformZerodha,
		0,
	)
	if err != nil {
		return fmt.Errorf("create upload row: %w", err)
	}

	result := w.importSvc.PersistRows(ctx, uploadID, txs, nil)
	status := domain.UploadCompleted
	if result.Imported == 0 && result.Errors > 0 {
		status = domain.UploadFailed
	} else if result.Errors > 0 || result.Duplicates > 0 {
		status = domain.UploadPartial
	}
	total := result.Imported + result.Duplicates + result.Errors
	w.importSvc.FinalizeUploadRow(ctx, uploadID, status, total, result.Imported, result.Duplicates, result.Errors, result.ErrorMsgs)

	slog.Info("gmail watcher: zerodha contract note imported",
		"msg_id", msgID, "subject", subject, "trades", len(txs),
		"imported", result.Imported, "duplicates", result.Duplicates)

	recStatus := "OK"
	errMsg := ""
	if result.Errors > 0 && result.Imported == 0 {
		recStatus = "ERROR"
		errMsg = strings.Join(result.ErrorMsgs, "; ")
	}
	return w.recordImport(ctx, msgID, msg.ThreadId, sender, subject, receivedAt, recStatus, uploadID, errMsg)
}

// findPDFAttachment recursively searches a message payload for a PDF attachment
// and downloads it. Returns nil if none found.
func findPDFAttachment(part *gmail.MessagePart, svc *gmail.Service, msgID string) ([]byte, string) {
	if part == nil {
		return nil, ""
	}
	if strings.HasPrefix(part.MimeType, "application/pdf") ||
		(part.Filename != "" && strings.ToLower(part.Filename[max(0, len(part.Filename)-4):]) == ".pdf") {
		if part.Body != nil {
			if part.Body.Data != "" {
				b, err := decodeGmailBody(part.Body.Data)
				if err == nil {
					return []byte(b), part.Filename
				}
			}
			if part.Body.AttachmentId != "" {
				att, err := svc.Users.Messages.Attachments.Get("me", msgID, part.Body.AttachmentId).Do()
				if err == nil && att.Data != "" {
					b, err := base64.URLEncoding.DecodeString(att.Data)
					if err == nil {
						return b, part.Filename
					}
				}
			}
		}
	}
	for _, sub := range part.Parts {
		if b, name := findPDFAttachment(sub, svc, msgID); b != nil {
			return b, name
		}
	}
	return nil, ""
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}


// processIndMoneyMessage fetches, parses, and imports an IndMoney US stock SIP email.
func (w *GmailWatcher) processIndMoneyMessage(ctx context.Context, svc *gmail.Service, msgID string) error {
	msg, err := svc.Users.Messages.Get("me", msgID).Format("full").Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("fetch message: %w", err)
	}

	subject := headerValue(msg.Payload.Headers, "Subject")
	sender := headerValue(msg.Payload.Headers, "From")
	receivedAt := time.Unix(msg.InternalDate/1000, 0)

	htmlBody := extractHTMLPart(msg.Payload)
	if htmlBody == "" {
		return w.recordImport(ctx, msgID, msg.ThreadId, sender, subject, receivedAt, "SKIPPED", 0, "no HTML body")
	}

	txs, parseErrs := w.indmoneyParser.ParseEmail(htmlBody, msgID, subject, receivedAt)
	for _, pe := range parseErrs {
		slog.Warn("gmail watcher: indmoney parse error", "msg_id", msgID, "err", pe)
	}
	if len(txs) == 0 {
		return w.recordImport(ctx, msgID, msg.ThreadId, sender, subject, receivedAt, "SKIPPED", 0, "no transactions extracted")
	}

	uploadID, err := w.importSvc.CreateUploadRow(ctx,
		fmt.Sprintf("indmoney-sip:%s", msgID),
		0,
		domain.PlatformINDMoney,
		0,
	)
	if err != nil {
		return fmt.Errorf("create upload row: %w", err)
	}

	result := w.importSvc.PersistRows(ctx, uploadID, txs, nil)
	status := domain.UploadCompleted
	if result.Imported == 0 && result.Errors > 0 {
		status = domain.UploadFailed
	} else if result.Errors > 0 || result.Duplicates > 0 {
		status = domain.UploadPartial
	}
	total := result.Imported + result.Duplicates + result.Errors
	w.importSvc.FinalizeUploadRow(ctx, uploadID, status, total, result.Imported, result.Duplicates, result.Errors, result.ErrorMsgs)

	slog.Info("gmail watcher: indmoney SIP imported",
		"msg_id", msgID, "subject", subject,
		"imported", result.Imported, "duplicates", result.Duplicates)

	recStatus := "OK"
	errMsg := ""
	if result.Errors > 0 && result.Imported == 0 {
		recStatus = "ERROR"
		errMsg = strings.Join(result.ErrorMsgs, "; ")
	}
	return w.recordImport(ctx, msgID, msg.ThreadId, sender, subject, receivedAt, recStatus, uploadID, errMsg)
}

// isProcessed returns true if the message_id is already in email_imports.
func (w *GmailWatcher) isProcessed(ctx context.Context, messageID string) (bool, error) {
	var exists bool
	err := w.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM email_imports WHERE message_id = $1)`,
		messageID,
	).Scan(&exists)
	return exists, err
}

// recordImport inserts a row into email_imports.
func (w *GmailWatcher) recordImport(
	ctx context.Context,
	messageID, threadID, sender, subject string,
	receivedAt time.Time,
	status string,
	uploadID int64,
	errMsg string,
) error {
	var uploadIDArg any = uploadID
	if uploadID == 0 {
		uploadIDArg = nil
	}
	var errMsgArg any
	if errMsg != "" {
		errMsgArg = errMsg
	}
	_, err := w.pool.Exec(ctx,
		`INSERT INTO email_imports
		   (message_id, thread_id, sender, subject, received_at, status, upload_id, error_message)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		 ON CONFLICT (message_id) DO NOTHING`,
		messageID, threadID, sender, subject, receivedAt, status, uploadIDArg, errMsgArg,
	)
	return err
}

// extractHTMLPart recursively walks a Gmail MessagePart tree and returns the
// decoded text/html body, or "" if not found.
func extractHTMLPart(part *gmail.MessagePart) string {
	if part == nil {
		return ""
	}
	if part.MimeType == "text/html" && part.Body != nil && part.Body.Data != "" {
		body, err := decodeGmailBody(part.Body.Data)
		if err == nil {
			return body
		}
	}
	for _, sub := range part.Parts {
		if s := extractHTMLPart(sub); s != "" {
			return s
		}
	}
	return ""
}

// decodeGmailBody base64url-decodes a Gmail message body.
func decodeGmailBody(data string) (string, error) {
	b, err := base64.URLEncoding.DecodeString(data)
	if err != nil {
		// Some messages use standard base64.
		b, err = base64.StdEncoding.DecodeString(data)
		if err != nil {
			return "", err
		}
	}
	return string(b), nil
}

// headerValue returns the value of a named header from a Gmail message.
func headerValue(headers []*gmail.MessagePartHeader, name string) string {
	for _, h := range headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}
