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
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func setupMetricsTest(t *testing.T, enableMetrics bool, transport string) (*observabilityFixture, []option.ClientOption) {
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

	var clientOpts []option.ClientOption
	if transport == "grpc" {
		clientOpts = []option.ClientOption{
			option.WithEndpoint("127.0.0.1:7469"),
			option.WithAuthCredentials(auth.NewCredentials(&auth.CredentialsOptions{
				TokenProvider: dummyTokenProvider{},
			})),
			option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
		}
	} else {
		clientOpts = []option.ClientOption{
			option.WithEndpoint("http://127.0.0.1:7469"),
			option.WithTokenSource(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "dummy-token"})),
		}
	}

	return fix, clientOpts
}

func verifyInMemoryMetric(t *testing.T, fix *observabilityFixture, expectedName string, expectedScope string, expectedMethod string, wantAttrs map[string]any, unexpectedAttrs []string) {
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
			mCopy := m
			gotMetric = &mCopy
			break
		}
	}

	if gotMetric == nil {
		t.Fatalf("did not find the expected metric %q for method %q", expectedName, expectedMethod)
	}

	if gotMetric.Scope != expectedScope {
		t.Errorf("expected metric scope to be %q, got %q", expectedScope, gotMetric.Scope)
	}

	if wantAttrs != nil {
		if _, ok := gotMetric.Attributes["gcp.client.version"]; ok {
			gotMetric.Attributes["gcp.client.version"] = "DYNAMIC"
		}
		if _, ok := gotMetric.Attributes["url.full"]; ok {
			gotMetric.Attributes["url.full"] = "DYNAMIC"
		}

		filteredGot := make(map[string]any)
		for k, v := range gotMetric.Attributes {
			if _, expected := wantAttrs[k]; expected {
				filteredGot[k] = v
			}
		}

		if diff := cmp.Diff(wantAttrs, filteredGot); diff != "" {
			t.Errorf("Client metric attributes mismatch (-want +got):\n%s", diff)
		}
	}

	for _, attr := range unexpectedAttrs {
		if _, ok := gotMetric.Attributes[attr]; ok {
			t.Errorf("expected attribute %q to be NOT SET, but it was present", attr)
		}
	}

	if len(gotMetric.DataPoints) == 0 {
		t.Errorf("expected metric to have data points")
	}
}

func TestObservability_Metrics_Disablement(t *testing.T) {
	transports := []string{"grpc", "rest"}
	for _, transport := range transports {
		t.Run(transport, func(t *testing.T) {
			fix, clientOpts := setupMetricsTest(t, false, transport)
			ctx := context.Background()

			var echoClient interface {
				Close() error
			}
			var err error
			if transport == "grpc" {
				echoClient, err = showcase.NewEchoClient(ctx, clientOpts...)
			} else {
				echoClient, err = showcase.NewEchoRESTClient(ctx, clientOpts...)
			}
			if err != nil {
				t.Fatalf("failed to create echo client: %v", err)
			}
			t.Cleanup(func() { echoClient.Close() })

			if transport == "grpc" {
				runTracingDisablementScenario(ctx, t, echoClient.(*showcase.EchoClient))
			} else {
				runTracingDisablementScenarioREST(ctx, t, echoClient.(*showcase.EchoClient))
			}

			ctxFlush, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := fix.meterProvider.ForceFlush(ctxFlush); err != nil {
				t.Fatalf("failed to flush meter provider: %v", err)
			}

			time.Sleep(100 * time.Millisecond)

			metrics := fix.metricServer.GetCapturedMetrics()
			for _, m := range metrics {
				if m.Name == "gcp.client.request.duration" {
					t.Errorf("found gcp.client.request.duration metric, but metrics telemetry should be disabled")
				}
			}
		})
	}
}

