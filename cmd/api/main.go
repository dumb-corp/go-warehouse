package main

import (
	"fmt"
	"os"

	core_logger "go-wharehouse/internal/core/logger"
	robots_transport_http "go-wharehouse/internal/features/transport/http"
)

func main() {
	fmt.Println("Hello, I'm wharehouse")
	logger, err := core_logger.NewLogger(core_logger.NewConfigMust)
	if err != nil {
		fmt.Println("failed to init app logger:", err)
		os.Exit(1)
	}
	defer logger.Close()

	logger.Debug("starting app")

	robotsTransportHTTP := robots_transport_http.NewUserHTTPHandler(nil)
	robotsRoutes := 
}
