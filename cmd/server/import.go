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

var (
	errEmptyFile     = errors.New("file is empty")
	errInvalidHeader = errors.New("invalid csv header")
)
var requiredCols = []string{"product_id", "product_name", "quantity", "zone", "date"}

func parseHeader(header []string) (map[string]int, []string, error) {
	idx := make(map[string]int, len(header))
	norm_header := []string{}
	for i, name := range header {
		name = strings.ToLower(strings.TrimPrefix(name, "\ufeff"))
		if _, ok := idx[name]; ok {
			return nil, nil, fmt.Errorf("duplicate column %q", name)
		}
		idx[name] = i
		norm_header = append(norm_header, name)
	}

	for _, col := range requiredCols {
		if _, ok := idx[col]; !ok {
			return nil, nil, fmt.Errorf("missing required column")
		}
	}

	return idx, norm_header, nil
}

func parseRow(row []string, header []string, idx map[string]int, index int) error {
	if len(row) != len(header) {
		return errors.New("count values in row must be equal column count")
	}

	get := func(s string) string { return row[idx[s]] }

	var err error
	var invRow inventoryRow
	for _, col := range header {
		v := get(col)
		if v == "" && slices.Contains(requiredCols, col) {
			return fmt.Errorf("error in line %d: column '%s' mustn't be empty", index, col)
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
				return errors.New("quantity must be integer")
			}
		case "date":
			invRow.Date, err = time.Parse("2006-01-02", v)
			if err != nil {
				return errors.New("invalid date format")
			}
		case "row":
			t, err := strconv.Atoi(v)
			if err != nil {
				return errors.New("row must be integer")
			}
			invRow.Row = &t
		case "shelf":
			t, err := strconv.Atoi(v)
			if err != nil {
				return errors.New("shelf must be integer")
			}
			invRow.Shelf = &t
		}
	}
	return nil
}

func processCSV(reader *csv.Reader) (Result, error) {
	index := 1
	res := Result{}
	header, err := reader.Read()
	if err == io.EOF {
		return res, err
	} else if err != nil {
		return res, fmt.Errorf("reading header: %w", err)
	}
	idx, new_header, err := parseHeader(header)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errInvalidHeader, err)
	}
	for {
		row, err := reader.Read()
		var errBQ *csv.ParseError
		if errors.As(err, &errBQ) && errBQ.Err == csv.ErrBareQuote {
			res.Failed++
			if len(res.Errors) <= maxReportedErrors {
				res.Errors = append(res.Errors, fmt.Sprintf("line %v: %v", errBQ.Line+1, errBQ.Error()))
			} else {
				res.ErrorsTruncated = true
			}
			continue
		} else if err == io.EOF {
			break
		} else if err != nil {
			return Result{}, fmt.Errorf("reading row: %w", err)
		}

		if err = parseRow(row, new_header, idx, index); err != nil {
			res.Failed++
			if len(res.Errors) <= maxReportedErrors {
				res.Errors = append(res.Errors, err.Error())
			} else {
				res.ErrorsTruncated = true
			}
		} else {
			res.Success++
		}
		index++
	}

	return res, nil
}

type Result struct {
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
			http.Error(w, "file is required", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()
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
				http.Error(w, fmt.Sprintf("line %v: %v", pe.Line+1, pe.Error()), http.StatusBadRequest)
				return
			case err == io.EOF:
				http.Error(w, "file is empty", http.StatusBadRequest)
				return
			default:
				fmt.Fprintf(os.Stderr, "processing csv file: %v", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
		}

		w.Header().Set("Content-Type", "multipart/form-data")
		json.NewEncoder(w).Encode(map[string]any{
			"success":          res.Success,
			"failed":           res.Failed,
			"errors":           res.Errors,
			"errors_truncated": res.ErrorsTruncated,
		})
	}
}
