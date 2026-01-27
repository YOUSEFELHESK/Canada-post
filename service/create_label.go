package service

import (
	"context"
	"log"
	"time"

	labels "bitbucket.org/lexmodo/proto/labels"
	shippingpluginpb "bitbucket.org/lexmodo/proto/shipping_plugin"
	"lexmodo-plugin/database"
)

// CreateLabel creates a shipment and persists the label/tracking metadata.
func (s *Server) CreateLabel(
	ctx context.Context,
	req *shippingpluginpb.ShippingRateRequest,
) (*shippingpluginpb.ResultResponse, error) {
	log.Println("📥 CreateLabel RECEIVED")
	log.Printf("%+v\n", req)
	logIncomingMetadata(ctx)

	returnData := &shippingpluginpb.ResultResponse{
		Success:      true,
		Failure:      false,
		Code:         "200",
		Message:      "CreateLabel OK",
		ShippingAuth: req.GetShippingAuth(),
	}

	shipRequest := req.GetShipRequest()
	if shipRequest == nil {
		return &shippingpluginpb.ResultResponse{
			Success: false,
			Failure: true,
			Code:    "400",
			Message: "CreateLabel missing ship_request",
		}, nil
	}

	selectedRateID := shipRequest.GetShippingRateId()
	labelID := shipRequest.GetLabelId()
	if labelID == "" {
		labelID = generateLabelID()
	}

	shipment, err := s.createShipmentFromAPI(ctx, shipRequest)
	if err != nil {
		return &shippingpluginpb.ResultResponse{
			Success: false,
			Failure: true,
			Code:    "400",
			Message: err.Error(),
		}, nil
	}

	labelURL := ""
	for _, link := range shipment.Links.Link {
		if link.Rel == "label" {
			labelURL = link.Href
			break
		}
	}
	if labelURL == "" {
		return &shippingpluginpb.ResultResponse{
			Success: false,
			Failure: true,
			Code:    "500",
			Message: "label URL not found in response",
		}, nil
	}

	labelPDF, err := s.CanadaPost.GetArtifact(ctx, labelURL)
	if err != nil {
		return &shippingpluginpb.ResultResponse{
			Success: false,
			Failure: true,
			Code:    "500",
			Message: err.Error(),
		}, nil
	}

	tracking := shipment.TrackingPIN

	returnData.Label = &labels.LabelResponse{
		LabelId:     labelID,
		LabelUrl:    s.buildLabelURL(labelID),
		TackingCode: tracking,
		Carrier:     "Canada Post",
		Method:      camelCaseSpace(defaultValue(selectedRateID, "STANDARD")),
		ShipDate:    uint32(time.Now().Unix()),
		InvoiceUuid: shipRequest.GetInvoiceUuid(),
		DelayTask:   shipRequest.GetDelayTask(),
	}

	if shipRequest.GetInvoiceUuid() != "" && shipRequest.GetShippingRateId() != "" {
		if err := s.Store.SaveChosenRateID(shipRequest.GetInvoiceUuid(), shipRequest.GetShippingRateId()); err != nil {
			log.Println("❌ Failed to store chosen rate:", err)
		} else {
			log.Printf("✅ Stored rate %s for invoice %s\n", shipRequest.GetShippingRateId(), shipRequest.GetInvoiceUuid())
		}
	}
	if shipRequest.GetInvoiceUuid() != "" && tracking != "" {
		if err := s.Store.SaveTrackingNumber(shipRequest.GetInvoiceUuid(), tracking); err != nil {
			log.Println("❌ Failed to store tracking number:", err)
		} else {
			log.Printf("✅ Stored tracking number %s for invoice %s\n", tracking, shipRequest.GetInvoiceUuid())
		}
	}

	totalWeight := 0.0
	if parcel := shipRequest.GetParcel(); parcel != nil {
		totalWeight = float64(parcel.GetWeight())
	}

	record := database.LabelRecord{
		ID:             labelID,
		ShipmentID:     shipment.ShipmentID,
		TrackingNumber: tracking,
		ServiceCode:    resolveServiceCode(selectedRateID),
		Weight:         totalWeight,
		LabelPDF:       labelPDF,
	}

	if err := s.Store.SaveLabelRecord(record); err != nil {
		log.Println("❌ Failed to store label record:", err)
	}

	return returnData, nil
}
