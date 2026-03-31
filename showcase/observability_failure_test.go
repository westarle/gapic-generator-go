package showcase

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	showcase "github.com/googleapis/gapic-showcase/client"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc/codes"
)

func TestObservability_Tracing_ServerFailure(t *testing.T) {
	fix, grpcOpts, _ := setupTracingTest(t, true)
	ctx := context.Background()

	grpcClient, err := showcase.NewSequenceClient(ctx, grpcOpts...)
	if err != nil {
		t.Fatalf("failed to create grpc sequence client: %v", err)
	}
	t.Cleanup(func() { grpcClient.Close() })

	ctxSpan, span := otel.Tracer("test-tracer").Start(context.Background(), "APP")
	_ = runTracingServerFailureScenario(ctxSpan, t, grpcClient)
	span.End()

	expectedName := "google.showcase.v1beta1.SequenceService/AttemptSequence"
	gotSpan := fix.FindSpan(t, span.SpanContext().TraceID(), expectedName)

	expectedScope := "go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	if gotSpan.Scope != expectedScope {
		t.Errorf("expected span scope to be %q, got %q", expectedScope, gotSpan.Scope)
	}

	wantAttrs := map[string]any{
		"error.type":               "NOT_FOUND",
		"exception.type":           "*status.Error",
		"gcp.client.artifact":      "github.com/googleapis/gapic-showcase/client",
		"gcp.client.language":      "go",
		"gcp.client.repo":          "googleapis/google-cloud-go",
		"gcp.client.service":       "showcase",
		"gcp.client.version":       "DYNAMIC",
		"gcp.grpc.resend_count":    int64(0),
		"rpc.grpc.status_code":     int64(codes.NotFound),
		"rpc.method":               "AttemptSequence",
		"rpc.response.status_code": "NOT_FOUND",
		"rpc.service":              "google.showcase.v1beta1.SequenceService",
		"rpc.system":               "grpc",
		"server.address":           "127.0.0.1",
		"server.port":              int64(7469),
		"status.message":           "not found",
		"url.domain":               "showcase.googleapis.com",
	}

	if _, ok := gotSpan.Attributes["gcp.client.version"]; ok {
		gotSpan.Attributes["gcp.client.version"] = "DYNAMIC"
	}

	if diff := cmp.Diff(wantAttrs, gotSpan.Attributes); diff != "" {
		t.Errorf("Client span attributes mismatch (-want +got):\n%s", diff)
	}
}

func TestObservability_Tracing_ServerFailureREST(t *testing.T) {
	fix, _, restOpts := setupTracingTest(t, true)
	ctx := context.Background()

	restClient, err := showcase.NewSequenceRESTClient(ctx, restOpts...)
	if err != nil {
		t.Fatalf("failed to create rest sequence client: %v", err)
	}
	t.Cleanup(func() { restClient.Close() })

	ctxSpan, span := otel.Tracer("test-tracer").Start(context.Background(), "APP")
	_ = runTracingServerFailureScenario(ctxSpan, t, restClient)
	span.End()

	expectedName := "POST /v1beta1/{name=sequences/*}"
	gotSpan := fix.FindSpan(t, span.SpanContext().TraceID(), expectedName)

	expectedScope := "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	if gotSpan.Scope != expectedScope {
		t.Errorf("expected span scope to be %q, got %q", expectedScope, gotSpan.Scope)
	}

	wantAttrs := map[string]any{
		"gcp.client.artifact":       "github.com/googleapis/gapic-showcase/client",
		"gcp.client.language":       "go",
		"gcp.client.repo":           "googleapis/google-cloud-go",
		"gcp.client.service":        "showcase",
		"gcp.client.version":        "DYNAMIC",
		"rpc.system.name":           "http",
		"http.request.method":       "POST",
		"http.request.resend_count": int64(0),
		"http.response.status_code": int64(404),
		"network.protocol.version":  "1.1",
		"server.address":            "127.0.0.1",
		"server.port":               int64(7469),
		"url.domain":                "showcase.googleapis.com",
		"url.template":              "/v1beta1/{name=sequences/*}",
	}

	if _, ok := gotSpan.Attributes["gcp.client.version"]; ok {
		gotSpan.Attributes["gcp.client.version"] = "DYNAMIC"
	}
	
	// Remove dynamic url.full
	delete(gotSpan.Attributes, "url.full")
	delete(gotSpan.Attributes, "error.type")
	delete(gotSpan.Attributes, "exception.type")
	delete(gotSpan.Attributes, "status.message")

	if diff := cmp.Diff(wantAttrs, gotSpan.Attributes); diff != "" {
		t.Errorf("Client span attributes mismatch (-want +got):\n%s", diff)
	}
}

