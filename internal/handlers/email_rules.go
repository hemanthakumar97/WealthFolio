package handlers

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type EmailRulesHandler struct {
	pool *pgxpool.Pool
}

func NewEmailRulesHandler(pool *pgxpool.Pool) *EmailRulesHandler {
	return &EmailRulesHandler{pool: pool}
}

type emailWatchRule struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Platform     string `json:"platform"`
	FromEmail    string `json:"from_email"`
	SubjectQuery string `json:"subject_query"`
	ParserType   string `json:"parser_type"`
	Enabled      bool   `json:"enabled"`
	CreatedAt    string `json:"created_at"`
}

func (h *EmailRulesHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.pool.Query(r.Context(),
		`SELECT id, name, platform, from_email, subject_query, parser_type, enabled, created_at::text
		   FROM email_watch_rules ORDER BY id`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []emailWatchRule{}
	for rows.Next() {
		var rule emailWatchRule
		if err := rows.Scan(&rule.ID, &rule.Name, &rule.Platform, &rule.FromEmail,
			&rule.SubjectQuery, &rule.ParserType, &rule.Enabled, &rule.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, rule)
	}
	writeJSON(w, http.StatusOK, out)
}

type createRuleRequest struct {
	Name         string `json:"name"`
	Platform     string `json:"platform"`
	FromEmail    string `json:"from_email"`
	SubjectQuery string `json:"subject_query"`
	ParserType   string `json:"parser_type"`
}

func (h *EmailRulesHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createRuleRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Name == "" || req.FromEmail == "" || req.ParserType == "" {
		writeError(w, http.StatusBadRequest, "name, from_email, and parser_type are required")
		return
	}
	if !isValidParserType(req.ParserType) {
		writeError(w, http.StatusBadRequest, "parser_type must be one of: groww_mf, zerodha_contract_note, indmoney_us")
		return
	}

	var rule emailWatchRule
	err := h.pool.QueryRow(r.Context(),
		`INSERT INTO email_watch_rules (name, platform, from_email, subject_query, parser_type)
		 VALUES ($1,$2,$3,$4,$5)
		 RETURNING id, name, platform, from_email, subject_query, parser_type, enabled, created_at::text`,
		req.Name, req.Platform, req.FromEmail, req.SubjectQuery, req.ParserType,
	).Scan(&rule.ID, &rule.Name, &rule.Platform, &rule.FromEmail,
		&rule.SubjectQuery, &rule.ParserType, &rule.Enabled, &rule.CreatedAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

type updateRuleRequest struct {
	Enabled *bool  `json:"enabled"`
	Name    string `json:"name"`
}

func (h *EmailRulesHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req updateRuleRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	var rule emailWatchRule
	var scanErr error
	if req.Enabled != nil {
		scanErr = h.pool.QueryRow(r.Context(),
			`UPDATE email_watch_rules SET enabled=$1, updated_at=NOW() WHERE id=$2
			 RETURNING id, name, platform, from_email, subject_query, parser_type, enabled, created_at::text`,
			*req.Enabled, id,
		).Scan(&rule.ID, &rule.Name, &rule.Platform, &rule.FromEmail,
			&rule.SubjectQuery, &rule.ParserType, &rule.Enabled, &rule.CreatedAt)
	} else if req.Name != "" {
		scanErr = h.pool.QueryRow(r.Context(),
			`UPDATE email_watch_rules SET name=$1, updated_at=NOW() WHERE id=$2
			 RETURNING id, name, platform, from_email, subject_query, parser_type, enabled, created_at::text`,
			req.Name, id,
		).Scan(&rule.ID, &rule.Name, &rule.Platform, &rule.FromEmail,
			&rule.SubjectQuery, &rule.ParserType, &rule.Enabled, &rule.CreatedAt)
	} else {
		writeError(w, http.StatusBadRequest, "nothing to update")
		return
	}
	if scanErr != nil {
		writeError(w, http.StatusInternalServerError, scanErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

func (h *EmailRulesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	tag, err := h.pool.Exec(r.Context(), `DELETE FROM email_watch_rules WHERE id=$1`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "rule not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func isValidParserType(t string) bool {
	switch t {
	case "groww_mf", "zerodha_contract_note", "indmoney_us":
		return true
	}
	return false
}
