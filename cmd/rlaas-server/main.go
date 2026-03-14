package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	rlaasv1 "rlaas/api/proto"
	grpcadapter "rlaas/internal/adapter/grpc"
	httpadapter "rlaas/internal/adapter/http"
	"rlaas/internal/analytics"
	"rlaas/internal/controlplane/invalidation"
	"rlaas/internal/server"
	"rlaas/internal/store/counter/memory"
	filestore "rlaas/internal/store/policy/file"
	"rlaas/pkg/rlaas"
	"strings"
	"time"

	"google.golang.org/grpc"
)

// main starts a local HTTP server with file policies and memory counters.
func main() {
	if err := runAll(defaultListen, defaultGRPCListen); err != nil {
		log.Printf("rlaas-server startup failed: %v", err)
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
		publishInvalidation(ctx, pushClient, invalidationTargets, event)
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

func defaultListen(s *server.HTTPServer) error {
	return s.ListenAndServe()
}

func defaultGRPCListen(s *server.GRPCServer) error {
	if s == nil || s.Server == nil {
		return fmt.Errorf("grpc server is not configured")
	}
	go func() {
		if err := s.ListenAndServe(); err != nil {
			log.Printf("rlaas grpc server stopped: %v", err)
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
	for _, target := range targets {
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
}
