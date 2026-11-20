package main

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type inventoryRow struct {
	ProductID   string
	ProductName string
	Zone        string
	Quantity    int
	Date        time.Time
	Row         *int
	Shelf       *int
}

const maxReportedErrors = 100

var errInvalidHeader = errors.New("invalid csv header")
var requiredCols = []string{"product_id", "product_name", "quantity", "zone", "date"}

func parseHeader(header []string) (map[string]int, []string, error) {
	idx := make(map[string]int, len(header))
	normHeader := []string{}
	for i, name := range header {
		name = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(name, "\ufeff")))
		if name != "" {
			if _, ok := idx[name]; ok {
				return nil, nil, fmt.Errorf("duplicate column %q", name)
			}
			idx[name] = i
		}

		normHeader = append(normHeader, name)
	}

	for _, col := range requiredCols {
		if _, ok := idx[col]; !ok {
			return nil, nil, fmt.Errorf("missing required column %q", col)
		}
	}

	return idx, normHeader, nil
}

func parseRow(row []string, header []string, idx map[string]int, index int) (inventoryRow, error) {
	if len(row) != len(header) {
		return inventoryRow{}, fmt.Errorf("line %d: count values in row must be equal column count: expected %d, got %d", index, len(header), len(row))
	}

	get := func(s string) string { return strings.TrimSpace(row[idx[s]]) }

	var err error
	var invRow inventoryRow
	for _, col := range header {
		if col == "" {
			continue
		}
		v := get(col)
		if v == "" && slices.Contains(requiredCols, col) {
			return inventoryRow{}, fmt.Errorf("line %d: column '%s' mustn't be empty", index, col)
		}

		switch col {
		case "product_id":
			invRow.ProductID = v
		case "product_name":
			invRow.ProductName = v
		case "zone":
			invRow.Zone = v
		case "quantity":
			invRow.Quantity, err = strconv.Atoi(v)
			if err != nil {
				return inventoryRow{}, fmt.Errorf("line %d: quantity must be integer", index)
			}
			if invRow.Quantity < 0 {
				return inventoryRow{}, fmt.Errorf("line %d: quantity must be >= 0", index)
			}
		case "date":
			invRow.Date, err = time.Parse("2006-01-02", v)
			if err != nil {
				return inventoryRow{}, fmt.Errorf("line %d: invalid date format", index)
			}
		case "row":
			if v == "" {
				return inventoryRow{}, fmt.Errorf("line %d: row must not be empty when the column is present", index)
			}
			t, err := strconv.Atoi(v)
			if err != nil {
				return inventoryRow{}, fmt.Errorf("line %d: row must be integer", index)
			}
			if t < 0 {
				return inventoryRow{}, fmt.Errorf("line %d: row must be >= 0", index)
			}
			invRow.Row = &t
		case "shelf":
			if v == "" {
				return inventoryRow{}, fmt.Errorf("line %d: shelf must not be empty when the column is present", index)
			}
			t, err := strconv.Atoi(v)
			if err != nil {
				return inventoryRow{}, fmt.Errorf("line %d: shelf must be integer", index)
			}
			if t < 0 {
				return inventoryRow{}, fmt.Errorf("line %d: shelf must be >= 0", index)
			}
			invRow.Shelf = &t
		}
	}

	return invRow, nil
}

type importResult struct {
	Valid  []inventoryRow
	Failed int
	Errs   []string
}

func processCSV(reader *csv.Reader) (importResult, error) {
	res := importResult{}
	header, err := reader.Read()
	if err == io.EOF {
		return res, err
	} else if err != nil {
		return res, fmt.Errorf("reading header: %w", err)
	}
	idx, newHeader, err := parseHeader(header)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errInvalidHeader, err)
	}
	var index int
	for {
		row, err := reader.Read()
		var pe *csv.ParseError
		if errors.As(err, &pe) {
			if errors.Is(pe.Err, csv.ErrBareQuote) {
				res.Failed++
				if len(res.Errs) < maxReportedErrors {
					res.Errs = append(res.Errs, fmt.Sprintf("line %d: quote \" inside an unquote field", pe.StartLine))
				}
				continue
			} else {
				return importResult{}, fmt.Errorf("line %d: %w", pe.StartLine, pe)
			}
		} else if err == io.EOF {
			break
		} else if err != nil {
			return importResult{}, fmt.Errorf("reading row: %w", err)
		}
		index, _ = reader.FieldPos(0)
		invRow, err := parseRow(row, newHeader, idx, index)
		if err != nil {
			res.Failed++
			if len(res.Errs) < maxReportedErrors {
				res.Errs = append(res.Errs, err.Error())
			}
		} else {
			res.Valid = append(res.Valid, invRow)
		}
	}

	return res, nil
}

type importResponse struct {
	Success         int      `json:"success"`
	Failed          int      `json:"failed"`
	Errors          []string `json:"errors"`
	ErrorsTruncated bool     `json:"errors_truncated"`
}

func importHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
		file, _, err := r.FormFile("file")
		if err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				http.Error(w, "file is too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "file is required", http.StatusBadRequest)
			return
		}
		defer file.Close()

		reader := csv.NewReader(file)
		reader.Comma = ';'
		reader.FieldsPerRecord = -1

		res, err := processCSV(reader)
		var pe *csv.ParseError
		if err != nil {
			switch {
			case errors.Is(err, errInvalidHeader):
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			case errors.As(err, &pe):
				http.Error(w, fmt.Sprintf("line %d: invalid csv format, check quotes. nothing was imported", pe.StartLine), http.StatusBadRequest)
				return
			case err == io.EOF:
				http.Error(w, "file is empty", http.StatusBadRequest)
				return
			default:
				fmt.Fprintf(os.Stderr, "processing csv file: %v\n", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
		}

		if len(res.Valid) > 0 {
			ctx := r.Context()
			tx, err := pool.Begin(r.Context())
			if err != nil {
				fmt.Fprintf(os.Stderr, "create transaction error: %v\n", err)
				http.Error(w, "db error", http.StatusInternalServerError)
				return
			}
			defer tx.Rollback(r.Context())

			for _, row := range res.Valid {
				_, err = tx.Exec(ctx, `INSERT INTO products (id, name) VALUES ($1, $2)
				ON CONFLICT (id) DO NOTHING`, row.ProductID, row.ProductName)

				if err != nil {
					fmt.Fprintf(os.Stderr, "import csv: product insert: %v\n", err)
					http.Error(w, "db error", http.StatusInternalServerError)
					return
				}

				_, err = tx.Exec(ctx, `
				INSERT INTO inventory_history 
				(product_id, quantity, row_number, shelf_number, zone, scanned_at) VALUES ($1, $2, $3, $4, $5, $6)`,
					row.ProductID, row.Quantity, row.Row, row.Shelf, row.Zone, row.Date)

				if err != nil {
					fmt.Fprintf(os.Stderr, "import csv: inventory_history insert: %v\n", err)
					http.Error(w, "db error", http.StatusInternalServerError)
					return
				}
			}

			if err = tx.Commit(r.Context()); err != nil {
				fmt.Fprintf(os.Stderr, "commit transaction error: %v\n", err)
				http.Error(w, "db error", http.StatusInternalServerError)
				return
			}
		}

		resp := importResponse{
			Success:         len(res.Valid),
			Failed:          res.Failed,
			ErrorsTruncated: len(res.Errs) < res.Failed,
		}

		if res.Errs != nil {
			resp.Errors = res.Errs
		} else {
			resp.Errors = []string{}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}