func TestObservability_Metrics_Success(t *testing.T) {
	transports := []string{"grpc", "rest"}
	for _, transport := range transports {
		t.Run(transport, func(t *testing.T) {
			fix, clientOpts := setupMetricsTest(t, true, transport)
			ctx := context.Background()

			var seqClient interface {
				Close() error
			}
			var err error
			if transport == "grpc" {
				seqClient, err = showcase.NewSequenceClient(ctx, clientOpts...)
			} else {
				seqClient, err = showcase.NewSequenceRESTClient(ctx, clientOpts...)
			}
			if err != nil {
				t.Fatalf("failed to create sequence client: %v", err)
			}
			t.Cleanup(func() { seqClient.Close() })

			if transport == "grpc" {
				_ = runTracingSuccessScenario(ctx, t, seqClient.(*showcase.SequenceClient))
			} else {
				_ = runTracingSuccessScenarioREST(ctx, t, seqClient.(*showcase.SequenceClient))
			}

			var wantAttrs map[string]any
			var unexpectedAttrs []string
			var expectedMethod string

			if transport == "grpc" {
				expectedMethod = "google.showcase.v1beta1.SequenceService/AttemptSequence"
				wantAttrs = map[string]any{
					"gcp.client.artifact":      "github.com/googleapis/gapic-showcase/client",
					"gcp.client.repo":          "googleapis/google-cloud-go",
					"gcp.client.service":       "showcase",
					"gcp.client.version":       "DYNAMIC",
					"rpc.method":               "google.showcase.v1beta1.SequenceService/AttemptSequence",
					"rpc.response.status_code": "OK",
					"rpc.system.name":          "grpc",
					"server.address":           "127.0.0.1",
					"server.port":              int64(7469),
					"url.domain":               "showcase.googleapis.com",
				}
				unexpectedAttrs = []string{"error.type", "http.response.status_code"}
			} else {
				expectedMethod = "POST"
				wantAttrs = map[string]any{
					"gcp.client.artifact":      "github.com/googleapis/gapic-showcase/client",
					"gcp.client.repo":          "googleapis/google-cloud-go",
					"gcp.client.service":       "showcase",
					"gcp.client.version":       "DYNAMIC",
					"http.response.status_code": int64(200),
					"rpc.method":               "POST",
					"rpc.system.name":          "http",
					"server.address":           "127.0.0.1",
					"server.port":              int64(7469),
					"url.domain":               "showcase.googleapis.com",
					"url.template":             "/v1beta1/{name=sequences/*}",
				}
				unexpectedAttrs = []string{"error.type", "rpc.response.status_code"}
			}

			verifyInMemoryMetric(t, fix, "gcp.client.request.duration", "github.com/googleapis/gapic-showcase/client", expectedMethod, wantAttrs, unexpectedAttrs)
		})
	}
}

func TestObservability_Metrics_Failure(t *testing.T) {
	transports := []string{"grpc", "rest"}
	for _, transport := range transports {
		t.Run(transport, func(t *testing.T) {
			fix, clientOpts := setupMetricsTest(t, true, transport)
			ctx := context.Background()

			var seqClient interface {
				Close() error
			}
			var err error
			if transport == "grpc" {
				seqClient, err = showcase.NewSequenceClient(ctx, clientOpts...)
			} else {
				seqClient, err = showcase.NewSequenceRESTClient(ctx, clientOpts...)
			}
			if err != nil {
				t.Fatalf("failed to create sequence client: %v", err)
			}
			t.Cleanup(func() { seqClient.Close() })

			if transport == "grpc" {
				_ = runTracingServerFailureScenario(ctx, t, seqClient.(*showcase.SequenceClient))
			} else {
				_ = runTracingServerFailureScenarioREST(ctx, t, seqClient.(*showcase.SequenceClient))
			}

			var wantAttrs map[string]any
			var unexpectedAttrs []string
			var expectedMethod string

			if transport == "grpc" {
				expectedMethod = "google.showcase.v1beta1.SequenceService/AttemptSequence"
				wantAttrs = map[string]any{
					"error.type":               "NOT_FOUND",
					"gcp.client.artifact":      "github.com/googleapis/gapic-showcase/client",
					"gcp.client.repo":          "googleapis/google-cloud-go",
					"gcp.client.service":       "showcase",
					"gcp.client.version":       "DYNAMIC",
					"rpc.method":               "google.showcase.v1beta1.SequenceService/AttemptSequence",
					"rpc.response.status_code": "NOT_FOUND",
					"rpc.system.name":          "grpc",
					"server.address":           "127.0.0.1",
					"server.port":              int64(7469),
					"url.domain":               "showcase.googleapis.com",
				}
				unexpectedAttrs = []string{"http.response.status_code"}
			} else {
				expectedMethod = "POST"
				wantAttrs = map[string]any{
					"error.type":               "404",
					"gcp.client.artifact":      "github.com/googleapis/gapic-showcase/client",
					"gcp.client.repo":          "googleapis/google-cloud-go",
					"gcp.client.service":       "showcase",
					"gcp.client.version":       "DYNAMIC",
					"http.response.status_code": int64(404),
					"rpc.method":               "POST",
					"rpc.system.name":          "http",
					"server.address":           "127.0.0.1",
					"server.port":              int64(7469),
					"url.domain":               "showcase.googleapis.com",
					"url.template":             "/v1beta1/{name=sequences/*}",
				}
				unexpectedAttrs = []string{"rpc.response.status_code"}
			}

			verifyInMemoryMetric(t, fix, "gcp.client.request.duration", "github.com/googleapis/gapic-showcase/client", expectedMethod, wantAttrs, unexpectedAttrs)
		})
	}
}

