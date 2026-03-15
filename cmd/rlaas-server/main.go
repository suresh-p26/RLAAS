package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	rlaasv1 "github.com/suresh-p26/RLAAS/api/proto"
	grpcadapter "github.com/suresh-p26/RLAAS/internal/adapter/grpc"
	httpadapter "github.com/suresh-p26/RLAAS/internal/adapter/http"
	"github.com/suresh-p26/RLAAS/internal/analytics"
	"github.com/suresh-p26/RLAAS/internal/controlplane/invalidation"
	"github.com/suresh-p26/RLAAS/internal/server"
	"github.com/suresh-p26/RLAAS/internal/store/counter/memory"
	filestore "github.com/suresh-p26/RLAAS/internal/store/policy/file"
	"github.com/suresh-p26/RLAAS/pkg/rlaas"

	"google.golang.org/grpc"
)

// main starts a local HTTP server with file policies and memory counters.
func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if err := runAll(defaultListen, defaultGRPCListen); err != nil {
		slog.Error("rlaas-server startup failed", "error", err)
		return
	}
}

func run(listenFn func(*server.HTTPServer) error) error {
	return runAll(listenFn, func(*server.GRPCServer) error { return nil })
}

func runAll(listenFn func(*server.HTTPServer) error, grpcListenFn func(*server.GRPCServer) error) error {
	policyFile := os.Getenv("RLAAS_POLICY_FILE")
	if policyFile == "" {
		policyFile = "examples/policies.json"
	}
	grpcAddr := os.Getenv("RLAAS_GRPC_ADDR")
	if grpcAddr == "" {
		grpcAddr = ":9090"
	}
	if _, err := os.Stat(policyFile); err != nil {
		return fmt.Errorf("policy file not found: %s", policyFile)
	}
	client := rlaas.New(rlaas.Options{
		PolicyStore:  filestore.New(policyFile),
		CounterStore: memory.New(),
		KeyPrefix:    "rlaas",
	})
	policyStore := filestore.New(policyFile)
	b := invalidation.NewBroker()
	analyticsRecorder := analytics.NewRecorder()
	invalidationTargets := loadInvalidationTargets()
	pushClient := &http.Client{Timeout: 2 * time.Second}
	enqueueInvalidation := startInvalidationDispatcher(pushClient, invalidationTargets, analyticsRecorder)
	b.Subscribe("policy.changed", func(event map[string]string) {
		analyticsRecorder.Record(context.Background(), "policy_invalidation_event", event)
	})
	checkHandler := httpadapter.CheckHandler(client)
	httpServer := server.NewHTTPServer(":8080", checkHandler)
	mux := httpServer.Mux
	acquireHandler, releaseHandler := httpadapter.NewAcquireReleaseHandlers(client)
	mux.Handle("/v1/acquire", acquireHandler)
	mux.Handle("/v1/release", releaseHandler)
	policiesHandler := httpadapter.PoliciesHandlerWithHooks(policyStore, func(ctx context.Context, topic string, event map[string]string) error {
		_ = b.Publish(ctx, topic, event)
		enqueueInvalidation(copyEvent(event))
		return nil
	}, analyticsRecorder.Record)
	mux.Handle("/v1/policies", policiesHandler)
	mux.Handle("/v1/policies/", policiesHandler)
	mux.Handle("/v1/analytics/summary", analytics.SummaryHandler(analyticsRecorder))
	mw := httpadapter.NewMiddleware(client)
	// Demo endpoint applies middleware so you can observe enforcement quickly.
	mux.Handle("/demo", mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})))

	grpcSvc := grpcadapter.NewRateLimitService(client)
	grpcServer := grpc.NewServer(grpc.UnaryInterceptor(grpcadapter.UnaryServerInterceptor(client)))
	rlaasv1.RegisterRateLimitServiceServer(grpcServer, grpcSvc)
	if err := grpcListenFn(server.NewGRPCServer(grpcAddr, grpcServer)); err != nil {
		return err
	}
	return listenFn(httpServer)
}

func startInvalidationDispatcher(client *http.Client, targets []string, analyticsRecorder *analytics.Recorder) func(map[string]string) {
	if len(targets) == 0 || client == nil {
		return func(map[string]string) {}
	}
	const queueSize = 256
	workers := len(targets)
	if workers > 4 {
		workers = 4
	}
	queue := make(chan map[string]string, queueSize)
	for i := 0; i < workers; i++ {
		go func() {
			for event := range queue {
				publishInvalidation(context.Background(), client, targets, event)
			}
		}()
	}
	return func(event map[string]string) {
		select {
		case queue <- event:
		default:
			if analyticsRecorder != nil {
				analyticsRecorder.Record(context.Background(), "policy_invalidation_dropped", map[string]string{"reason": "queue_full"})
			}
		}
	}
}

func copyEvent(event map[string]string) map[string]string {
	out := make(map[string]string, len(event))
	for k, v := range event {
		out[k] = v
	}
	return out
}

func defaultListen(s *server.HTTPServer) error {
	// Start HTTP in a goroutine so we can wait for shutdown signals.
	errCh := make(chan error, 1)
	go func() { errCh <- s.ListenAndServe() }()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		// Server failed to start or stopped unexpectedly.
		return err
	case sig := <-quit:
		slog.Info("shutting down", "signal", sig.String())
	}

	if err := s.Shutdown(10 * time.Second); err != nil {
		slog.Error("http shutdown error", "error", err)
		return err
	}
	slog.Info("server stopped gracefully")
	return nil
}

func defaultGRPCListen(s *server.GRPCServer) error {
	if s == nil || s.Server == nil {
		return fmt.Errorf("grpc server is not configured")
	}
	go func() {
		if err := s.ListenAndServe(); err != nil {
			slog.Error("grpc server stopped", "error", err)
		}
	}()
	return nil
}

func loadInvalidationTargets() []string {
	raw := strings.TrimSpace(os.Getenv("RLAAS_INVALIDATION_TARGETS"))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func publishInvalidation(ctx context.Context, client *http.Client, targets []string, event map[string]string) {
	if len(targets) == 0 || client == nil {
		return
	}
	body, err := json.Marshal(event)
	if err != nil {
		return
	}
	workerCount := len(targets)
	if workerCount > 8 {
		workerCount = 8
	}
	jobs := make(chan string, len(targets))
	for _, t := range targets {
		jobs <- t
	}
	close(jobs)

	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for target := range jobs {
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(target, "/")+"/v1/agent/invalidate", bytes.NewReader(body))
				if err != nil {
					continue
				}
				req.Header.Set("Content-Type", "application/json")
				resp, err := client.Do(req)
				if err != nil {
					continue
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
		}()
	}
	wg.Wait()
}
