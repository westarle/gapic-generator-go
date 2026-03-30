package showcase

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/contrib/detectors/gcp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	v1common "go.opentelemetry.io/proto/otlp/common/v1"
	pb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
)

type mockTraceServer struct {
	pb.UnimplementedTraceServiceServer
	mu       sync.Mutex
	requests []*pb.ExportTraceServiceRequest
}

func (s *mockTraceServer) Export(ctx context.Context, req *pb.ExportTraceServiceRequest) (*pb.ExportTraceServiceResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, req)
	return &pb.ExportTraceServiceResponse{}, nil
}

type CapturedSpan struct {
	Name       string
	Scope      string
	TraceID    []byte
	Attributes map[string]any
}

func (s *mockTraceServer) getRequests() []*pb.ExportTraceServiceRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	reqs := make([]*pb.ExportTraceServiceRequest, len(s.requests))
	copy(reqs, s.requests)
	return reqs
}

func (s *mockTraceServer) GetCapturedSpans() []CapturedSpan {
	reqs := s.getRequests()
	var spans []CapturedSpan
	for _, req := range reqs {
		for _, rs := range req.ResourceSpans {
			for _, ss := range rs.ScopeSpans {
				for _, s := range ss.Spans {
					attrs := make(map[string]any)
					for _, kv := range s.Attributes {
						if kv.Value != nil {
							switch v := kv.Value.Value.(type) {
							case *v1common.AnyValue_StringValue:
								attrs[kv.Key] = v.StringValue
							case *v1common.AnyValue_IntValue:
								attrs[kv.Key] = v.IntValue
							case *v1common.AnyValue_BoolValue:
								attrs[kv.Key] = v.BoolValue
							case *v1common.AnyValue_DoubleValue:
								attrs[kv.Key] = v.DoubleValue
							// other types omitted for brevity, but easily added later if needed
							default:
								attrs[kv.Key] = kv.Value.String()
							}
						}
					}
					spans = append(spans, CapturedSpan{
						Name:       s.Name,
						Scope:      ss.Scope.Name,
						TraceID:    s.TraceId,
						Attributes: attrs,
					})
				}
			}
		}
	}
	return spans
}

type observabilityFixture struct {
	grpcServer  *grpc.Server
	traceServer *mockTraceServer
	provider    *sdktrace.TracerProvider
}

// setupObservabilityFixture creates an in-memory OTLP trace server and configures the OTel SDK to export to it.
func setupObservabilityFixture(t *testing.T) *observabilityFixture {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	traceServer := &mockTraceServer{}
	pb.RegisterTraceServiceServer(grpcServer, traceServer)

	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			t.Logf("grpc server serve err: %v", err)
		}
	}()
	t.Cleanup(func() {
		grpcServer.Stop()
	})

	ctx := context.Background()
	exp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(lis.Addr().String()),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		t.Fatalf("failed to create exporter: %v", err)
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
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tp.Shutdown(ctx); err != nil {
			t.Logf("Failed to shutdown tracer provider: %v", err)
		}
	})

	return &observabilityFixture{
		grpcServer:  grpcServer,
		traceServer: traceServer,
		provider:    tp,
	}
}
