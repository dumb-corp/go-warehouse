package robots

import (
	"time"
)

type ScanResult struct {
	ProductID   string `json:"product_id"`
	ProductName string `json:"product_name"`
	Quantity    int    `json:"quantity"`
	Status      string `json:"status"`
}

type RobotRequest struct {
	RobotID        string       `json:"robot_id"`
	Timestamp      time.Time    `json:"timestamp"`
	Location       string       `json:"location"`
	ScanResults    []ScanResult `json:"scan_results"`
	BatteryLevel   float64      `json:"battery_level"`
	NextCheckpoint string       `json:"next_checkpoint"`
}

type RobotResponse struct {
	Status    string `json:"status"`
	MessageID string `json:"message_id"`
}
