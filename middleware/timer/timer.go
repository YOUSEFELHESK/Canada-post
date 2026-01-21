package timer

import (
	"context"
	"log"
	"time"

	"google.golang.org/grpc"
)

//UnaryServerInterceptor returns a new unary server interceptors that checks request time.
func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {

		start := time.Now()

		// Calls the handler
		h, err := handler(ctx, req)

		// Logging with grpclog (grpclog.LoggerV2)
		log.Printf("Request - Method:%s\tDuration:%s\tError:%v\n",
			info.FullMethod,
			time.Since(start),
			err)

		return h, err
	}
}
