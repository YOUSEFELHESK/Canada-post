package ipresolver

import (
	"context"
	"strings"

	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

var ip_error = status.New(3, "IP Address can't be resolved").Err()

func StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := stream.Context()
		var err error
		ctx, err = getIPAddress(ctx)
		if err != nil {
			return err
		}
		wrapped := grpc_middleware.WrapServerStream(stream)
		wrapped.WrappedContext = ctx

		return handler(srv, wrapped)
	}
}

func UnaryServerInterceptor() grpc.UnaryServerInterceptor {

	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		var err error
		ctx, err = getIPAddress(ctx)
		if err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

func getIPAddress(ctx context.Context) (context.Context, error) {
	//	log.Println("HELLO IP ADDRESS")
	p, ok := peer.FromContext(ctx)
	if !ok {

		return nil, ip_error
	}
	//	log.Println("HELLO IP ADDRESS 2")
	meta, ok := metadata.FromIncomingContext(ctx)
	if !ok {

		return nil, ip_error
	}
	//	log.Println("HELLO IP ADDRESS 3")
	//User IP address will always be first
	if len(meta.Get("x-forwarded-for")) > 0 {

		ctx = context.WithValue(ctx, "x-user-ip", meta.Get("x-forwarded-for")[0])

	}
	//	log.Println("HELLO IP ADDRESS 4")
	ip := strings.Split(p.Addr.String(), ":")[0]

	ctx = context.WithValue(ctx, "x-caller-ip", ip)
	//	log.Println("HELLO IP ADDRESS 5")
	///log.Println("CHECK HERE NO ", internalvariables.ConvertFromInternalVariables(ctx).Value("client_id"))
	return ctx, nil
}
