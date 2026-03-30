package showcase

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	showcase "github.com/googleapis/gapic-showcase/client"
	gax "github.com/googleapis/gax-go/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
)

func setupTracingTest(t *testing.T, enableTracing bool) (*observabilityFixture, []option.ClientOption) {
	// Reset feature cache just in case something else evaluated it
	gax.TestOnlyResetIsFeatureEnabled()
	t.Cleanup(gax.TestOnlyResetIsFeatureEnabled)
	
	if enableTracing {
		os.Setenv("GOOGLE_SDK_GO_EXPERIMENTAL_TRACING", "true")
	} else {
		os.Setenv("GOOGLE_SDK_GO_EXPERIMENTAL_TRACING", "false")
	}
	t.Cleanup(func() { os.Unsetenv("GOOGLE_SDK_GO_EXPERIMENTAL_TRACING") })

	fix := setupObservabilityFixture(t)
	oldTP := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(oldTP) })
	otel.SetTracerProvider(fix.provider)

	// Create a new client to ensure it picks up the OTel provider and env vars
	grpcClientOpts := []option.ClientOption{
		option.WithEndpoint("127.0.0.1:7469"),
		option.WithTokenSource(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "dummy-token"})),
		option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
	}

	return fix, grpcClientOpts
}

func verifyInMemorySpan(t *testing.T, fix *observabilityFixture, expectedName string, traceID trace.TraceID, wantAttrs map[string]any) {
	t.Helper()

	// Force flush the provider to ensure traces are exported
	ctxFlush, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := fix.provider.ForceFlush(ctxFlush); err != nil {
		t.Fatalf("failed to flush provider: %v", err)
	}

	// Give a little time for the gRPC export to arrive
	time.Sleep(100 * time.Millisecond)

	spans := fix.traceServer.GetCapturedSpans()
	if len(spans) == 0 {
		t.Fatalf("expected to receive trace exports, got none")
	}

	var gotSpan *CapturedSpan
	for _, s := range spans {
		if string(s.TraceID) == string(traceID[:]) && s.Name == expectedName {
			gotSpan = &s
			break
		}
	}

	if gotSpan == nil {
		t.Fatalf("did not find the expected client span")
	}

	// TODO: The instrumentation scope should be the artifact name ("github.com/googleapis/gapic-showcase/client"), 
	// but it is currently the otelgrpc scope because the underlying transport hardcodes it.
	expectedScope := "go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	if gotSpan.Scope != expectedScope {
		t.Errorf("expected span scope to be %q, got %q", expectedScope, gotSpan.Scope)
	}

	if wantAttrs != nil {
		if _, ok := gotSpan.Attributes["gcp.client.version"]; ok {
			gotSpan.Attributes["gcp.client.version"] = "DYNAMIC"
		}

		if diff := cmp.Diff(wantAttrs, gotSpan.Attributes); diff != "" {
			t.Errorf("Client span attributes mismatch (-want +got):\n%s", diff)
		}
	}
}

