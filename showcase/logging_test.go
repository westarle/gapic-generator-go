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
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func setupLoggingTest(t *testing.T, enableLogging bool, transport string) (*observabilityFixture, []option.ClientOption) {
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

	oldTP := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(oldTP) })
	otel.SetTracerProvider(fix.provider)

	var clientOpts []option.ClientOption
	if transport == "grpc" {
		clientOpts = []option.ClientOption{
			option.WithEndpoint("127.0.0.1:7469"),
			option.WithAuthCredentials(auth.NewCredentials(&auth.CredentialsOptions{
				TokenProvider: dummyTokenProvider{},
			})),
			option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
			option.WithLogger(otelslog.NewLogger("github.com/googleapis/gapic-showcase/client")),
		}
	} else {
		clientOpts = []option.ClientOption{
			option.WithEndpoint("http://127.0.0.1:7469"),
			option.WithTokenSource(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "dummy-token"})),
			option.WithLogger(otelslog.NewLogger("github.com/googleapis/gapic-showcase/client")),
		}
	}

	return fix, clientOpts
}

func verifyInMemoryLogsEmpty(t *testing.T, fix *observabilityFixture, traceID trace.TraceID) {
	t.Helper()

	ctxFlush, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := fix.loggerProvider.ForceFlush(ctxFlush); err != nil {
		t.Fatalf("failed to flush logger provider: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	logs := fix.logServer.GetCapturedLogs()
	var errorLogs []CapturedLog
	for _, l := range logs {
		if string(l.TraceID) == string(traceID[:]) {
			if _, ok := l.Attributes["error.type"]; ok {
				errorLogs = append(errorLogs, l)
			}
		}
	}

	if len(errorLogs) > 0 {
		t.Fatalf("expected to receive no log exports, got %d", len(errorLogs))
	}
}

func verifyInMemoryLog(t *testing.T, fix *observabilityFixture, expectedSeverity olog.SeverityNumber, expectedScope string, traceID trace.TraceID, wantAttrs map[string]any, unexpectedAttrs []string) {
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
		if string(l.TraceID) == string(traceID[:]) && l.Severity == expectedSeverity {
			if _, ok := l.Attributes["error.type"]; ok {
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
		if _, ok := gotLog.Attributes["exception.message"]; ok {
			gotLog.Attributes["exception.message"] = "DYNAMIC"
		}
		if _, ok := gotLog.Attributes["exception.stacktrace"]; ok {
			gotLog.Attributes["exception.stacktrace"] = "DYNAMIC"
		}

		filteredGot := make(map[string]any)
		for k, v := range gotLog.Attributes {
			if _, expected := wantAttrs[k]; expected {
				filteredGot[k] = v
			}
		}

		if diff := cmp.Diff(wantAttrs, filteredGot); diff != "" {
			t.Errorf("Client log attributes mismatch (-want +got):\n%s", diff)
		}
	}

	for _, attr := range unexpectedAttrs {
		if _, ok := gotLog.Attributes[attr]; ok {
			t.Errorf("expected attribute %q to be NOT SET, but it was present", attr)
		}
	}
}

func TestObservability_Logging_Disablement(t *testing.T) {
	transports := []string{"grpc", "rest"}
	for _, transport := range transports {
		t.Run(transport, func(t *testing.T) {
			fix, clientOpts := setupLoggingTest(t, false, transport)
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

			ctxSpan, span := otel.Tracer("test-tracer").Start(context.Background(), "APP")

			if transport == "grpc" {
				runTracingDisablementScenario(ctxSpan, t, echoClient.(*showcase.EchoClient))
			} else {
				runTracingDisablementScenarioREST(ctxSpan, t, echoClient.(*showcase.EchoClient))
			}
			span.End()
			traceID := span.SpanContext().TraceID()

			verifyInMemoryLogsEmpty(t, fix, traceID)
		})
	}
}

func TestObservability_Logging_Success(t *testing.T) {
	transports := []string{"grpc", "rest"}
	for _, transport := range transports {
		t.Run(transport, func(t *testing.T) {
			fix, clientOpts := setupLoggingTest(t, true, transport)
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

			ctxSpan, span := otel.Tracer("test-tracer").Start(ctx, "APP")
			
			if transport == "grpc" {
				_ = runTracingSuccessScenario(ctxSpan, t, seqClient.(*showcase.SequenceClient))
			} else {
				_ = runTracingSuccessScenarioREST(ctxSpan, t, seqClient.(*showcase.SequenceClient))
			}
			span.End()
			traceID := span.SpanContext().TraceID()

			verifyInMemoryLogsEmpty(t, fix, traceID)
		})
	}
}

func TestObservability_Logging_Failure(t *testing.T) {
	transports := []string{"grpc", "rest"}
	for _, transport := range transports {
		t.Run(transport, func(t *testing.T) {
			fix, clientOpts := setupLoggingTest(t, true, transport)
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

			ctxSpan, span := otel.Tracer("test-tracer").Start(ctx, "APP")
			if transport == "grpc" {
				_ = runTracingServerFailureScenario(ctxSpan, t, seqClient.(*showcase.SequenceClient))
			} else {
				_ = runTracingServerFailureScenarioREST(ctxSpan, t, seqClient.(*showcase.SequenceClient))
			}
			span.End()
			traceID := span.SpanContext().TraceID()

			var wantAttrs map[string]any
			var unexpectedAttrs []string

			if transport == "grpc" {
				wantAttrs = map[string]any{
					"error.type":               "NOT_FOUND",
					"exception.type":           "*status.Error",
					"exception.message":        "DYNAMIC",
					"gcp.client.artifact":      "github.com/googleapis/gapic-showcase/client",
					"gcp.client.language":      "go",
					"gcp.client.repo":          "googleapis/google-cloud-go",
					"gcp.client.service":       "showcase",
					"gcp.client.version":       "DYNAMIC",
					"rpc.response.status_code": "NOT_FOUND",
					"rpc.system.name":          "grpc",
					"url.domain":               "showcase.googleapis.com",
				}
			} else {
				wantAttrs = map[string]any{
					"error.type":               "404",
					"exception.type":           "NOT SET", // REST transport might not have exception type for non-client errors
					"exception.message":        "DYNAMIC",
					"gcp.client.artifact":      "github.com/googleapis/gapic-showcase/client",
					"gcp.client.language":      "go",
					"gcp.client.repo":          "googleapis/google-cloud-go",
					"gcp.client.service":       "showcase",
					"gcp.client.version":       "DYNAMIC",
					"http.response.status_code": int64(404),
					"rpc.system.name":          "http",
					"url.domain":               "showcase.googleapis.com",
				}
				unexpectedAttrs = []string{"exception.type"}
			}

			verifyInMemoryLog(t, fix, olog.SeverityNumber_SEVERITY_NUMBER_DEBUG, "github.com/googleapis/gapic-showcase/client", traceID, wantAttrs, unexpectedAttrs)
		})
	}
}

func TestObservability_Logging_ClientFailure(t *testing.T) {
	transports := []string{"grpc", "rest"}
	for _, transport := range transports {
		t.Run(transport, func(t *testing.T) {
			fix, clientOpts := setupLoggingTest(t, true, transport)
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

			ctxSpan, span := otel.Tracer("test-tracer").Start(ctx, "APP")
			if transport == "grpc" {
				_ = runTracingClientFailureScenario(ctxSpan, t, seqClient.(*showcase.SequenceClient))
			} else {
				_ = runTracingClientFailureScenarioREST(ctxSpan, t, seqClient.(*showcase.SequenceClient))
			}
			span.End()
			traceID := span.SpanContext().TraceID()

			var wantAttrs map[string]any
			var unexpectedAttrs []string

			if transport == "grpc" {
				wantAttrs = map[string]any{
					"error.type":               "CLIENT_TIMEOUT",
					"exception.type":           "*status.Error",
					"exception.message":        "DYNAMIC",
					"gcp.client.artifact":      "github.com/googleapis/gapic-showcase/client",
					"gcp.client.language":      "go",
					"gcp.client.repo":          "googleapis/google-cloud-go",
					"gcp.client.service":       "showcase",
					"gcp.client.version":       "DYNAMIC",
					"rpc.system.name":          "grpc",
					"url.domain":               "showcase.googleapis.com",
				}
				unexpectedAttrs = []string{"rpc.response.status_code", "http.response.status_code"}
			} else {
				wantAttrs = map[string]any{
					"error.type":               "context.deadlineExceededError",
					"exception.type":           "*fmt.wrapError",
					"exception.message":        "DYNAMIC",
					"gcp.client.artifact":      "github.com/googleapis/gapic-showcase/client",
					"gcp.client.language":      "go",
					"gcp.client.repo":          "googleapis/google-cloud-go",
					"gcp.client.service":       "showcase",
					"gcp.client.version":       "DYNAMIC",
					"rpc.system.name":          "http",
					"url.domain":               "showcase.googleapis.com",
				}
				unexpectedAttrs = []string{"rpc.response.status_code", "http.response.status_code"}
			}

			verifyInMemoryLog(t, fix, olog.SeverityNumber_SEVERITY_NUMBER_DEBUG, "github.com/googleapis/gapic-showcase/client", traceID, wantAttrs, unexpectedAttrs)
		})
	}
}

func TestObservability_Logging_Retry(t *testing.T) {
	transports := []string{"grpc", "rest"}
	for _, transport := range transports {
		t.Run(transport, func(t *testing.T) {
			fix, clientOpts := setupLoggingTest(t, true, transport)
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

			ctxSpan, span := otel.Tracer("test-tracer").Start(ctx, "APP")
			if transport == "grpc" {
				_ = runTracingRetryScenario(ctxSpan, t, seqClient.(*showcase.SequenceClient))
			} else {
				_ = runTracingRetryScenarioREST(ctxSpan, t, seqClient.(*showcase.SequenceClient))
			}
			span.End()
			traceID := span.SpanContext().TraceID()

			ctxFlush, cancelFlush := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelFlush()
			if err := fix.loggerProvider.ForceFlush(ctxFlush); err != nil {
				t.Fatalf("failed to flush logger provider: %v", err)
			}

			time.Sleep(100 * time.Millisecond)

			logs := fix.logServer.GetCapturedLogs()
			var attemptLogs []CapturedLog
			for _, l := range logs {
				if string(l.TraceID) == string(traceID[:]) && l.Severity == olog.SeverityNumber_SEVERITY_NUMBER_DEBUG {
					if _, ok := l.Attributes["error.type"]; ok {
						attemptLogs = append(attemptLogs, l)
					}
				}
			}

			// We expect 3 failure logs for the 3 failed retry attempts.
			if len(attemptLogs) != 3 {
				t.Errorf("expected 3 attempt logs (3 failures), got %d", len(attemptLogs))
			}
		})
	}
}
