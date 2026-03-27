package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rlaas-io/rlaas/pkg/model"
	"github.com/rlaas-io/rlaas/pkg/rlaas"
)

type stubErrorEvaluator struct{}

func (s *stubErrorEvaluator) Evaluate(_ context.Context, _ model.RequestContext) (model.Decision, error) {
	return model.Decision{}, errors.New("eval failed")
}

func (s *stubErrorEvaluator) StartConcurrencyLease(_ context.Context, _ model.RequestContext) (model.Decision, func() error, error) {
	return model.Decision{}, nil, nil
}

func TestRunExample(t *testing.T) {
	buf := &bytes.Buffer{}
	if err := run(buf); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "request 1") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestMainExample(t *testing.T) {
	main()
}

func TestMainExamplePanicPath(t *testing.T) {
	old, _ := os.Getwd()
	defer os.Chdir(old)
	_ = os.Chdir(t.TempDir())
	defer func() { _ = recover() }()
	_ = filepath.Separator
	main()
}

func TestRunExampleErrorPath(t *testing.T) {
	old, _ := os.Getwd()
	defer os.Chdir(old)
	tmp := t.TempDir()
	_ = os.Chdir(tmp)
	_ = os.MkdirAll("examples", 0755)
	_ = os.WriteFile(filepath.Join("examples", "policies.json"), []byte("not-json"), 0644)
	if err := run(&bytes.Buffer{}); err != nil {
		t.Fatalf("expected fail-open run behavior, got error: %v", err)
	}
}

func TestRun_EvaluateError(t *testing.T) {
	orig := newClient
	newClient = func() rlaas.Evaluator { return &stubErrorEvaluator{} }
	t.Cleanup(func() { newClient = orig })

	err := run(&bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error from run when evaluator fails")
	}
}

func TestMain_PanicsOnEvaluateError(t *testing.T) {
	orig := newClient
	newClient = func() rlaas.Evaluator { return &stubErrorEvaluator{} }
	defer func() {
		recover()
		newClient = orig
	}()
	main()
}
