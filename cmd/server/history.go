package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type HistoryFilter struct {
	Zones    []string
	Statuses []string
	From     *time.Time
	To       *time.Time
}

func (f *HistoryFilter) where() (string, []any) {
	conds := []string{}
	args := []any{}

	if len(f.Zones) > 0 {
		args = append(args, f.Zones)
		conds = append(conds, fmt.Sprintf("h.zone = ANY($%d)", len(args)))
	}

	if len(f.Statuses) > 0 {
		args = append(args, f.Statuses)
		conds = append(conds, fmt.Sprintf("h.status = ANY($%d)", len(args)))
	}

	if f.From != nil {
		args = append(args, *f.From)
		conds = append(conds, fmt.Sprintf("h.scanned_at >= $%d", len(args)))
	}

	if f.To != nil {
		args = append(args, f.To.AddDate(0, 0, 1))
		conds = append(conds, fmt.Sprintf("h.scanned_at < $%d", len(args)))
	}

	if len(conds) == 0 {
		return "", args
	}

	return " WHERE " + strings.Join(conds, " AND "), args
}

func checkFilterZone(zone string) bool {
	if zone >= "A" && zone <= "Z" {
		return true
	}

	return false
}

var validStatuses = map[string]bool{
	"OK":        true,
	"LOW_STOCK": true,
	"CRITICAL":  true,
}

func checkFilterStatus(status string) bool {
	return validStatuses[status]
}

func parseHistoryFilters(r *http.Request) (HistoryFilter, error) {
	var f HistoryFilter
	q := r.URL.Query()

	if s := q.Get("from"); s != "" {
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			return HistoryFilter{}, errors.New("invalid 'from' parameter")
		}
		f.From = &t
	}

	if s := q.Get("to"); s != "" {
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			return HistoryFilter{}, errors.New("invalid 'to' parameter")
		}
		f.To = &t
	}

	if f.From != nil && f.To != nil && f.From.After(*f.To) {
		return HistoryFilter{}, errors.New("from must be <= to")
	}

	if s := q.Get("zone"); s != "" {
		for _, z := range strings.Split(s, ",") {
			z = strings.ToUpper(strings.TrimSpace(z))
			if !checkFilterZone(z) {
				return HistoryFilter{}, errors.New("invalid zone")
			}
			f.Zones = append(f.Zones, z)
		}
	}

	if s := q.Get("status"); s != "" {
		for _, status := range strings.Split(s, ",") {
			status = strings.ToUpper(strings.TrimSpace(status))
			if !checkFilterStatus(status) {
				return HistoryFilter{}, errors.New("invalid status")
			}
			f.Statuses = append(f.Statuses, status)
		}
	}

	return f, nil
}

func parsePagination(r *http.Request) (limit, offset int, err error) {
	limit, offset = 50, 0

	if s := r.URL.Query().Get("limit"); s != "" {
		limit, err = strconv.Atoi(s)
		if err != nil || limit < 1 || limit > 200 {
			return 0, 0, errors.New("limit must be 1...200")
		}
	}

	if s := r.URL.Query().Get("offset"); s != "" {
		offset, err = strconv.Atoi(s)
		if err != nil || offset < 0 {
			return 0, 0, errors.New("offset must be >= 0")
		}
	}

	return limit, offset, nil
}

func historyHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, offset, err := parsePagination(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		filter, err := parseHistoryFilters(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		where, args := filter.where()

		itemsQuery := fmt.Sprintf(`SELECT h.robot_id, h.product_id, p.name, h.quantity, h.zone, COALESCE(h.status, ''), h.scanned_at
		FROM inventory_history h JOIN products p ON p.id = h.product_id %s ORDER BY h.scanned_at DESC, h.id DESC LIMIT %d OFFSET %d`, where, limit, offset)

		historyRows, err := pool.Query(r.Context(), itemsQuery, args...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "history query: %v", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		defer historyRows.Close()

		items := []ScanOut{}
		for historyRows.Next() {
			var s ScanOut
			if err := historyRows.Scan(
				&s.RobotID,
				&s.ProductID,
				&s.Name,
				&s.Quantity,
				&s.Zone,
				&s.Status,
				&s.ScannedAt); err != nil {
				fmt.Fprintf(os.Stderr, "history history scan error: %v\n", err)
				http.Error(w, "db error", http.StatusInternalServerError)
				return
			}
			items = append(items, s)
		}
		if err := historyRows.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "interating history items error: %v\n", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}

		totalCountQuery := `SELECT COUNT(*) FROM inventory_history h JOIN products p ON p.id = h.product_id ` + where

		var total int
		if err := pool.QueryRow(r.Context(), totalCountQuery, args...).Scan(&total); err != nil {
			fmt.Fprintf(os.Stderr, "count filtered items error: %v\n", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"total": total,
			"items": items,
			"pagination": map[string]int{
				"limit":  limit,
				"offset": offset,
			},
		})
	}
}
