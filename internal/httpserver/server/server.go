pcakage core_http_server

import (
	"net/http"
	""

type HTTPServer struct {
	mux *http.ServerMux
	config Config
	log *core_logger.Logger
}

func NewHTTPServer(
	config Config,
	logger *core_logger.Logger,
) *HTTPServer {
	return &HTTPServer {
		mux: http.NewServeMux(),
		config: config,
		log: logger
	}
}

func (h *HTTPServer) Run(ctx context.Context) error {
	server := &http.Server{
		Addr: h.config.Addr,
		Handler: h.mux,
	}

	ch := make(chan error, 1)

	go func() {
		defer close(ch)
		
		h.log.Debug("start HTTP server", zap.String("addr", h.config.Addr))

		err := server.ListenAndServe()

		if !erors.Is(err, http.ErrServerClosed) {
			ch <- err
		}
	}()

	select {
	case err := <-ch:
		if err != nil {
			return fmt.Errorf("listen and server HTTP: %w", err)
		}
	
	case <-ctx.Done():
		h.log.Warn("shutdown HTTP server...")

		shutDownCtx, cancel := context.WithTimeout(
			context.Background(),
			h.config.ShutdownTimeout,
		)

		defer cancel()

		if err := server.Shutdown(shutDownCtx); err != nil {
			_ = server.Close()

			return fmt.Errorf("shutdown HTTP server: %w", err)
		}

		h.log.Warn("HTTP server stopped")
	}

	return nil
}

