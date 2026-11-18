package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func validateRobotRequest(req RobotRequest) error {
	if strings.TrimSpace(req.ID) == "" {
		return errors.New("id is required")
	}

	if req.BatteryLevel > 100 || req.BatteryLevel < 0 {
		return errors.New("invalid battery level")
	}

	if strings.TrimSpace(req.Location.Zone) == "" {
		return errors.New("zone is required")
	}

	for _, s := range req.ScanResults {
		if strings.TrimSpace(s.ID) == "" {
			return errors.New("product id is required")
		}

		if s.Quantity < 0 {
			return errors.New("quantity must be >= 0")
		}
	}

	return nil
}

func checkRobotStatus(last_update time.Time, battery_level float32) string {
	if time.Since(last_update) > 60*time.Second {
		return "offline"
	}
	if battery_level < 20 {
		return "low_battery"
	}
	return "active"
}

func robotsDataHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

		var req RobotRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			fmt.Fprintf(os.Stderr, "json decode error: %v\n", err)
			http.Error(w, "error json data", http.StatusBadRequest)
			return
		}

		if !checkRobotToken(r, req.ID) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		if err := validateRobotRequest(req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.LastUpdate.IsZero() {
			req.LastUpdate = time.Now()
		}
		req.LastUpdate = req.LastUpdate.UTC()

		robotStatus := checkRobotStatus(time.Now().UTC(), req.BatteryLevel)

		ctx := r.Context()
		tx, err := pool.Begin(r.Context())
		if err != nil {
			fmt.Fprintf(os.Stderr, "create transaction error: %v\n", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		defer tx.Rollback(r.Context())

		_, err = tx.Exec(ctx, `
			INSERT INTO robots (id, status, battery_level, last_update, current_zone, current_row, current_shelf)
			VALUES ($1, $2, $3, NOW(), $4, $5, $6)
			ON CONFLICT (id) DO UPDATE SET
				status = EXCLUDED.status,
				battery_level = EXCLUDED.battery_level,
				last_update = EXCLUDED.last_update,
				current_zone = EXCLUDED.current_zone,
				current_row = EXCLUDED.current_row,
				current_shelf = EXCLUDED.current_shelf`,
			req.ID, robotStatus, req.BatteryLevel, req.Location.Zone, req.Location.Row, req.Location.Shelf)

		if err != nil {
			fmt.Fprintf(os.Stderr, "robot insert error: %v\n", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		for _, s := range req.ScanResults {
			_, err = tx.Exec(ctx, `INSERT INTO products (id, name) VALUES ($1, $2)
			ON CONFLICT (id) DO NOTHING`, s.ID, s.Name)

			if err != nil {
				fmt.Fprintf(os.Stderr, "product insert error: %v\n", err)
				http.Error(w, "db error", http.StatusInternalServerError)
				return
			}

			_, err := tx.Exec(ctx, `
			INSERT INTO inventory_history 
			(robot_id, product_id, quantity, zone, row_number, shelf_number, status, scanned_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
				req.ID, s.ID, s.Quantity, req.Location.Zone, req.Location.Row, req.Location.Shelf, s.Status, req.LastUpdate)

			if err != nil {
				fmt.Fprintf(os.Stderr, "inventory_history insert error: %v\n", err)
				http.Error(w, "db error", http.StatusInternalServerError)
				return
			}
		}

		if err := tx.Commit(r.Context()); err != nil {
			fmt.Fprintf(os.Stderr, "commit transaction error: %v\n", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":     "received",
			"message_id": fmt.Sprintf("%s-%d", req.ID, time.Now().UnixNano()),
		})
	}
}
