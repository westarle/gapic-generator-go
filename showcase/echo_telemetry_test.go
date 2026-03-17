package showcase_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/googleapis/gapic-showcase/client"
	showcasepb "github.com/googleapis/gapic-showcase/server/genproto"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestTelemetryOutput(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	ctx := context.Background()

	// Connect to local showcase server
	opts := []option.ClientOption{
		option.WithEndpoint("localhost:7469"),
		option.WithTokenSource(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "dummy-token"})),
		option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
		option.WithLogger(logger),
	}

	c, err := client.NewEchoClient(ctx, opts...)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer c.Close()

	ei1, _ := anypb.New(&errdetails.ErrorInfo{
		Reason: "CREDENTIALS_MISSING",
		Domain: "example.com",
		Metadata: map[string]string{
			"key1": "value1",
		},
	})

	// Call Echo configured to fail
	_, err = c.Echo(ctx, &showcasepb.EchoRequest{
		Response: &showcasepb.EchoRequest_Error{
			Error: &status.Status{
				Code:    int32(codes.InvalidArgument),
				Message: "fail this on purpose",
				Details: []*anypb.Any{ei1},
			},
		},
	})
	if err == nil {
		t.Fatalf("Expected error, got none")
	}

	st, ok := grpcstatus.FromError(err)
	if !ok {
		t.Fatalf("Expected gRPC status error, got: %v", err)
	}
	t.Logf("Got expected error: %v", st.Code())

	// Sleep briefly to let handlers finish
	time.Sleep(1 * time.Second)

	output := buf.String()
	t.Logf("Logger output:\n%s", output)
	if output == "" {
		t.Fatalf("Expected telemetry logs, got none")
	}

	if !strings.Contains(output, `"rpc.system.name":"grpc"`) {
		t.Errorf("Expected rpc.system.name in log output")
	}
	if !strings.Contains(output, `"error.type":"CREDENTIALS_MISSING"`) {
		t.Errorf("Expected error.type: CREDENTIALS_MISSING in log output")
	}
	if !strings.Contains(output, `"gcp.errors.domain":"example.com"`) {
		t.Errorf("Expected gcp.errors.domain in log output")
	}
	if !strings.Contains(output, `"gcp.errors.metadata.key1":"value1"`) {
		t.Errorf("Expected gcp.errors.metadata.key1 in log output")
	}
}