func TestObservability_Tracing_ClientFailure(t *testing.T) {
	fix, grpcOpts, _ := setupTracingTest(t, true)
	ctx := context.Background()

	grpcClient, err := showcase.NewSequenceClient(ctx, grpcOpts...)
	if err != nil {
		t.Fatalf("failed to create grpc sequence client: %v", err)
	}
	t.Cleanup(func() { grpcClient.Close() })

	ctxSpan, span := otel.Tracer("test-tracer").Start(context.Background(), "APP")
	_ = runTracingClientFailureScenario(ctxSpan, t, grpcClient)
	span.End()

	expectedName := "google.showcase.v1beta1.SequenceService/AttemptSequence"
	gotSpan := fix.FindSpan(t, span.SpanContext().TraceID(), expectedName)

	expectedScope := "go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	if gotSpan.Scope != expectedScope {
		t.Errorf("expected span scope to be %q, got %q", expectedScope, gotSpan.Scope)
	}

	wantAttrs := map[string]any{
		"error.type":               "CLIENT_TIMEOUT",
		"exception.type":           "*status.Error",
		"gcp.client.artifact":      "github.com/googleapis/gapic-showcase/client",
		"gcp.client.language":      "go",
		"gcp.client.repo":          "googleapis/google-cloud-go",
		"gcp.client.service":       "showcase",
		"gcp.client.version":       "DYNAMIC",
		"gcp.grpc.resend_count":    int64(0),
		"rpc.grpc.status_code":     int64(codes.DeadlineExceeded),
		"rpc.method":               "AttemptSequence",
		"rpc.response.status_code": "DEADLINE_EXCEEDED",
		"rpc.service":              "google.showcase.v1beta1.SequenceService",
		"rpc.system":               "grpc",
		"server.address":           "127.0.0.1",
		"server.port":              int64(7469),
		"status.message":           "context deadline exceeded",
		"url.domain":               "showcase.googleapis.com",
	}

	if _, ok := gotSpan.Attributes["gcp.client.version"]; ok {
		gotSpan.Attributes["gcp.client.version"] = "DYNAMIC"
	}

	if diff := cmp.Diff(wantAttrs, gotSpan.Attributes); diff != "" {
		t.Errorf("Client span attributes mismatch (-want +got):\n%s", diff)
	}
}

func TestObservability_Tracing_ClientFailureREST(t *testing.T) {
	fix, _, restOpts := setupTracingTest(t, true)
	ctx := context.Background()

	restClient, err := showcase.NewSequenceRESTClient(ctx, restOpts...)
	if err != nil {
		t.Fatalf("failed to create rest sequence client: %v", err)
	}
	t.Cleanup(func() { restClient.Close() })

	ctxSpan, span := otel.Tracer("test-tracer").Start(context.Background(), "APP")
	_ = runTracingClientFailureScenario(ctxSpan, t, restClient)
	span.End()

	expectedName := "POST /v1beta1/{name=sequences/*}"
	gotSpan := fix.FindSpan(t, span.SpanContext().TraceID(), expectedName)

	expectedScope := "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	if gotSpan.Scope != expectedScope {
		t.Errorf("expected span scope to be %q, got %q", expectedScope, gotSpan.Scope)
	}

	wantAttrs := map[string]any{
		"gcp.client.artifact":       "github.com/googleapis/gapic-showcase/client",
		"gcp.client.language":       "go",
		"gcp.client.repo":           "googleapis/google-cloud-go",
		"gcp.client.service":        "showcase",
		"gcp.client.version":        "DYNAMIC",
		"rpc.system.name":           "http",
		"http.request.method":       "POST",
		"http.request.resend_count": int64(0),
		"network.protocol.version":  "1.1",
		"server.address":            "127.0.0.1",
		"server.port":               int64(7469),
		"url.domain":                "showcase.googleapis.com",
		"url.template":              "/v1beta1/{name=sequences/*}",
	}

	if _, ok := gotSpan.Attributes["gcp.client.version"]; ok {
		gotSpan.Attributes["gcp.client.version"] = "DYNAMIC"
	}
	
	delete(gotSpan.Attributes, "url.full")
	delete(gotSpan.Attributes, "error.type")
	delete(gotSpan.Attributes, "exception.type")
	delete(gotSpan.Attributes, "status.message")
	delete(gotSpan.Attributes, "http.response.status_code")

	if diff := cmp.Diff(wantAttrs, gotSpan.Attributes); diff != "" {
		t.Errorf("Client span attributes mismatch (-want +got):\n%s", diff)
	}
}

