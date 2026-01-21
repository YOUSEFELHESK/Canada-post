package grpcapi

import "lexmodo-plugin/service"

func Start(addr string, server *service.Server) {
	service.StartGRPCServer(addr, server)
}
