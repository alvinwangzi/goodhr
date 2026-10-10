// 本文件负责启动 HRPlus 5 云端 HTTP 服务。
package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"goodhr5/cloud/backend/internal/httpapi"
)

func main() {
	logPath, err := setupLogger()
	if err != nil {
		log.Fatalf("setup logger failed: %v", err)
	}
	addr := envOrDefault("GOODHR_CLOUD_ADDR", ":8084")
	server, err := httpapi.NewServer()
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go server.RunExecutionReportNotifications(ctx)

	log.Printf("HRPlus cloud backend log file: %s", logPath)
	log.Printf("HRPlus cloud backend listening on %s", addr)
	httpServer := &http.Server{Addr: addr, Handler: server.Routes()}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = httpServer.Shutdown(shutdown)
	}()
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	if ctx.Err() != nil {
		<-shutdownDone
	}
}

func envOrDefault(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func setupLogger() (string, error) {
	logPath := envOrDefault("GOODHR_CLOUD_LOG_FILE", "logs/backend.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return "", err
	}
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	log.SetOutput(io.MultiWriter(os.Stdout, file))
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	return logPath, nil
}