func TestObservability_Tracing_Success(t *testing.T) {
	fix, clientOpts := setupTracingTest(t, true)
	ctx := context.Background()
	seqClient, err := showcase.NewSequenceClient(ctx, clientOpts...)
	if err != nil {
		t.Fatalf("failed to create sequence client: %v", err)
	}
	t.Cleanup(func() { seqClient.Close() })

	ctxSpan, span := otel.Tracer("test-tracer").Start(ctx, "APP")
	_ = runTracingSuccessScenario(ctxSpan, t, seqClient)
	span.End()
	traceID := span.SpanContext().TraceID()

	wantAttrs := map[string]any{
		"gcp.client.artifact":         "github.com/googleapis/gapic-showcase/client",
		// TODO: gcp.client.language is [removed] from requirements (present in telemetry.sdk.language).
		"gcp.client.language":         "go",
		"gcp.client.repo":             "googleapis/google-cloud-go",
		"gcp.client.service":          "showcase",
		"gcp.client.version":          "DYNAMIC",
		"gcp.grpc.resend_count":       int64(0),
		// TODO: gcp.resource.destination.id should be populated from the resource_reference, but currently is not emitted by the generator.
		// "gcp.resource.destination.id": seq.GetName(),
		// TODO: rpc.grpc.status_code is [deleted] in OTel SemConv 1.39 (use rpc.response.status_code).
		"rpc.grpc.status_code":        int64(0),
		// TODO: rpc.method should be [modified] to be fully-qualified "$serviceName/$method".
		"rpc.method":                  "AttemptSequence",
		"rpc.response.status_code":    "OK",
		// TODO: rpc.service is [deleted] in OTel SemConv 1.39.
		"rpc.service":                 "google.showcase.v1beta1.SequenceService",
		// TODO: rpc.system is [moved] to rpc.system.name in OTel SemConv 1.39.
		"rpc.system":                  "grpc",
		"server.address":              "127.0.0.1",
		"server.port":                 int64(7469),
		"url.domain":                  "showcase.googleapis.com",
	}

	verifyInMemorySpan(t, fix, "google.showcase.v1beta1.SequenceService/AttemptSequence", traceID, wantAttrs)
}

func TestObservability_Tracing_Failure(t *testing.T) {
	fix, clientOpts := setupTracingTest(t, true)
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
		"exception.type":           "*status.Error",
		"gcp.client.artifact":      "github.com/googleapis/gapic-showcase/client",
		// TODO: gcp.client.language is [removed] from requirements (present in telemetry.sdk.language).
		"gcp.client.language":      "go",
		"gcp.client.repo":          "googleapis/google-cloud-go",
		"gcp.client.service":       "showcase",
		"gcp.client.version":       "DYNAMIC",
		"gcp.grpc.resend_count":    int64(0),
		// TODO: gcp.resource.destination.id should be populated from the resource_reference, but currently is not emitted by the generator.
		// "gcp.resource.destination.id": seq.GetName(),
		// TODO: rpc.grpc.status_code is [deleted] in OTel SemConv 1.39 (use rpc.response.status_code).
		"rpc.grpc.status_code":     int64(codes.NotFound),
		// TODO: rpc.method should be [modified] to be fully-qualified "$serviceName/$method".
		"rpc.method":               "AttemptSequence",
		"rpc.response.status_code": "NOT_FOUND",
		// TODO: rpc.service is [deleted] in OTel SemConv 1.39.
		"rpc.service":              "google.showcase.v1beta1.SequenceService",
		// TODO: rpc.system is [moved] to rpc.system.name in OTel SemConv 1.39.
		"rpc.system":               "grpc",
		"server.address":           "127.0.0.1",
		"server.port":              int64(7469),
		"status.message":           "not found",
		"url.domain":               "showcase.googleapis.com",
	}

	verifyInMemorySpan(t, fix, "google.showcase.v1beta1.SequenceService/AttemptSequence", traceID, wantAttrs)
}

