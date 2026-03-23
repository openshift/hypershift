package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/openshift/jira-agent-dashboard/internal/api"
	"github.com/openshift/jira-agent-dashboard/internal/db"

	_ "github.com/mattn/go-sqlite3"
)

func main() {
	dbPath := envOrDefault("DB_PATH", "/data/dashboard.db")
	port := envOrDefault("PORT", "8080")
	webDir := envOrDefault("WEB_DIR", "./web")

	conn, err := sql.Open("sqlite3", dbPath+"?mode=rw")
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer conn.Close()

	if err := db.InitSchema(conn); err != nil {
		log.Fatalf("failed to initialize schema: %v", err)
	}

	store := db.NewStore(conn)
	srv := api.NewServer(store, webDir)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})
	mux.Handle("/", srv)

	httpSrv := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	// Graceful shutdown on SIGTERM/SIGINT.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	go func() {
		log.Printf("jira-agent-dashboard listening on :%s", port)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("shutdown error: %v", err)
	}
	log.Println("shutdown complete")
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
