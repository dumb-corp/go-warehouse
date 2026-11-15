package main

import "time"

// === INVENTORY ===
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

type ScanOut struct {
	RobotID   string    `json:"robot_id"`
	ProductID string    `json:"product_id"`
	Name      string    `json:"product_name"`
	Quantity  int       `json:"quantity"`
	Zone      string    `json:"zone"`
	Status    string    `json:"status"`
	ScannedAt time.Time `json:"scanned_at"`
}

// === ROBOTS ===
type RobotRequest struct {
	ID             string       `json:"robot_id"`
	LastUpdate     time.Time    `json:"timestamp"`
	Location       Location     `json:"location"`
	ScanResults    []ScanResult `json:"scan_results"`
	BatteryLevel   float32      `json:"battery_level"`
	NextCheckpoint string       `json:"next_checkpoint"`
}

type RobotDashboard struct {
	ID           string    `json:"robot_id"`
	LastUpdate   time.Time `json:"last_update"`
	Location     Location  `json:"location"`
	BatteryLevel float32   `json:"battery_level"`
	Status       string    `json:"status"`
}

// === LOGIN ===
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type UserResponse struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}
