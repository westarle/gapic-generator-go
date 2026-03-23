package showcase

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	showcasepb "github.com/googleapis/gapic-showcase/server/genproto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestMetrics_Record(t *testing.T) {
	// 1. Setup a ManualReader to inspect recorded metrics.
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	// OpenTelemetry's global provider creates delegating meters, so even though
	// the clients were instantiated in TestMain (before this), they will start
	// sending metrics to this provider once we set it globally.
	otel.SetMeterProvider(provider)

	content := "Hello, metrics!"
	req := &showcasepb.EchoRequest{
		Response: &showcasepb.EchoRequest_Content{
			Content: content,
		},
	}

	// 2. Invoke an RPC over gRPC
	_, err := echo.Echo(context.Background(), req)
	if err != nil {
		t.Fatalf("gRPC Echo failed: %v", err)
	}

	// 3. Invoke an RPC over REST
	_, err = echoREST.Echo(context.Background(), req)
	if err != nil {
		t.Fatalf("REST Echo failed: %v", err)
	}

	// 4. Collect metrics
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Failed to collect metrics: %v", err)
	}
	
	// Print metrics for user
	jsonBytes, _ := json.MarshalIndent(rm, "", "  ")
	t.Logf("Collected Metrics:\n%s\n", string(jsonBytes))

	// 5. Assert the metrics
	var durationMetric *metricdata.Metrics
	var scopeAttrs []attribute.KeyValue
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "gcp.client.request.duration" {
				// We expect the metric to be found
				durationMetric = &m
				scopeAttrs = sm.Scope.Attributes.ToSlice()
				break
			}
		}
	}

	if durationMetric == nil {
		t.Fatalf("metric 'gcp.client.request.duration' was not recorded. Collected scope metrics: %+v", rm.ScopeMetrics)
	}

	wantScopeAttr := map[string]string{
		"gcp.client.service": "showcase",
	}
	gotScopeAttr := make(map[string]string)
	for _, a := range scopeAttrs {
		gotScopeAttr[string(a.Key)] = a.Value.AsString()
	}
	if len(gotScopeAttr) != len(wantScopeAttr) {
		t.Errorf("expected %d scope attributes, got %d (%v)", len(wantScopeAttr), len(gotScopeAttr), gotScopeAttr)
	}
	for k, v := range wantScopeAttr {
		if got, ok := gotScopeAttr[k]; !ok || got != v {
			t.Errorf("expected scope attribute %s=%s, got %s", k, v, got)
		}
	}

	hist, ok := durationMetric.Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("metric data type is %T, want Histogram", durationMetric.Data)
	}

	// We expect 2 data points: since rpc.system.name is populated by the generator,
	// gRPC and REST calls will separate into their own Histogram DataPoints.
	if len(hist.DataPoints) != 2 {
		t.Errorf("expected 2 data points, got %d", len(hist.DataPoints))
	}

	var grpcCount, restCount int
	for _, dp := range hist.DataPoints {
		// Minimum validation on the recorded duration
		if dp.Count != 1 {
			t.Errorf("expected data point to have Count=1, got %d", dp.Count)
		}
		if dp.Sum <= 0 {
			t.Errorf("expected recorded duration to be > 0, got %f", dp.Sum)
		}

		// Check exact attributes present on the datapoint
		gotDataAttr := make(map[string]string)
		for _, attr := range dp.Attributes.ToSlice() {
			gotDataAttr[string(attr.Key)] = fmt.Sprintf("%v", attr.Value.AsInterface())
		}

		sysVal := gotDataAttr["rpc.system.name"]

		var wantDataAttr map[string]string
		if sysVal == "grpc" {
			grpcCount++
			wantDataAttr = map[string]string{
				"rpc.system.name": "grpc",
				"rpc.method":      "google.showcase.v1beta1.Echo/Echo",
				"url.domain":      "showcase.googleapis.com",
				"rpc.response.status_code": "OK", // since there's no error in the test
			}
		} else if sysVal == "http" {
			restCount++
			wantDataAttr = map[string]string{
				"rpc.system.name":           "http",
				"rpc.method":                "google.showcase.v1beta1.Echo/Echo",
				"url.template":              "/v1beta1/echo:echo",
				"url.domain":                "showcase.googleapis.com",
				"rpc.response.status_code":  "OK",
			}
		} else {
			t.Errorf("unexpected rpc.system.name: %s", sysVal)
			continue
		}

		if len(gotDataAttr) != len(wantDataAttr) {
			t.Errorf("expected %d datapoint attributes for %s, got %d", len(wantDataAttr), sysVal, len(gotDataAttr))
		}
		t.Logf("gotDataAttr for %s: %v", sysVal, gotDataAttr)
		for k, v := range wantDataAttr {
			got, ok := gotDataAttr[k]
			if !ok {
				t.Errorf("missing expected datapoint attribute %s=%s for %s", k, v, sysVal)
				continue
			}
			if k == "server.address" && v == "localhost" && (got == "[::1]" || got == "127.0.0.1") {
				continue // Accept loopback addresses as equivalent to localhost
			}
			if got != v {
				t.Errorf("expected datapoint attribute %s=%s for %s, got %s", k, v, sysVal, got)
			}
		}
	}

	if grpcCount == 1 && restCount == 1 {
		t.Log("Successfully recorded separate datapoints for gRPC and REST")
	} else {
		t.Errorf("expected 1 grpc and 1 rest datapoint, got grpc=%d rest=%d", grpcCount, restCount)
	}
}
