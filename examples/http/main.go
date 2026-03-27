package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/rlaas-io/rlaas/internal/store/counter/memory"
	filestore "github.com/rlaas-io/rlaas/internal/store/policy/file"
	"github.com/rlaas-io/rlaas/pkg/model"
	"github.com/rlaas-io/rlaas/pkg/rlaas"
)

// newClient constructs the rlaas evaluator used by run.
var newClient = func() rlaas.Evaluator {
	return rlaas.New(rlaas.Options{
		PolicyStore:  filestore.New("examples/policies.json"),
		CounterStore: memory.New(),
	})
}

// main runs a simple loop to show allow and deny decisions.
func main() {
	if err := run(os.Stdout); err != nil {
		panic(err)
	}
}

func run(out io.Writer) error {
	client := newClient()
	for i := 0; i < 5; i++ {
		d, err := client.Evaluate(context.Background(), model.RequestContext{OrgID: "acme", TenantID: "retail", Service: "payments", SignalType: "http", Operation: "charge", Endpoint: "/v1/charge", Method: "POST", UserID: "u1"})
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "request %d allowed=%v action=%s remaining=%d reason=%s\n", i+1, d.Allowed, d.Action, d.Remaining, d.Reason)
	}
	return nil
}