func TestObservability_Tracing_ClientFailure(t *testing.T) {
	fix, clientOpts := setupTracingTest(t, true)
	ctx := context.Background()
	seqClient, err := showcase.NewSequenceClient(ctx, clientOpts...)
	if err != nil {
		t.Fatalf("failed to create sequence client: %v", err)
	}
	t.Cleanup(func() { seqClient.Close() })

	ctxSpan, span := otel.Tracer("test-tracer").Start(ctx, "APP")
	_ = runTracingClientFailureScenario(ctxSpan, t, seqClient)
	span.End()
	traceID := span.SpanContext().TraceID()

	wantAttrs := map[string]any{
		"error.type":               "CLIENT_TIMEOUT",
		"exception.type":           "*status.Error",
		"gcp.client.artifact":      "github.com/googleapis/gapic-showcase/client",
		// TODO: gcp.client.language is [removed] from requirements (present in telemetry.sdk.language).
		"gcp.client.language":      "go",
		"gcp.client.repo":          "googleapis/google-cloud-go",
		"gcp.client.service":       "showcase",
		"gcp.client.version":       "DYNAMIC",
		"gcp.grpc.resend_count":    int64(0),
		// TODO: gcp.resource.destination.id should be populated from the resource_reference, but currently is not emitted by the generator.
		// "gcp.resource.destination.id": seq.GetName(),
		// TODO: rpc.grpc.status_code is [deleted] in OTel SemConv 1.39 (use rpc.response.status_code).
		"rpc.grpc.status_code":     int64(codes.DeadlineExceeded),
		// TODO: rpc.method should be [modified] to be fully-qualified "$serviceName/$method".
		"rpc.method":               "AttemptSequence",
		"rpc.response.status_code": "DEADLINE_EXCEEDED",
		// TODO: rpc.service is [deleted] in OTel SemConv 1.39.
		"rpc.service":              "google.showcase.v1beta1.SequenceService",
		// TODO: rpc.system is [moved] to rpc.system.name in OTel SemConv 1.39.
		"rpc.system":               "grpc",
		"server.address":           "127.0.0.1",
		"server.port":              int64(7469),
		"status.message":           "context deadline exceeded",
		"url.domain":               "showcase.googleapis.com",
	}

	verifyInMemorySpan(t, fix, "google.showcase.v1beta1.SequenceService/AttemptSequence", traceID, wantAttrs)
}

func TestObservability_Tracing_Disablement(t *testing.T) {
	fix, clientOpts := setupTracingTest(t, false)
	ctx := context.Background()
	echoClient, err := showcase.NewEchoClient(ctx, clientOpts...)
	if err != nil {
		t.Fatalf("failed to create echo client: %v", err)
	}
	t.Cleanup(func() { echoClient.Close() })

	ctxSpan, span := otel.Tracer("test-tracer").Start(context.Background(), "APP")
	runTracingDisablementScenario(ctxSpan, t, echoClient)
	span.End()
	traceID := span.SpanContext().TraceID()

	ctxFlush, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := fix.provider.ForceFlush(ctxFlush); err != nil {
		t.Fatalf("failed to flush provider: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	spans := fix.traceServer.GetCapturedSpans()
	var gotSpan *CapturedSpan
	for _, s := range spans {
		if string(s.TraceID) == string(traceID[:]) && s.Name == "google.showcase.v1beta1.Echo/Echo" {
			gotSpan = &s
			break
		}
	}

	if gotSpan != nil {
		if _, ok := gotSpan.Attributes["gcp.client.artifact"]; ok {
			t.Errorf("found gcp.client.artifact attribute, but tracing telemetry should be disabled")
		}
	}
}

func TestObservability_Tracing_Retry(t *testing.T) {
	fix, clientOpts := setupTracingTest(t, true)
	ctx := context.Background()

	seqClient, err := showcase.NewSequenceClient(ctx, clientOpts...)
	if err != nil {
		t.Fatalf("failed to create sequence client: %v", err)
	}
	t.Cleanup(func() { seqClient.Close() })

	ctxSpan, span := otel.Tracer("test-tracer").Start(ctx, "APP")
	_ = runTracingRetryScenario(ctxSpan, t, seqClient)
	span.End()
	traceID := span.SpanContext().TraceID()

	ctxFlush, cancelFlush := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFlush()
	if err := fix.provider.ForceFlush(ctxFlush); err != nil {
		t.Fatalf("failed to flush provider: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	spans := fix.traceServer.GetCapturedSpans()
	var attemptSpans []CapturedSpan
	for _, s := range spans {
		if string(s.TraceID) == string(traceID[:]) && s.Name == "google.showcase.v1beta1.SequenceService/AttemptSequence" {
			attemptSpans = append(attemptSpans, s)
		}
	}

	if len(attemptSpans) != 4 {
		t.Errorf("expected 4 attempt spans (3 failures + 1 success), got %d", len(attemptSpans))
	}
}
