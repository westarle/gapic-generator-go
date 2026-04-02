package showcase

import (
	"context"
	"fmt"
	"testing"
	"time"

	showcase "github.com/googleapis/gapic-showcase/client"
	"go.opentelemetry.io/otel"
)

func TestIssue(t *testing.T) {
	fix, clientOpts := setupLoggingTest(t, true, "grpc")
	ctx := context.Background()
	seqClient, err := showcase.NewSequenceClient(ctx, clientOpts...)
	if err != nil {
		t.Fatalf("failed to create sequence client: %v", err)
	}
	t.Cleanup(func() { seqClient.Close() })

	ctxSpan, span := otel.Tracer("test-tracer").Start(ctx, "APP")
	_ = runTracingServerFailureScenario(ctxSpan, t, seqClient)
	span.End()

	ctxFlush, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := fix.loggerProvider.ForceFlush(ctxFlush); err != nil {
		t.Fatalf("failed to flush logger provider: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	reqs := fix.logServer.getRequests()
	for _, req := range reqs {
		for _, rl := range req.ResourceLogs {
			for _, sl := range rl.ScopeLogs {
				for _, l := range sl.LogRecords {
					fmt.Printf("Log Body: %s\n", l.Body.GetStringValue())
					fmt.Println("Attributes:")
					for _, kv := range l.Attributes {
						fmt.Printf("  %s: %v\n", kv.Key, kv.Value.Value)
					}
				}
			}
		}
	}
}
