package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/majidkabir/sootcase/internal/api"
	"github.com/majidkabir/sootcase/pkg/places"
)

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	var c common
	c.register(fs)
	addr := fs.String("addr", envOr("ADDR", ":8080"), "listen address")
	maxConcurrent := fs.Int("max-concurrent", 0, "calculation requests at once; more get 503 (default 4 x CPUs)")
	timeout := fs.Duration("request-timeout", 30*time.Second, "time limit per calculation request; past it 504")
	maxLegs := fs.Int("max-legs", 100, "legs (and hubs) allowed per shipment")
	batchWorkers := fs.Int("batch-workers", 0, "shipments of one batch or report calculated at once (default 4 x CPUs; raise it when routing through OSRM)")
	bigBatch := fs.Int("big-batch", 1000, "batches and reports of this many shipments or more take turns")
	bigConcurrent := fs.Int("big-concurrent", 2, "big batches calculated at once; the rest wait in line")
	_ = fs.Parse(args)

	c.countryKm = true // the API's ?country_km=true
	calculator, ds, err := c.calculator()
	if err != nil {
		return err
	}
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	go func() { // load the place list in the background so the first search is quick
		if _, err := places.Default(); err != nil {
			log.Error("loading places", "err", err)
		}
	}()
	srv := &http.Server{
		Addr: *addr,
		Handler: (&api.Server{Calc: calculator, Factors: ds, Log: log,
			Limits: api.Limits{MaxConcurrent: *maxConcurrent, Timeout: *timeout, MaxLegs: *maxLegs, BatchWorkers: *batchWorkers,
				BigBatch: *bigBatch, BigConcurrent: *bigConcurrent}}).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      *timeout + 30*time.Second, // room to write the 504 after a timeout
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", *addr, "factors", ds.Name)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

// env reads SOOTCASE_<name>, falling back to the old CARBONARA_<name> so
// existing setups keep working after the rename.
func env(name string) string {
	if v := os.Getenv("SOOTCASE_" + name); v != "" {
		return v
	}
	return os.Getenv("CARBONARA_" + name)
}

func envOr(name, def string) string {
	if v := env(name); v != "" {
		return v
	}
	return def
}
