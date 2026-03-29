package showcase

import (
	"context"
	"os"
	"testing"
	"time"

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

	requests := fix.traceServer.getRequests()
	if len(requests) == 0 {
		t.Fatalf("expected to receive trace exports, got none")
	}

	var clientSpanFound bool
	var artifactAttrFound bool
	for _, req := range requests {
		for _, rs := range req.ResourceSpans {
			for _, ss := range rs.ScopeSpans {
				for _, s := range ss.Spans {
					if s.Name == "google.showcase.v1beta1.Echo/Echo" {
						clientSpanFound = true
						t.Logf("Found client span: %v", s.Name)
						for _, kv := range s.Attributes {
							t.Logf("Span attribute: %q = %v", kv.Key, kv.Value.GetStringValue())
							if kv.Key == "gcp.client.artifact" {
								artifactAttrFound = true
								expectedArtifact := "github.com/googleapis/gapic-showcase/client"
								if kv.Value.GetStringValue() != expectedArtifact {
									t.Errorf("expected gcp.client.artifact to be %q, got %q", expectedArtifact, kv.Value.GetStringValue())
								}
							}
						}
					}
				}
			}
		}
	}

	if !clientSpanFound {
		t.Errorf("did not find the expected client span")
	}
	if !artifactAttrFound {
		t.Errorf("did not find the gcp.client.artifact attribute in the client span")
	}
}
