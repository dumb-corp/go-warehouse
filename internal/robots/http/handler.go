package robots

import (
	"encoding/json"
	"net/http"
)

func (h RobotHTTPHandler) TakeData(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	var req RobotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	res := RobotResponse{
		Status:    "received",
		MessageID: "001",
	}
	if err := json.NewEncoder(w).Encode(&res); err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
}
