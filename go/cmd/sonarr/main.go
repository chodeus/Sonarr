// Command sonarr is the Sonarr daemon entry point.
//
// During the C#-to-Go port this binary is a thin shell that grows
// constructor wiring as subsystems land. At Phase 1 sign-off it
// satisfies the strategic plan's bar: "a Go binary that opens an
// existing user DB and reports the same schema version" — see
// ~/.claude/plans/curried-discovering-meadow.md § Phase 1.
//
// What this binary currently does:
//
//  1. Parse startup args (`/data=PATH`, `/nobrowser`, etc.).
//  2. Detect the AppFolder (data dir, startup dir, temp dir).
//  3. Load <data>/config.xml or treat as empty when missing.
//  4. Construct a messaging Bus + lifecycle wiring.
//  5. Open the SQLite main database at <data>/<InstanceName>.db.
//  6. Run migrations to head (Sonarr always auto-migrates on
//     startup; the C# tree does this in NzbDroneStartupArguments).
//  7. Report the schema version through slog.
//  8. Publish ApplicationStartedEvent and block on ctx.Done().
//  9. On signal, publish ApplicationShutdownRequested, drain the
//     bus, close the DB.
//
// Postgres support: when config.xml carries PostgresHost +
// PostgresMainDb (matching the C# detection), the binary opens
// Postgres instead of SQLite. Phase 1 ships only the wiring; the
// configfile package already exposes the typed accessors.
//
// See ../../CONVERSION.md for the rules every change must follow.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"sonarr.tv/sonarr/internal/appenv"
	"sonarr.tv/sonarr/internal/auth"
	"sonarr.tv/sonarr/internal/buildinfo"
	"sonarr.tv/sonarr/internal/configfile"
	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/instrumentation"
	"sonarr.tv/sonarr/internal/lifecycle"
	"sonarr.tv/sonarr/internal/messaging"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "sonarr: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Special-case --version BEFORE structured logging — keep the
	// machine-readable line clean for build tooling.
	for _, a := range os.Args[1:] {
		if a == "--version" || a == "-version" || a == "/version" {
			fmt.Println(buildinfo.String())
			return nil
		}
	}

	logger := instrumentation.NewLogger(os.Stderr, slog.LevelInfo)
	slog.SetDefault(logger)
	logger.Info("sonarr starting", "version", buildinfo.Version, "commit", buildinfo.Commit)
	defer logger.Info("sonarr stopped")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := serve(ctx, logger); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// serve is the top-level wiring point. As phases land, additional
// subsystems (HTTP server, SignalR hub, job scheduler, providers)
// are constructed here.
func serve(ctx context.Context, logger *slog.Logger) error {
	startup := appenv.ParseStartupArgs(os.Args[1:])

	folder, err := appenv.DetectAppFolder(startup)
	if err != nil {
		return fmt.Errorf("appenv: %w", err)
	}
	logger.Info("appfolder",
		"data", folder.AppData,
		"startup", folder.Startup,
		"config", folder.ConfigPath())

	if err := os.MkdirAll(folder.AppData, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	loader := configfile.NewLoader(folder.ConfigPath())
	if err := loader.Load(ctx); err != nil {
		return fmt.Errorf("config load: %w", err)
	}
	bus := messaging.New(logger)
	cfg := configfile.NewProvider(loader, configfile.Overrides{}, bus, auth.GenerateAPIKey)

	instanceName, err := cfg.InstanceName(ctx)
	if err != nil {
		return fmt.Errorf("config InstanceName: %w", err)
	}

	dbCfg, source, err := buildDatastoreConfig(ctx, cfg, folder, instanceName)
	if err != nil {
		return fmt.Errorf("datastore config: %w", err)
	}
	db, err := datastore.Open(ctx, dbCfg)
	if err != nil {
		return fmt.Errorf("datastore open (%s): %w", source, err)
	}
	defer func() { _ = db.Close() }()

	if err := datastore.Migrate(ctx, db); err != nil {
		return fmt.Errorf("datastore migrate: %w", err)
	}
	version, err := datastore.CurrentVersion(ctx, db)
	if err != nil {
		return fmt.Errorf("datastore version: %w", err)
	}
	logger.Info("datastore ready",
		"engine", string(db.Engine),
		"role", string(db.Role),
		"source", source,
		"schemaVersion", version,
		"instance", instanceName)

	// Bus is hot — fan out the start signal so subscribers (auth
	// service seed, future health checks, etc.) can do their lazy
	// init.
	if err := messaging.Publish(ctx, bus, lifecycle.ApplicationStartedEvent{}); err != nil {
		return fmt.Errorf("publish ApplicationStartedEvent: %w", err)
	}

	logger.Info("daemon ready", "version", buildinfo.Version,
		"hint", "HTTP server / SignalR / scheduler arrive in Phase 5")
	<-ctx.Done()

	// Shutdown sequence — publish the signal BEFORE BeginShutdown
	// gates further lifecycle events; then drain the async pool.
	logger.Info("sonarr shutdown requested")
	if err := messaging.Publish(context.Background(), bus, lifecycle.ApplicationShutdownRequested{}); err != nil &&
		!errors.Is(err, messaging.ErrShutdown) {
		logger.Warn("publish ApplicationShutdownRequested failed", "err", err)
	}
	bus.BeginShutdown()
	bus.Wait()
	return ctx.Err()
}

// buildDatastoreConfig picks SQLite or Postgres based on the
// configfile's Postgres* keys and returns a human-readable source
// label for logging. The C# detection is "PostgresMainDb is
// non-empty"; we replicate that so existing user databases keep
// being detected the same way.
func buildDatastoreConfig(ctx context.Context, cfg *configfile.Provider, folder appenv.AppFolderInfo, instance string) (datastore.Config, string, error) {
	pgMain, err := cfg.String(ctx, "PostgresMainDb", "", false)
	if err != nil {
		return datastore.Config{}, "", err
	}
	if pgMain == "" {
		return datastore.Config{
			Engine: datastore.EngineSQLite,
			Role:   datastore.RoleMain,
			Path:   folder.MainDBPath(instance),
		}, "sqlite:" + folder.MainDBPath(instance), nil
	}
	host, err := cfg.String(ctx, "PostgresHost", "localhost", false)
	if err != nil {
		return datastore.Config{}, "", err
	}
	port, err := cfg.Int(ctx, "PostgresPort", 5432, false)
	if err != nil {
		return datastore.Config{}, "", err
	}
	user, err := cfg.String(ctx, "PostgresUser", "", false)
	if err != nil {
		return datastore.Config{}, "", err
	}
	password, err := cfg.String(ctx, "PostgresPassword", "", false)
	if err != nil {
		return datastore.Config{}, "", err
	}
	return datastore.Config{
		Engine:   datastore.EnginePostgres,
		Role:     datastore.RoleMain,
		Host:     host,
		Port:     port,
		User:     user,
		Password: password,
		Database: pgMain,
	}, fmt.Sprintf("postgres://%s@%s:%d/%s", user, host, port, pgMain), nil
}
