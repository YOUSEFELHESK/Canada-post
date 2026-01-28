package authentication

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

var adminError = status.New(16, "authorization is missing/expired")

// UnaryServerInterceptor returns a new unary server interceptors that performs per-request auth.
func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		g := time.Now()
		defer func() {
			log.Println("Time took for authentication", time.Since(g))
		}()

		// ✅ Skip auth for local testing
		if os.Getenv("SKIP_AUTH") == "true" {
			log.Println("⚠️  AUTH SKIPPED (SKIP_AUTH=true)")
			return handler(ctx, req)
		}

		var err error
		ctx, err = authentication(ctx)
		if err != nil {
			return nil, status.New(16, fmt.Sprintf("authorization is missing/expired ( Reason: %s)", err.Error())).Err()
		}

		log.Println("Time took for For JWT token to respond", time.Since(g))
		return handler(ctx, req)
	}
}
