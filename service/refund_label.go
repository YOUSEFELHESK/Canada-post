package service

import (
	"context"
	"strings"

	shippingpluginpb "bitbucket.org/lexmodo/proto/shipping_plugin"
)

// RefundLabel maps legacy RPC name to Canada Post refund implementation.
// Request Non-Contract Shipment Refund – REST
func (s *Server) RefundLabel(ctx context.Context, req *shippingpluginpb.ShippingRateRequest) (*shippingpluginpb.ResultResponse, error) {
	if req == nil || req.GetShipRequest() == nil {
		return &shippingpluginpb.ResultResponse{
			Success: false,
			Failure: true,
			Code:    "400",
			Message: "RefundLabel missing ship_request",
		}, nil
	}

	shipRequest := req.GetShipRequest()
	if getter, ok := interface{}(shipRequest).(interface{ GetRefundLink() string }); ok {
		if strings.TrimSpace(getter.GetRefundLink()) != "" {
			return s.RefundShipment(ctx, req)
		}
	}

	return s.RefundShipment(ctx, req)
}
