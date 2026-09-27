package grpcx_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/blessed-go/sling/platform/ctxerr"
	"github.com/blessed-go/sling/platform/grpcx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestServerLoggingInterceptor_PreservesReturnedError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	interceptor := grpcx.ServerLoggingInterceptor(logger)

	expectedErr := status.Error(codes.NotFound, "item not found")
	internalDBErr := errors.New("sql: no rows in result set")

	handler := func(ctx context.Context, req any) (any, error) {
		ctxerr.SetErr(ctx, internalDBErr)
		return nil, expectedErr
	}

	info := &grpc.UnaryServerInfo{
		FullMethod: "/test.Service/GetItem",
	}

	reply, err := interceptor(context.Background(), "test-req", info, handler)
	if reply != nil {
		t.Errorf("expected reply to be nil, got %v", reply)
	}

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %T: %v", err, err)
	}

	if st.Code() != codes.NotFound {
		t.Errorf("expected status code %v (NotFound), got %v", codes.NotFound, st.Code())
	}

	if st.Message() != "item not found" {
		t.Errorf("expected message 'item not found', got %q", st.Message())
	}
}