func TestObservability_Tracing_Disablement(t *testing.T) {
	fix, grpcOpts, _ := setupTracingTest(t, false)
	ctx := context.Background()

	grpcClient, err := showcase.NewEchoClient(ctx, grpcOpts...)
	if err != nil {
		t.Fatalf("failed to create grpc echo client: %v", err)
	}
	t.Cleanup(func() { grpcClient.Close() })

	ctxSpan, span := otel.Tracer("test-tracer").Start(context.Background(), "APP")
	runTracingDisablementScenario(ctxSpan, t, grpcClient)
	span.End()
	traceID := span.SpanContext().TraceID()

	ctxFlush, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := fix.provider.ForceFlush(ctxFlush); err != nil {
		t.Fatalf("failed to flush provider: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	spans := fix.traceServer.GetCapturedSpans()
	expectedName := "google.showcase.v1beta1.Echo/Echo"
	
	var gotSpan *CapturedSpan
	for _, s := range spans {
		if string(s.TraceID) == string(traceID[:]) && s.Name == expectedName {
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

func TestObservability_Tracing_DisablementREST(t *testing.T) {
	fix, _, restOpts := setupTracingTest(t, false)
	ctx := context.Background()

	restClient, err := showcase.NewEchoRESTClient(ctx, restOpts...)
	if err != nil {
		t.Fatalf("failed to create rest echo client: %v", err)
	}
	t.Cleanup(func() { restClient.Close() })

	ctxSpan, span := otel.Tracer("test-tracer").Start(context.Background(), "APP")
	runTracingDisablementScenario(ctxSpan, t, restClient)
	span.End()
	traceID := span.SpanContext().TraceID()

	ctxFlush, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := fix.provider.ForceFlush(ctxFlush); err != nil {
		t.Fatalf("failed to flush provider: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	spans := fix.traceServer.GetCapturedSpans()
	expectedName := "POST /v1beta1/echo:echo"
	
	var gotSpan *CapturedSpan
	for _, s := range spans {
		if string(s.TraceID) == string(traceID[:]) && s.Name == expectedName {
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
	fix, grpcOpts, _ := setupTracingTest(t, true)
	ctx := context.Background()

	grpcClient, err := showcase.NewSequenceClient(ctx, grpcOpts...)
	if err != nil {
		t.Fatalf("failed to create grpc sequence client: %v", err)
	}
	t.Cleanup(func() { grpcClient.Close() })

	ctxSpan, span := otel.Tracer("test-tracer").Start(context.Background(), "APP")
	_ = runTracingRetryScenario(ctxSpan, t, grpcClient)
	span.End()
	traceID := span.SpanContext().TraceID()

	ctxFlush, cancelFlush := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFlush()
	if err := fix.provider.ForceFlush(ctxFlush); err != nil {
		t.Fatalf("failed to flush provider: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	spans := fix.traceServer.GetCapturedSpans()
	expectedName := "google.showcase.v1beta1.SequenceService/AttemptSequence"
	
	var attemptSpans []CapturedSpan
	for _, s := range spans {
		if string(s.TraceID) == string(traceID[:]) && s.Name == expectedName {
			attemptSpans = append(attemptSpans, s)
		}
	}

	if len(attemptSpans) != 4 {
		t.Errorf("expected 4 attempt spans (3 failures + 1 success), got %d", len(attemptSpans))
	}
}

func TestObservability_Tracing_RetryREST(t *testing.T) {
	fix, _, restOpts := setupTracingTest(t, true)
	ctx := context.Background()

	restClient, err := showcase.NewSequenceRESTClient(ctx, restOpts...)
	if err != nil {
		t.Fatalf("failed to create rest sequence client: %v", err)
	}
	t.Cleanup(func() { restClient.Close() })

	ctxSpan, span := otel.Tracer("test-tracer").Start(context.Background(), "APP")
	_ = runTracingRetryScenario(ctxSpan, t, restClient)
	span.End()
	traceID := span.SpanContext().TraceID()

	ctxFlush, cancelFlush := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFlush()
	if err := fix.provider.ForceFlush(ctxFlush); err != nil {
		t.Fatalf("failed to flush provider: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	spans := fix.traceServer.GetCapturedSpans()
	expectedName := "POST /v1beta1/{name=sequences/*}"
	
	var attemptSpans []CapturedSpan
	for _, s := range spans {
		if string(s.TraceID) == string(traceID[:]) && s.Name == expectedName {
			attemptSpans = append(attemptSpans, s)
		}
	}

	if len(attemptSpans) != 4 {
		t.Errorf("expected 4 attempt spans (3 failures + 1 success), got %d", len(attemptSpans))
	}
}
