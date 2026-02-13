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
	logIncomingOptions(req)

	rates, err := s.fetchRatesFromAPI(ctx, req)
	if err != nil {
		resp := &shippingpluginpb.ResultResponse{
			Success: false,
			Failure: true,
			Code:    "400",
			Message: err.Error(),
		}
		logPluginResponse("GetShippingRate", resp)
		return resp, nil
	}

	logRequestDetails(req, rates)

	resp := &shippingpluginpb.ResultResponse{
		Success:       true,
		Code:          "200",
		Message:       "GetShippingRate OK",
		ShippingRates: rates,
	}
	logPluginResponse("GetShippingRate", resp)
	return resp, nil
}

func logIncomingOptions(req *shippingpluginpb.ShippingRateRequest) {
	if req == nil {
		log.Println("GetShippingRate options: request is nil")
		return
	}
	options := req.GetShippingpluginreqeustCustomInfo()
	log.Printf("GetShippingRate options received: %d", len(options))
	for i, option := range options {
		if option == nil {
			log.Printf("[%d] <nil>", i)
			continue
		}
		log.Printf("[%d] field=%s value=%s value_set=%v", i, option.GetFieldName(), option.GetFieldValue(), option.GetFieldValueSet())
	}
}
