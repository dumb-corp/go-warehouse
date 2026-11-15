package logger

import (
	"context"
	"log/slog"
	"os"
)

type Logger struct {
	*slog.Logger
}

func FromContext(ctx context.Context) *Logger {
	log, ok := ctx.Value("log").(*Logger)
	if !ok {
		panic("no logger in context")
	}

	return log
}

func New(config Config) (*Logger, func() error, error) {
	file, err := os.OpenFile("app.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, nil, err
	}

	stdoutHandler := slog.NewTextHandler(os.Stdin, nil)
	fileHandler := slog.NewJSONHandler(file, nil)

	multiHandler := slog.NewMultiHandler(stdoutHandler, fileHandler)
	logger := slog.New(multiHandler)

	return &Logger{
		Logger: logger,
	}, file.Close, nil
}
