package service

import (
	"context"
	"log"
	"strings"

	shippingpluginpb "bitbucket.org/lexmodo/proto/shipping_plugin"
)

// RefundLabel cancels a shipment with FedEx using the stored tracking number.
func (s *Server) RefundLabel(
	ctx context.Context,
	req *shippingpluginpb.ShippingRateRequest,
) (*shippingpluginpb.ResultResponse, error) {
	log.Println("📥 RefundLabel RECEIVED")
	log.Printf("%+v\n", req)
	logIncomingMetadata(ctx)

	shipRequest := req.GetShipRequest()
	if shipRequest == nil {
		return &shippingpluginpb.ResultResponse{
			Success: false,
			Failure: true,
			Code:    "400",
			Message: "RefundLabel missing ship_request",
		}, nil
	}

	invoiceID := strings.TrimSpace(shipRequest.GetInvoiceUuid())
	if invoiceID == "" {
		invoiceID = strings.TrimSpace(shipRequest.GetLabelId())
	}

	trackingNumber := ""
	if invoiceID != "" {
		storedTracking, err := s.Store.LoadTrackingNumber(invoiceID)
		if err != nil {
			log.Println("❌ Failed to load tracking number:", err)
		}
		if storedTracking != "" {
			trackingNumber = storedTracking
		}
	}
	if trackingNumber == "" {
		latestTracking, err := s.Store.LoadLatestTrackingNumber()
		if err != nil {
			log.Println("❌ Failed to load latest tracking number:", err)
		}
		if latestTracking != "" {
			trackingNumber = latestTracking
		}
	}
	if trackingNumber == "" {
		return &shippingpluginpb.ResultResponse{
			Success: false,
			Failure: true,
			Code:    "400",
			Message: "RefundLabel missing tracking number",
		}, nil
	}

	if _, err := s.cancelShipmentFromAPI(ctx, trackingNumber, shipRequest.GetShipper()); err != nil {
		return &shippingpluginpb.ResultResponse{
			Success: false,
			Failure: true,
			Code:    "400",
			Message: err.Error(),
		}, nil
	}

	return &shippingpluginpb.ResultResponse{
		Success: true,
		Code:    "200",
		Message: "RefundLabel OK",
	}, nil
}
