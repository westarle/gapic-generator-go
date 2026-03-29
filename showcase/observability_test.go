package showcase

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	showcase "github.com/googleapis/gapic-showcase/client"
	showcasepb "github.com/googleapis/gapic-showcase/server/genproto"
	gax "github.com/googleapis/gax-go/v2"
	"go.opentelemetry.io/otel"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestObservability_Tracing_F1_2_Success(t *testing.T) {
	// Reset feature cache just in case something else evaluated it
	gax.TestOnlyResetIsFeatureEnabled()
	defer gax.TestOnlyResetIsFeatureEnabled()
	
	// F1.2: Assert T4 spans emitted properly on success.
	os.Setenv("GOOGLE_SDK_GO_EXPERIMENTAL_TRACING", "true")
	defer os.Unsetenv("GOOGLE_SDK_GO_EXPERIMENTAL_TRACING")

	fix := setupObservabilityFixture(t)

	// Create a new client to ensure it picks up the OTel provider and env vars
	grpcClientOpts := []option.ClientOption{
		option.WithEndpoint("127.0.0.1:7469"),
		option.WithTokenSource(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "dummy-token"})),
		option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
	}

	ctx := context.Background()
	echoClient, err := showcase.NewEchoClient(ctx, grpcClientOpts...)
	if err != nil {
		t.Fatalf("failed to create echo client: %v", err)
	}
	defer echoClient.Close()

	ctx, span := otel.Tracer("test-tracer").Start(ctx, "APP")

	// Call an RPC that succeeds
	_, err = echoClient.Echo(ctx, &showcasepb.EchoRequest{
		Response: &showcasepb.EchoRequest_Content{
			Content: "hello",
		},
	})
	if err != nil {
		t.Fatalf("Echo RPC failed: %v", err)
	}
	span.End()

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
		if s.Name == "google.showcase.v1beta1.Echo/Echo" {
			gotSpan = &s
			break
		}
	}

	if gotSpan == nil {
		t.Fatalf("did not find the expected client span")
	}

	wantAttrs := map[string]any{
		"gcp.client.artifact": "github.com/googleapis/gapic-showcase/client",
		"gcp.client.language": "go",
		"gcp.client.repo":     "googleapis/google-cloud-go",
		"gcp.client.service":  "showcase",
	}

	filteredGotAttrs := make(map[string]any)
	for key, val := range gotSpan.Attributes {
		if _, ok := wantAttrs[key]; ok {
			filteredGotAttrs[key] = val
		}
	}

	if diff := cmp.Diff(wantAttrs, filteredGotAttrs); diff != "" {
		t.Errorf("Client span attributes mismatch (-want +got):\n%s", diff)
	}
}
