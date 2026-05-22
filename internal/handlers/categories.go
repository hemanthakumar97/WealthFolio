package handlers

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CategoriesHandler struct {
	pool *pgxpool.Pool
}

func NewCategoriesHandler(pool *pgxpool.Pool) *CategoriesHandler {
	return &CategoriesHandler{pool: pool}
}

// --- response types ---

type categoryResponse struct {
	ID              int64     `json:"id"`
	Name            string    `json:"name"`
	Description     *string   `json:"description"`
	Color           *string   `json:"color"`
	InstrumentCount int       `json:"instrument_count"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type categoryInstrumentResponse struct {
	InstrumentID   int64   `json:"instrument_id"`
	InstrumentName string  `json:"instrument_name"`
	ISIN           *string `json:"isin"`
	AssetType      string  `json:"asset_type"`
	Weight         float64 `json:"weight"`
}

// --- input types ---

type categoryInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Color       string `json:"color"`
}

type instrumentCategoryMapping struct {
	InstrumentID int64   `json:"instrument_id"`
	Weight       float64 `json:"weight"`
}

// --- handlers ---

func (h *CategoriesHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.pool.Query(r.Context(),
		`SELECT c.id, c.name, c.description, c.color,
		        (SELECT COUNT(*) FROM instrument_categories ic WHERE ic.category_id = c.id) AS instrument_count,
		        c.created_at, c.updated_at
		   FROM categories c
		  ORDER BY c.name ASC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []categoryResponse{}
	for rows.Next() {
		var c categoryResponse
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.Color,
			&c.InstrumentCount, &c.CreatedAt, &c.UpdatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, c)
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *CategoriesHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req categoryInput
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	var c categoryResponse
	err := h.pool.QueryRow(r.Context(),
		`INSERT INTO categories (name, description, color)
		 VALUES ($1, $2, $3)
		 RETURNING id, name, description, color, 0, created_at, updated_at`,
		strings.TrimSpace(req.Name),
		nullableString(req.Description),
		nullableString(req.Color),
	).Scan(&c.ID, &c.Name, &c.Description, &c.Color,
		&c.InstrumentCount, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "unique") {
			writeError(w, http.StatusConflict, "category name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (h *CategoriesHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var c categoryResponse
	err = h.pool.QueryRow(r.Context(),
		`SELECT c.id, c.name, c.description, c.color,
		        (SELECT COUNT(*) FROM instrument_categories ic WHERE ic.category_id = c.id),
		        c.created_at, c.updated_at
		   FROM categories c WHERE c.id = $1`, id,
	).Scan(&c.ID, &c.Name, &c.Description, &c.Color,
		&c.InstrumentCount, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (h *CategoriesHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req categoryInput
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	sets := []string{}
	args := []any{}
	add := func(col string, val any) {
		args = append(args, val)
		sets = append(sets, col+" = $"+itoa(len(args)))
	}
	if strings.TrimSpace(req.Name) != "" {
		add("name", strings.TrimSpace(req.Name))
	}
	// Allow clearing description/color by sending empty string → NULL
	add("description", nullableString(req.Description))
	add("color", nullableString(req.Color))

	if len(sets) == 0 {
		writeError(w, http.StatusBadRequest, "nothing to update")
		return
	}
	sets = append(sets, "updated_at = NOW()")
	args = append(args, id)

	sql := "UPDATE categories SET " + strings.Join(sets, ", ") +
		" WHERE id = $" + itoa(len(args)) +
		" RETURNING id, name, description, color, 0, created_at, updated_at"

	var c categoryResponse
	err = h.pool.QueryRow(r.Context(), sql, args...).Scan(
		&c.ID, &c.Name, &c.Description, &c.Color,
		&c.InstrumentCount, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Re-fetch instrument_count after update.
	_ = h.pool.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM instrument_categories WHERE category_id = $1`, id,
	).Scan(&c.InstrumentCount)
	writeJSON(w, http.StatusOK, c)
}

func (h *CategoriesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	tag, err := h.pool.Exec(r.Context(), `DELETE FROM categories WHERE id = $1`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *CategoriesHandler) ListInstruments(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	rows, err := h.pool.Query(r.Context(),
		`SELECT ic.instrument_id, i.name, i.isin, i.asset_type, ic.weight::float
		   FROM instrument_categories ic
		   JOIN instruments i ON i.id = ic.instrument_id
		  WHERE ic.category_id = $1
		  ORDER BY i.name ASC`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []categoryInstrumentResponse{}
	for rows.Next() {
		var ci categoryInstrumentResponse
		if err := rows.Scan(&ci.InstrumentID, &ci.InstrumentName,
			&ci.ISIN, &ci.AssetType, &ci.Weight); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, ci)
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *CategoriesHandler) AddInstrument(w http.ResponseWriter, r *http.Request) {
	categoryID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req instrumentCategoryMapping
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.InstrumentID == 0 {
		writeError(w, http.StatusBadRequest, "instrument_id required")
		return
	}
	weight := req.Weight
	if weight <= 0 {
		weight = 1.0
	}

	_, err = h.pool.Exec(r.Context(),
		`INSERT INTO instrument_categories (instrument_id, category_id, weight)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (instrument_id, category_id) DO UPDATE SET weight = EXCLUDED.weight`,
		req.InstrumentID, categoryID, weight)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"instrument_id": req.InstrumentID,
		"category_id":   categoryID,
		"weight":        weight,
	})
}

func (h *CategoriesHandler) RemoveInstrument(w http.ResponseWriter, r *http.Request) {
	categoryID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	instrID, err := strconv.ParseInt(chi.URLParam(r, "instr_id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid instrument id")
		return
	}
	tag, err := h.pool.Exec(r.Context(),
		`DELETE FROM instrument_categories WHERE category_id = $1 AND instrument_id = $2`,
		categoryID, instrID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
