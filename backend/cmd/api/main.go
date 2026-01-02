package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jagadeesh/grainlify/backend/internal/api"
	"github.com/jagadeesh/grainlify/backend/internal/bus"
	"github.com/jagadeesh/grainlify/backend/internal/bus/natsbus"
	"github.com/jagadeesh/grainlify/backend/internal/config"
	"github.com/jagadeesh/grainlify/backend/internal/db"
	"github.com/jagadeesh/grainlify/backend/internal/migrate"
	"github.com/jagadeesh/grainlify/backend/internal/syncjobs"
)

func main() {
	slog.Info("=== Grainlify API Starting ===")
	slog.Info("step", "1", "action", "loading environment variables")
	
	config.LoadDotenv()
	slog.Info("step", "2", "action", "loading configuration")
	cfg := config.Load()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.LogLevel(),
	}))
	slog.SetDefault(logger)

	// Log configuration (mask sensitive values)
	slog.Info("step", "3", "action", "configuration loaded",
		"env", cfg.Env,
		"log_level", cfg.Log,
		"http_addr", cfg.HTTPAddr,
		"port", os.Getenv("PORT"),
		"db_url_set", cfg.DBURL != "",
		"auto_migrate", cfg.AutoMigrate,
		"jwt_secret_set", cfg.JWTSecret != "",
		"nats_url_set", cfg.NATSURL != "",
		"github_oauth_client_id_set", cfg.GitHubOAuthClientID != "",
		"public_base_url", cfg.PublicBaseURL,
	)

	slog.Info("step", "4", "action", "connecting to database")
	var database *db.DB
	if cfg.DBURL == "" {
		if cfg.Env != "dev" {
			slog.Error("step", "4", "action", "db_connection_failed",
				"error", "DB_URL is required in non-dev environments",
				"env", cfg.Env,
			)
			os.Exit(1)
		}
		slog.Warn("step", "4", "action", "db_connection_skipped",
			"reason", "DB_URL not set; running without database (only /health will be useful)",
		)
	} else {
		slog.Info("step", "4.1", "action", "parsing_db_url", "db_url_length", len(cfg.DBURL))
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		slog.Info("step", "4.2", "action", "attempting_db_connection", "timeout", "10s")
		d, err := db.Connect(ctx, cfg.DBURL)
		cancel()
		if err != nil {
			slog.Error("step", "4", "action", "db_connection_failed",
				"error", err,
				"error_type", fmt.Sprintf("%T", err),
			)
			os.Exit(1)
		}
		slog.Info("step", "4.3", "action", "db_connection_successful",
			"max_conns", 10,
		)
		database = d
		defer func() {
			slog.Info("closing database connection")
			database.Close()
		}()

		if cfg.AutoMigrate {
			slog.Info("step", "5", "action", "running_database_migrations", "timeout", "120s")
			// Increased timeout to 120s to allow for retries and lock acquisition
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			err := migrate.Up(ctx, database.Pool)
			cancel()
			if err != nil {
				slog.Error("step", "5", "action", "migration_failed",
					"error", err,
					"error_type", fmt.Sprintf("%T", err),
				)
				os.Exit(1)
			}
			slog.Info("step", "5", "action", "migrations_complete")
		} else {
			slog.Info("step", "5", "action", "migrations_skipped", "reason", "AUTO_MIGRATE=false")
		}
	}

	slog.Info("step", "6", "action", "connecting_to_nats")
	var eventBus bus.Bus
	if cfg.NATSURL != "" {
		slog.Info("step", "6.1", "action", "nats_url_provided", "nats_url_length", len(cfg.NATSURL))
		b, err := natsbus.Connect(cfg.NATSURL)
		if err != nil {
			slog.Error("step", "6", "action", "nats_connection_failed",
				"error", err,
				"error_type", fmt.Sprintf("%T", err),
			)
			os.Exit(1)
		}
		slog.Info("step", "6.2", "action", "nats_connection_successful")
		eventBus = b
		defer func() {
			slog.Info("closing NATS connection")
			eventBus.Close()
		}()
	} else {
		slog.Info("step", "6", "action", "nats_skipped", "reason", "NATS_URL not set")
	}

	slog.Info("step", "7", "action", "initializing_api")
	app := api.New(cfg, api.Deps{DB: database, Bus: eventBus})
	slog.Info("step", "7", "action", "api_initialized")

	// Background workers (dev convenience). In production we run `cmd/worker` instead.
	// If NATS is configured, prefer the external worker process.
	if cfg.NATSURL == "" && database != nil && database.Pool != nil {
		slog.Info("step", "8", "action", "starting_background_worker")
		worker := syncjobs.New(cfg, database.Pool)
		go func() {
			slog.Info("background worker started")
			_ = worker.Run(context.Background())
		}()

		// GitHub App cleanup is now handled via webhooks (installation.deleted events)
		// No need for periodic polling
	} else {
		slog.Info("step", "8", "action", "background_worker_skipped",
			"reason", func() string {
				if cfg.NATSURL != "" {
					return "NATS configured (use external worker)"
				}
				if database == nil {
					return "database not available"
				}
				return "unknown"
			}(),
		)
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("step", "9", "action", "starting_http_server",
			"addr", cfg.HTTPAddr,
			"port", os.Getenv("PORT"),
		)
		errCh <- app.Listen(cfg.HTTPAddr)
	}()

	// Give server a moment to start
	time.Sleep(100 * time.Millisecond)
	slog.Info("=== Grainlify API Started Successfully ===",
		"http_addr", cfg.HTTPAddr,
		"env", cfg.Env,
	)

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		slog.Info("shutdown signal received", "signal", sig.String())
	case err := <-errCh:
		// Fiber returns nil only on clean shutdown; treat any error as fatal.
		slog.Error("http server exited",
			"error", err,
			"error_type", fmt.Sprintf("%T", err),
		)
		os.Exit(1)
	}

	slog.Info("step", "10", "action", "initiating_graceful_shutdown")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := api.Shutdown(ctx, app); err != nil {
		slog.Error("graceful shutdown failed",
			"error", err,
			"error_type", fmt.Sprintf("%T", err),
		)
		os.Exit(1)
	}

	slog.Info("shutdown complete")
}
