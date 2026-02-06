package service

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
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

	selectedRateID := strings.TrimSpace(shipRequest.GetShippingRateId())
	if selectedRateID == "" {
		return &shippingpluginpb.ResultResponse{
			Success: false,
			Failure: true,
			Code:    "400",
			Message: "CreateLabel missing shipping_rate_id",
		}, nil
	}

	if s.RateSnapshots == nil {
		return &shippingpluginpb.ResultResponse{
			Success: false,
			Failure: true,
			Code:    "500",
			Message: "rate snapshot store not configured",
		}, nil
	}
	snapshot, err := s.RateSnapshots.Load(ctx, selectedRateID)
	if err != nil {
		return &shippingpluginpb.ResultResponse{
			Success: false,
			Failure: true,
			Code:    "404",
			Message: "rate expired or invalid",
		}, nil
	}
	log.Printf("✅ Snapshot loaded: rate_id=%s service_code=%s", selectedRateID, snapshot.ServiceCode)
	log.Printf("📦 CreateLabel XML includes: customs=%v phone=%s client_voice=%s",
		snapshot.CustomsInfo != nil,
		snapshot.Shipper.Phone,
		snapshot.Customer.Phone,
	)
	requestCurrency := resolveRequestCurrency(ctx, req)
	if snapshot.CurrencyCode == "" {
		snapshot.CurrencyCode = requestCurrency
	}
	if snapshot.RateToCad <= 0 && requestCurrency != "" && strings.ToUpper(requestCurrency) != "CAD" {
		if s.Store == nil {
			return &shippingpluginpb.ResultResponse{
				Success: false,
				Failure: true,
				Code:    "500",
				Message: "currency rates store not configured",
			}, nil
		}
		clientID := clientIDFromRequest(ctx, req)
		if clientID == 0 {
			return &shippingpluginpb.ResultResponse{
				Success: false,
				Failure: true,
				Code:    "400",
				Message: "client_id required for currency conversion",
			}, nil
		}
		rate, ok, err := s.Store.LoadCurrencyRate(clientID, requestCurrency)
		if err != nil {
			return &shippingpluginpb.ResultResponse{
				Success: false,
				Failure: true,
				Code:    "500",
				Message: "failed to load currency rate",
			}, nil
		}
		if !ok {
			return &shippingpluginpb.ResultResponse{
				Success: false,
				Failure: true,
				Code:    "400",
				Message: "missing conversion rate for " + requestCurrency,
			}, nil
		}
		snapshot.RateToCad = rate
	}

	labelID := shipRequest.GetLabelId()
	if labelID == "" {
		labelID = generateLabelID()
	}

	customInfo := req.GetShippingpluginreqeustCustomInfo()
	if err := s.validateCustomInfoValues(customInfo); err != nil {
		return &shippingpluginpb.ResultResponse{
			Success: false,
			Failure: true,
			Code:    "400",
			Message: err.Error(),
		}, nil
	}
	options := s.buildCanadaPostOptions(customInfo, snapshot.RateToCad)
	options = append(options, buildSnapshotOptions(snapshot)...)
	options = dedupeShipmentOptions(options)
	destCountry := resolveDestinationCountry(snapshot)
	if err := s.validateOptions(options, snapshot.ServiceCode, destCountry); err != nil {
		return &shippingpluginpb.ResultResponse{
			Success: false,
			Failure: true,
			Code:    "400",
			Message: err.Error(),
		}, nil
	}

	shipment, err := s.createShipmentFromSnapshot(ctx, snapshot, options)
	if err != nil {
		return &shippingpluginpb.ResultResponse{
			Success: false,
			Failure: true,
			Code:    "400",
			Message: err.Error(),
		}, nil
	}

	labelURL := ""
	refundURL := ""
	for _, link := range shipment.Links.Link {
		if link.Rel == "label" {
			labelURL = link.Href
			continue
		}
		if link.Rel == "refund" {
			refundURL = link.Href
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
	if err := s.saveLabelPDF(labelID, labelPDF); err != nil {
		return &shippingpluginpb.ResultResponse{
			Success: false,
			Failure: true,
			Code:    "500",
			Message: err.Error(),
		}, nil
	}

	returnData.Label = &labels.LabelResponse{
		LabelId:     labelID,
		LabelUrl:    s.buildLabelURL(labelID),
		TackingCode: tracking,
		Carrier:     "Canada Post",
		Method:      camelCaseSpace(defaultValue(snapshot.ServiceName, "STANDARD")),
		ShipDate:    uint32(time.Now().Unix()),
		InvoiceUuid: defaultValue(snapshot.InvoiceUUID, shipRequest.GetInvoiceUuid()),
		DelayTask:   shipRequest.GetDelayTask(),
	}

	invoiceUUID := defaultValue(snapshot.InvoiceUUID, shipRequest.GetInvoiceUuid())
	if invoiceUUID != "" && shipRequest.GetShippingRateId() != "" {
		if err := s.Store.SaveChosenRateID(invoiceUUID, shipRequest.GetShippingRateId()); err != nil {
			log.Println("❌ Failed to store chosen rate:", err)
		} else {
			log.Printf("✅ Stored rate %s for invoice %s\n", shipRequest.GetShippingRateId(), invoiceUUID)
		}
	}
	if invoiceUUID != "" && tracking != "" {
		if err := s.Store.SaveTrackingNumber(invoiceUUID, tracking); err != nil {
			log.Println("❌ Failed to store tracking number:", err)
		} else {
			log.Printf("✅ Stored tracking number %s for invoice %s\n", tracking, invoiceUUID)
		}
	}

	totalWeight := snapshot.Parcel.Weight

	serviceName := strings.TrimSpace(snapshot.ServiceName)
	if serviceName == "" {
		serviceName = fallbackServiceName(snapshot.ServiceCode)
	}

	record := database.LabelRecord{
		ID:                   labelID,
		ShipmentID:           shipment.ShipmentID,
		TrackingNumber:       tracking,
		InvoiceUUID:          invoiceUUID,
		RateID:               selectedRateID,
		Carrier:              "Canada Post",
		ServiceCode:          resolveServiceCode(snapshot.ServiceCode),
		ServiceName:          serviceName,
		ShippingChargesCents: snapshot.PriceCents,
		DeliveryDate:         snapshot.DeliveryDate,
		DeliveryDays:         int(deliveryDaysFromDeliveryDate(&snapshot.DeliveryDate)),
		RefundLink:           refundURL,
		Weight:               totalWeight,
	}

	if err := s.Store.SaveLabelRecord(record); err != nil {
		log.Println("❌ Failed to store label record:", err)
	}

	return returnData, nil
}

func (s *Server) saveLabelPDF(labelID string, data []byte) error {
	storagePath := strings.TrimSpace(s.Config.LabelStoragePath)
	if storagePath == "" {
		storagePath = "files/labels"
	}

	if strings.Contains(labelID, "/") || strings.Contains(labelID, "\\") || strings.Contains(labelID, "..") {
		return fmt.Errorf("invalid label id")
	}

	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		return err
	}

	filename := labelID + ".pdf"
	filePath := filepath.Join(storagePath, filename)
	if err := os.WriteFile(filePath, data, 0o644); err != nil {
		return err
	}
	return nil
}
