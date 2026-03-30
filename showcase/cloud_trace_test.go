package showcase

import (
	"context"
	"os"
	"testing"
	"time"

	trace "cloud.google.com/go/trace/apiv1"
	"cloud.google.com/go/trace/apiv1/tracepb"
	showcase "github.com/googleapis/gapic-showcase/client"
	showcasepb "github.com/googleapis/gapic-showcase/server/genproto"
	gax "github.com/googleapis/gax-go/v2"
	"go.opentelemetry.io/contrib/detectors/gcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/credentials/oauth"
)

func TestObservability_Tracing_CloudTrace_Integration(t *testing.T) {
	ctx := context.Background()
	creds, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		t.Skipf("Skipping Cloud Trace integration test: %v", err)
	}
	projectID := os.Getenv("GCLOUD_TESTS_GOLANG_PROJECT_ID")
	if projectID == "" {
		projectID = creds.ProjectID
	}
	if projectID == "" {
		t.Skip("Skipping Cloud Trace integration test: no project ID found in GCLOUD_TESTS_GOLANG_PROJECT_ID or default credentials")
	}

	gax.TestOnlyResetIsFeatureEnabled()
	t.Cleanup(gax.TestOnlyResetIsFeatureEnabled)
	os.Setenv("GOOGLE_SDK_GO_EXPERIMENTAL_TRACING", "true")
	t.Cleanup(func() { os.Unsetenv("GOOGLE_SDK_GO_EXPERIMENTAL_TRACING") })

	// Set OTEL_RESOURCE_ATTRIBUTES with project_id for the telemetry endpoint
	os.Setenv("OTEL_RESOURCE_ATTRIBUTES", "gcp.project_id="+projectID)
	t.Cleanup(func() { os.Unsetenv("OTEL_RESOURCE_ATTRIBUTES") })

	// The telemetry endpoint requires a quota project when using ADC user credentials
	os.Setenv("GOOGLE_CLOUD_QUOTA_PROJECT", projectID)
	t.Cleanup(func() { os.Unsetenv("GOOGLE_CLOUD_QUOTA_PROJECT") })

	grpcCreds, err := oauth.NewApplicationDefault(ctx)
	if err != nil {
		t.Fatalf("failed to create gRPC credentials: %v", err)
	}

	// Initialize the OTLP exporter to point to telemetry.googleapis.com
	exp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint("telemetry.googleapis.com:443"),
		otlptracegrpc.WithDialOption(grpc.WithPerRPCCredentials(grpcCreds)),
		otlptracegrpc.WithTLSCredentials(credentials.NewClientTLSFromCert(nil, "")),
		otlptracegrpc.WithHeaders(map[string]string{"x-goog-user-project": projectID}),
	)
	if err != nil {
		t.Fatalf("failed to create OTLP exporter: %v", err)
	}

	res, err := resource.New(ctx,
		resource.WithDetectors(gcp.NewDetector()),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String("test-app"),
		),
	)
	if err != nil {
		t.Fatalf("failed to create resource: %v", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	oldTP := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(oldTP) })
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		tp.Shutdown(ctx)
	})

	grpcClientOpts := []option.ClientOption{
		option.WithEndpoint("127.0.0.1:7469"),
		option.WithTokenSource(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "dummy-token"})),
		option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
	}

	echoClient, err := showcase.NewEchoClient(ctx, grpcClientOpts...)
	if err != nil {
		t.Fatalf("failed to create echo client: %v", err)
	}
	t.Cleanup(func() { echoClient.Close() })

	// Start a trace
	ctx, span := otel.Tracer("test-tracer").Start(context.Background(), "APP")
	traceID := span.SpanContext().TraceID()

	// Call an RPC that succeeds
	_, err = echoClient.Echo(ctx, &showcasepb.EchoRequest{
		Response: &showcasepb.EchoRequest_Content{
			Content: "hello from cloud trace test",
		},
	})
	if err != nil {
		t.Fatalf("Echo RPC failed: %v", err)
	}
	span.End()

	// Force flush the provider to ensure traces are exported
	ctxFlush, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tp.ForceFlush(ctxFlush); err != nil {
		t.Fatalf("failed to flush provider: %v", err)
	}

	// Verify trace exists in Cloud Trace API
	traceClient, err := trace.NewClient(ctx)
	if err != nil {
		t.Fatalf("failed to create trace client: %v", err)
	}
	t.Cleanup(func() { traceClient.Close() })

	traceIDStr := traceID.String()
	t.Logf("Looking for trace %s in project %s", traceIDStr, projectID)

	var found bool
	for i := 0; i < 15; i++ {
		req := &tracepb.GetTraceRequest{
			ProjectId: projectID,
			TraceId:   traceIDStr,
		}
		_, err := traceClient.GetTrace(ctx, req)
		if err == nil {
			found = true
			break
		}
		t.Logf("Attempt %d: trace %s not found yet, retrying...", i+1, traceIDStr)
		time.Sleep(2 * time.Second)
	}

	if !found {
		t.Errorf("Trace %s was not found in Cloud Trace backend", traceIDStr)
	}
}
