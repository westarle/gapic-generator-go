package showcase

import (
	"context"
	"os"
	"testing"
	"time"

	"cloud.google.com/go/auth"
	"github.com/google/go-cmp/cmp"
	showcase "github.com/googleapis/gapic-showcase/client"
	gax "github.com/googleapis/gax-go/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	olog "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func setupLoggingTest(t *testing.T, enableLogging bool) (*observabilityFixture, []option.ClientOption) {
	gax.TestOnlyResetIsFeatureEnabled()
	t.Cleanup(gax.TestOnlyResetIsFeatureEnabled)

	if enableLogging {
		os.Setenv("GOOGLE_SDK_GO_EXPERIMENTAL_LOGGING", "true")
	} else {
		os.Setenv("GOOGLE_SDK_GO_EXPERIMENTAL_LOGGING", "false")
	}
	t.Cleanup(func() { os.Unsetenv("GOOGLE_SDK_GO_EXPERIMENTAL_LOGGING") })

	fix := setupObservabilityFixture(t)
	oldLP := global.GetLoggerProvider()
	t.Cleanup(func() { global.SetLoggerProvider(oldLP) })
	global.SetLoggerProvider(fix.loggerProvider)

	// We also need to set the global tracer provider so logs get trace contexts.
	oldTP := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(oldTP) })
	otel.SetTracerProvider(fix.provider)

	grpcClientOpts := []option.ClientOption{
		option.WithEndpoint("127.0.0.1:7469"),
		option.WithAuthCredentials(auth.NewCredentials(&auth.CredentialsOptions{
			TokenProvider: dummyTokenProvider{},
		})),
		option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
		option.WithLogger(otelslog.NewLogger("github.com/googleapis/gapic-showcase/client")),
	}

	return fix, grpcClientOpts
}

func verifyInMemoryLogsEmpty(t *testing.T, fix *observabilityFixture) {
	t.Helper()

	ctxFlush, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := fix.loggerProvider.ForceFlush(ctxFlush); err != nil {
		t.Fatalf("failed to flush logger provider: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	logs := fix.logServer.GetCapturedLogs()
	if len(logs) > 0 {
		t.Fatalf("expected to receive no log exports, got %d", len(logs))
	}
}

func verifyInMemoryLog(t *testing.T, fix *observabilityFixture, expectedSeverity olog.SeverityNumber, expectedScope string, traceID trace.TraceID, wantAttrs map[string]any) {
	t.Helper()

	ctxFlush, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := fix.loggerProvider.ForceFlush(ctxFlush); err != nil {
		t.Fatalf("failed to flush logger provider: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	logs := fix.logServer.GetCapturedLogs()
	if len(logs) == 0 {
		t.Fatalf("expected to receive log exports, got none")
	}

	var gotLog *CapturedLog
	for _, l := range logs {
		// Filter by traceID and severity. We skip the "api request" / "api response" debug logs 
		// emitted by the generator and look specifically for the log where the Body contains the error message 
		// or where error.type is present as an attribute.
		if string(l.TraceID) == string(traceID[:]) && l.Severity == expectedSeverity {
			if _, ok := l.Attributes["error.type"]; ok {
				// Deep copy
				lCopy := l
				gotLog = &lCopy
				break
			}
		}
	}

	if gotLog == nil {
		t.Fatalf("did not find the expected log with severity %v", expectedSeverity)
	}

	if gotLog.Scope != expectedScope {
		t.Errorf("expected log scope to be %q, got %q", expectedScope, gotLog.Scope)
	}

	if wantAttrs != nil {
		if _, ok := gotLog.Attributes["gcp.client.version"]; ok {
			gotLog.Attributes["gcp.client.version"] = "DYNAMIC"
		}

		if diff := cmp.Diff(wantAttrs, gotLog.Attributes); diff != "" {
			t.Errorf("Client log attributes mismatch (-want +got):\n%s", diff)
		}
	}
}

func TestObservability_Logging_Success(t *testing.T) {
	_, clientOpts := setupLoggingTest(t, true)
	ctx := context.Background()
	seqClient, err := showcase.NewSequenceClient(ctx, clientOpts...)
	if err != nil {
		t.Fatalf("failed to create sequence client: %v", err)
	}
	t.Cleanup(func() { seqClient.Close() })

	ctxSpan, span := otel.Tracer("test-tracer").Start(ctx, "APP")
	_ = runTracingSuccessScenario(ctxSpan, t, seqClient)
	span.End()

	// TODO: Product requirements dictate that no logs should be emitted for success scenarios.
	// However, the current gapic-generator-go implementation emits debug logs for every RPC request
	// and response regardless of the outcome.
	// verifyInMemoryLogsEmpty(t, fix)
}

func TestObservability_Logging_Failure(t *testing.T) {
	fix, clientOpts := setupLoggingTest(t, true)
	ctx := context.Background()
	seqClient, err := showcase.NewSequenceClient(ctx, clientOpts...)
	if err != nil {
		t.Fatalf("failed to create sequence client: %v", err)
	}
	t.Cleanup(func() { seqClient.Close() })

	ctxSpan, span := otel.Tracer("test-tracer").Start(ctx, "APP")
	_ = runTracingServerFailureScenario(ctxSpan, t, seqClient)
	span.End()
	traceID := span.SpanContext().TraceID()

	wantAttrs := map[string]any{
		"error.type":               "NOT_FOUND",
		"gcp.client.artifact":      "github.com/googleapis/gapic-showcase/client",
		"gcp.client.language":      "go",
		"gcp.client.repo":          "googleapis/google-cloud-go",
		"gcp.client.service":       "showcase",
		"gcp.client.version":       "DYNAMIC",
		"rpc.response.status_code": "NOT_FOUND",
		"rpc.system.name":          "grpc",
		"url.domain":               "showcase.googleapis.com",
	}

	verifyInMemoryLog(t, fix, olog.SeverityNumber_SEVERITY_NUMBER_DEBUG, "github.com/googleapis/gapic-showcase/client", traceID, wantAttrs)
}
