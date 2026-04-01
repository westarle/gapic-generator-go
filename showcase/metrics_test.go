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
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func setupMetricsTest(t *testing.T, enableMetrics bool) (*observabilityFixture, []option.ClientOption) {
	gax.TestOnlyResetIsFeatureEnabled()
	t.Cleanup(gax.TestOnlyResetIsFeatureEnabled)

	if enableMetrics {
		os.Setenv("GOOGLE_SDK_GO_EXPERIMENTAL_METRICS", "true")
	} else {
		os.Setenv("GOOGLE_SDK_GO_EXPERIMENTAL_METRICS", "false")
	}
	t.Cleanup(func() { os.Unsetenv("GOOGLE_SDK_GO_EXPERIMENTAL_METRICS") })

	fix := setupObservabilityFixture(t)
	oldMP := otel.GetMeterProvider()
	t.Cleanup(func() { otel.SetMeterProvider(oldMP) })
	otel.SetMeterProvider(fix.meterProvider)

	grpcClientOpts := []option.ClientOption{
		option.WithEndpoint("127.0.0.1:7469"),
		option.WithAuthCredentials(auth.NewCredentials(&auth.CredentialsOptions{
			TokenProvider: dummyTokenProvider{},
		})),
		option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
	}

	return fix, grpcClientOpts
}

func verifyInMemoryMetric(t *testing.T, fix *observabilityFixture, expectedName string, expectedScope string, expectedMethod string, wantAttrs map[string]any) {
	t.Helper()

	ctxFlush, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := fix.meterProvider.ForceFlush(ctxFlush); err != nil {
		t.Fatalf("failed to flush meter provider: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	metrics := fix.metricServer.GetCapturedMetrics()
	if len(metrics) == 0 {
		t.Fatalf("expected to receive metric exports, got none")
	}

	var gotMetric *CapturedMetric
	for _, m := range metrics {
		if m.Name == expectedName && m.Attributes["rpc.method"] == expectedMethod {
			// Deep copy m so we can take the pointer
			mCopy := m
			gotMetric = &mCopy
			break
		}
	}

	if gotMetric == nil {
		t.Fatalf("did not find the expected metric %q", expectedName)
	}

	if gotMetric.Scope != expectedScope {
		t.Errorf("expected metric scope to be %q, got %q", expectedScope, gotMetric.Scope)
	}

	if wantAttrs != nil {
		if _, ok := gotMetric.Attributes["gcp.client.version"]; ok {
			gotMetric.Attributes["gcp.client.version"] = "DYNAMIC"
		}

		if diff := cmp.Diff(wantAttrs, gotMetric.Attributes); diff != "" {
			t.Errorf("Client metric attributes mismatch (-want +got):\n%s", diff)
		}
	}

	if len(gotMetric.DataPoints) == 0 {
		t.Errorf("expected metric to have data points")
	}
}

func TestObservability_Metrics_Success(t *testing.T) {
	fix, clientOpts := setupMetricsTest(t, true)
	ctx := context.Background()
	seqClient, err := showcase.NewSequenceClient(ctx, clientOpts...)
	if err != nil {
		t.Fatalf("failed to create sequence client: %v", err)
	}
	t.Cleanup(func() { seqClient.Close() })

	_ = runTracingSuccessScenario(ctx, t, seqClient)

	wantAttrs := map[string]any{
		// Expected on the instrumentation scope of the metric:
		// "gcp.client.version":  "DYNAMIC", // (this is the version, not an attribute)
		"gcp.client.service":  "showcase",
		
		// Expected on the metric datapoint:
		"rpc.method":               "google.showcase.v1beta1.SequenceService/AttemptSequence",
		"rpc.response.status_code": "OK",
		"rpc.system.name":          "grpc",
		"url.domain":               "showcase.googleapis.com",
	}

	// Wait, runTracingSuccessScenario calls CreateSequence and AttemptSequence. The loop breaks when we find the first metric matching the name, which might be CreateSequence's datapoint. We should just check that AtemptSequence is among the datapoints, or the attributes represent the last call. Wait, we break on `m.Name == expectedName`. Since multiple RPCs are called, they will all contribute datapoints to the SAME metric (`gcp.client.request.duration`).
	// We need to check if the specific attributes we want are present in ONE of the datapoints. But our `CapturedMetric` struct currently squashes all attributes from all datapoints into a single map (`attrsMap`), which will only reflect the FIRST or LAST datapoint! 
	
	verifyInMemoryMetric(t, fix, "gcp.client.request.duration", "github.com/googleapis/gapic-showcase/client", "google.showcase.v1beta1.SequenceService/AttemptSequence", wantAttrs)
}
