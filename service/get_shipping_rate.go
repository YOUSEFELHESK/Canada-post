package service

import (
	"context"
	"log"

	shippingpluginpb "bitbucket.org/lexmodo/proto/shipping_plugin"
)

// GetShippingRate returns available rates from the upstream Canada Post API.
func (s *Server) GetShippingRate(
	ctx context.Context,
	req *shippingpluginpb.ShippingRateRequest,
) (*shippingpluginpb.ResultResponse, error) {
	log.Println("📥 GetShippingRate RECEIVED")
	log.Printf("%+v\n", req)
	logIncomingMetadata(ctx)

	rates, err := s.fetchRatesFromAPI(ctx, req)
	if err != nil {
		return &shippingpluginpb.ResultResponse{
			Success: false,
			Failure: true,
			Code:    "400",
			Message: err.Error(),
		}, nil
	}

	logRequestDetails(req, rates)

	return &shippingpluginpb.ResultResponse{
		Success:       true,
		Code:          "200",
		Message:       "GetShippingRate OK",
		ShippingRates: rates,
	}, nil
}
