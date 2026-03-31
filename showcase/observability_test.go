package showcase

import (
	"context"
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
	showcase "github.com/googleapis/gapic-showcase/client"
	gax "github.com/googleapis/gax-go/v2"
	"go.opentelemetry.io/otel"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func setupTracingTest(t *testing.T, enableTracing bool) (*observabilityFixture, []option.ClientOption, []option.ClientOption) {
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
	t.Cleanup(func() { fix.Close() })
	oldTP := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(oldTP) })
	otel.SetTracerProvider(fix.provider)

	// Create a new client to ensure it picks up the OTel provider and env vars
	grpcClientOpts := []option.ClientOption{
		option.WithEndpoint("127.0.0.1:7469"),
		option.WithTokenSource(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "dummy-token"})),
		option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
	}

	restClientOpts := []option.ClientOption{
		option.WithEndpoint("http://127.0.0.1:7469"),
		option.WithTokenSource(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "dummy-token"})),
	}

	return fix, grpcClientOpts, restClientOpts
}

func TestObservability_Tracing_Success(t *testing.T) {
	fix, grpcOpts, _ := setupTracingTest(t, true)
	ctx := context.Background()

	grpcClient, err := showcase.NewEchoClient(ctx, grpcOpts...)
	if err != nil {
		t.Fatalf("failed to create grpc echo client: %v", err)
	}
	t.Cleanup(func() { grpcClient.Close() })

	ctxSpan, span := otel.Tracer("test-tracer").Start(context.Background(), "APP")

	runTracingSuccessScenario(ctxSpan, t, grpcClient)
	span.End()

	expectedName := "google.showcase.v1beta1.Echo/Echo"
	gotSpan := fix.FindSpan(t, span.SpanContext().TraceID(), expectedName)

	// TODO: The instrumentation scope should be the artifact name ("github.com/googleapis/gapic-showcase/client"), 
	// but it is currently the otelgrpc scope because the underlying transport hardcodes it.
	expectedScope := "go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	if gotSpan.Scope != expectedScope {
		t.Errorf("expected span scope to be %q, got %q", expectedScope, gotSpan.Scope)
	}

	wantAttrs := map[string]any{
		"gcp.client.artifact":      "github.com/googleapis/gapic-showcase/client",
		// TODO: gcp.client.language is [removed] from requirements (present in telemetry.sdk.language).
		"gcp.client.language":      "go",
		"gcp.client.repo":          "googleapis/google-cloud-go",
		"gcp.client.service":       "showcase",
		"gcp.client.version":       "DYNAMIC",
		"gcp.grpc.resend_count":    int64(0),
		// TODO: rpc.grpc.status_code is [deleted] in OTel SemConv 1.39 (use rpc.response.status_code).
		"rpc.grpc.status_code":     int64(0),
		// TODO: rpc.method should be [modified] to be fully-qualified "$serviceName/$method".
		"rpc.method":               "Echo",
		"rpc.response.status_code": "OK",
		// TODO: rpc.service is [deleted] in OTel SemConv 1.39.
		"rpc.service":              "google.showcase.v1beta1.Echo",
		// TODO: rpc.system is [moved] to rpc.system.name in OTel SemConv 1.39.
		"rpc.system":               "grpc",
		"server.address":           "127.0.0.1",
		"server.port":              int64(7469),
		"url.domain":               "showcase.googleapis.com",
	}

	if _, ok := gotSpan.Attributes["gcp.client.version"]; ok {
		gotSpan.Attributes["gcp.client.version"] = "DYNAMIC"
	}

	if diff := cmp.Diff(wantAttrs, gotSpan.Attributes); diff != "" {
		t.Errorf("Client span attributes mismatch (-want +got):\n%s", diff)
	}
}

func TestObservability_Tracing_SuccessREST(t *testing.T) {
	fix, _, restOpts := setupTracingTest(t, true)
	ctx := context.Background()

	restClient, err := showcase.NewEchoRESTClient(ctx, restOpts...)
	if err != nil {
		t.Fatalf("failed to create rest echo client: %v", err)
	}
	t.Cleanup(func() { restClient.Close() })

	ctxSpan, span := otel.Tracer("test-tracer").Start(context.Background(), "APP")

	runTracingSuccessScenario(ctxSpan, t, restClient)
	span.End()

	expectedName := "POST /v1beta1/echo:echo"
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
		"http.response.status_code": int64(200),
		"network.protocol.version":  "1.1",
		"server.address":            "127.0.0.1",
		"server.port":               int64(7469),
		"url.domain":                "showcase.googleapis.com",
		"url.full":                  "http://127.0.0.1:7469/v1beta1/echo:echo?%24alt=json%3Benum-encoding%3Dint",
		"url.template":              "/v1beta1/echo:echo",
	}

	if _, ok := gotSpan.Attributes["gcp.client.version"]; ok {
		gotSpan.Attributes["gcp.client.version"] = "DYNAMIC"
	}

	if diff := cmp.Diff(wantAttrs, gotSpan.Attributes); diff != "" {
		t.Errorf("Client span attributes mismatch (-want +got):\n%s", diff)
	}
}
