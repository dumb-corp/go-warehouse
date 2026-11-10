package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Location struct {
	Zone  string `json:"zone"`
	Row   int    `json:"row"`
	Shelf int    `json:"shelf"`
}

type ScanResult struct {
	ID       string `json:"product_id"`
	Name     string `json:"product_name"`
	Quantity int    `json:"quantity"`
	Status   string `json:"status"`
}

type RobotRequest struct {
	ID             string       `json:"robot_id"`
	Timestamp      string       `json:"timestamp"`
	Location       Location     `json:"location"`
	ScanResults    []ScanResult `json:"scan_results"`
	BatteryLevel   int          `json:"battery_level"`
	NextCheckpoint string       `json:"next_checkpoint"`
}

func robotsDataHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req RobotRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			fmt.Printf("json decode error: %v", err)
			http.Error(w, "error json data", http.StatusBadRequest)
		}
		t, err := time.Parse(time.RFC3339, req.Timestamp)
		if err != nil {
			t = time.Now() //TODO: придумать что делать, если формат времени не совпадает с нужным
		}

		ctx := r.Context()
		robotStatus := ""
		if len(req.ScanResults) > 0 {
			robotStatus = "active"
		} else {
			robotStatus = "off"
		}

		_, err = pool.Exec(ctx, `
		INSERT INTO robots (id, status, battery_level, last_update, current_zone, current_row, current_shelf) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (id) DO NOTHING;`, req.ID, robotStatus, req.BatteryLevel, t, req.Location.Zone, req.Location.Row, req.Location.Shelf)

		if err != nil {
			fmt.Fprintf(os.Stderr, "robot insert error: %v\n", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		for _, s := range req.ScanResults {
			_, err = pool.Exec(ctx, `INSERT INTO products (id, name) VALUES ($1, $2)
			ON CONFLICT (id) DO NOTHING`, s.ID, s.Name)

			if err != nil {
				fmt.Fprintf(os.Stderr, "product insert error: %v\n", err)
				http.Error(w, "db error", http.StatusInternalServerError)
				return
			}

			_, err := pool.Exec(ctx, `
			INSERT INTO inventory_history 
			(robot_id, product_id, quantity, zone, row_number, shelf_number, status, scanned_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
				req.ID, s.ID, s.Quantity, req.Location.Zone, req.Location.Row, req.Location.Shelf, s.Status, t)

			if err != nil {
				fmt.Fprintf(os.Stderr, "iventory_history insert error: %v\n", err)
				http.Error(w, "db error", http.StatusInternalServerError)
				return
			}
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"status": "received"})
	}
}

func main() {
	ctx := context.Background()

	connString := fmt.Sprintf("postgres://%v:%v@localhost:5432/%v", os.Getenv("POSTGRES_USER"), os.Getenv("POSTGRES_PASSWORD"), os.Getenv("POSTGRES_DB"))
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to connect databse: %v\n", err)
		os.Exit(1)
	}

	if err := pool.Ping(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Ping database error: %v\n", err)
		os.Exit(1)
	} else {
		fmt.Println("database connected successfully")
	}

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Hello, world!")
	})

	http.HandleFunc("POST /api/robots/data", robotsDataHandler(pool))

	http.ListenAndServe("localhost:3030", nil)
}