func TestObservability_Metrics_ClientFailure(t *testing.T) {
	transports := []string{"grpc", "rest"}
	for _, transport := range transports {
		t.Run(transport, func(t *testing.T) {
			fix, clientOpts := setupMetricsTest(t, true, transport)
			ctx := context.Background()

			var seqClient interface {
				Close() error
			}
			var err error
			if transport == "grpc" {
				seqClient, err = showcase.NewSequenceClient(ctx, clientOpts...)
			} else {
				seqClient, err = showcase.NewSequenceRESTClient(ctx, clientOpts...)
			}
			if err != nil {
				t.Fatalf("failed to create sequence client: %v", err)
			}
			t.Cleanup(func() { seqClient.Close() })

			if transport == "grpc" {
				_ = runTracingClientFailureScenario(ctx, t, seqClient.(*showcase.SequenceClient))
			} else {
				_ = runTracingClientFailureScenarioREST(ctx, t, seqClient.(*showcase.SequenceClient))
			}

			var wantAttrs map[string]any
			var unexpectedAttrs []string
			var expectedMethod string

			if transport == "grpc" {
				expectedMethod = "google.showcase.v1beta1.SequenceService/AttemptSequence"
				wantAttrs = map[string]any{
					"error.type":               "CLIENT_TIMEOUT",
					"gcp.client.artifact":      "github.com/googleapis/gapic-showcase/client",
					"gcp.client.repo":          "googleapis/google-cloud-go",
					"gcp.client.service":       "showcase",
					"gcp.client.version":       "DYNAMIC",
					"rpc.method":               "google.showcase.v1beta1.SequenceService/AttemptSequence",
					"rpc.system.name":          "grpc",
					"server.address":           "127.0.0.1",
					"server.port":              int64(7469),
					"url.domain":               "showcase.googleapis.com",
				}
				unexpectedAttrs = []string{"http.response.status_code", "rpc.response.status_code"}
			} else {
				expectedMethod = "POST"
				wantAttrs = map[string]any{
					"error.type":               "context.deadlineExceededError",
					"gcp.client.artifact":      "github.com/googleapis/gapic-showcase/client",
					"gcp.client.repo":          "googleapis/google-cloud-go",
					"gcp.client.service":       "showcase",
					"gcp.client.version":       "DYNAMIC",
					"rpc.method":               "POST",
					"rpc.system.name":          "http",
					"server.address":           "127.0.0.1",
					"server.port":              int64(7469),
					"url.domain":               "showcase.googleapis.com",
					"url.template":             "/v1beta1/{name=sequences/*}",
				}
				unexpectedAttrs = []string{"http.response.status_code", "rpc.response.status_code"}
			}

			verifyInMemoryMetric(t, fix, "gcp.client.request.duration", "github.com/googleapis/gapic-showcase/client", expectedMethod, wantAttrs, unexpectedAttrs)
		})
	}
}

func TestObservability_Metrics_Retry(t *testing.T) {
	transports := []string{"grpc", "rest"}
	for _, transport := range transports {
		t.Run(transport, func(t *testing.T) {
			fix, clientOpts := setupMetricsTest(t, true, transport)
			ctx := context.Background()

			var seqClient interface {
				Close() error
			}
			var err error
			if transport == "grpc" {
				seqClient, err = showcase.NewSequenceClient(ctx, clientOpts...)
			} else {
				seqClient, err = showcase.NewSequenceRESTClient(ctx, clientOpts...)
			}
			if err != nil {
				t.Fatalf("failed to create sequence client: %v", err)
			}
			t.Cleanup(func() { seqClient.Close() })

			if transport == "grpc" {
				_ = runTracingRetryScenario(ctx, t, seqClient.(*showcase.SequenceClient))
			} else {
				_ = runTracingRetryScenarioREST(ctx, t, seqClient.(*showcase.SequenceClient))
			}

			ctxFlush, cancelFlush := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelFlush()
			if err := fix.meterProvider.ForceFlush(ctxFlush); err != nil {
				t.Fatalf("failed to flush meter provider: %v", err)
			}

			time.Sleep(100 * time.Millisecond)

			metrics := fix.metricServer.GetCapturedMetrics()
			
			expectedMethod := "google.showcase.v1beta1.SequenceService/AttemptSequence"
			if transport == "rest" {
				expectedMethod = "POST"
			}

			var attemptMetrics []CapturedMetric
			for _, m := range metrics {
				if m.Name == "gcp.client.request.duration" && m.Attributes["rpc.method"] == expectedMethod {
					attemptMetrics = append(attemptMetrics, m)
				}
			}

			if len(attemptMetrics) != 1 {
				t.Errorf("expected 1 logical metric for retries, got %d", len(attemptMetrics))
			}
		})
	}
}
