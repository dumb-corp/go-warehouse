package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

func dashboardCurrentHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scanRows, err := pool.Query(r.Context(), `SELECT h.robot_id, h.product_id, p.name, h.quantity, h.zone, COALESCE(h.status, ''), h.scanned_at
		FROM inventory_history h JOIN products p ON p.id = h.product_id ORDER BY h.scanned_at DESC LIMIT 20`)
		if err != nil {
			fmt.Fprintf(os.Stderr, "dashboard select error: %v\n", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		defer scanRows.Close()

		scans := []ScanOut{}
		for scanRows.Next() {
			var s ScanOut
			if err := scanRows.Scan(
				&s.RobotID,
				&s.ProductID,
				&s.Name,
				&s.Quantity,
				&s.Zone,
				&s.Status,
				&s.ScannedAt); err != nil {
				fmt.Fprintf(os.Stderr, "current scan error: %v\n", err)
				http.Error(w, "db error", http.StatusInternalServerError)
				return
			}
			scans = append(scans, s)
		}
		if err := scanRows.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "interating scans error: %v\n", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}

		robots := []RobotDashboard{}
		robotRows, err := pool.Query(r.Context(),
			`SELECT id, battery_level, last_update, current_zone, current_row, current_shelf 
			FROM robots`)
		if err != nil {
			fmt.Fprintf(os.Stderr, "dashboard select error: %v\n", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		defer robotRows.Close()

		for robotRows.Next() {
			var robot RobotDashboard
			if err := robotRows.Scan(
				&robot.ID,
				&robot.BatteryLevel,
				&robot.LastUpdate,
				&robot.Location.Zone,
				&robot.Location.Row,
				&robot.Location.Shelf,
			); err != nil {
				fmt.Fprintf(os.Stderr, "robot scan error: %v\n", err)
				http.Error(w, "db error", http.StatusInternalServerError)
				return
			}
			robot.Status = checkRobotStatus(robot.LastUpdate, robot.BatteryLevel)
			robots = append(robots, robot)
		}
		if err := robotRows.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "interating robots error: %v\n", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}

		var scansToday int
		if err = pool.QueryRow(r.Context(),
			`SELECT COUNT(*) FROM inventory_history WHERE scanned_at >= CURRENT_DATE`,
		).Scan(&scansToday); err != nil {
			fmt.Fprintf(os.Stderr, "count scans error: %v\n", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"robots":       robots,
			"recent_scans": scans,
			"statistics": map[string]int{ //TODO: расширить статистики
				"robot_count": len(robots),
				"scans_count": scansToday,
			}})
	}
}
