package service

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	address "bitbucket.org/lexmodo/proto/address"
	labels "bitbucket.org/lexmodo/proto/labels"
	orderspb "bitbucket.org/lexmodo/proto/orders"
	shippingpluginpb "bitbucket.org/lexmodo/proto/shipping_plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/wrapperspb"
	"lexmodo-plugin/config"
	"lexmodo-plugin/database"
	"lexmodo-plugin/middleware/authentication"
	ipresolver "lexmodo-plugin/middleware/ip_resolver"
	"lexmodo-plugin/middleware/timer"
)

// ============================
// Server
// ============================
type Server struct {
	shippingpluginpb.UnimplementedShippingsServer
	Store  *database.Store
	Config config.Config
	Fedex  *FedexClient
}

func NewServer(store *database.Store, cfg config.Config) *Server {
	return &Server{
		Store:  store,
		Config: cfg,
		Fedex:  NewFedexClient(cfg),
	}
}

type fedexRatesRequest struct {
	AccountNumber string         `json:"accountNumber"`
	OrderNumber   string         `json:"orderNumber"`
	PickupType    string         `json:"pickupType"`
	Currency      string         `json:"currency"`
	Shipper       fedexAddress   `json:"shipper"`
	Recipient     fedexAddress   `json:"recipient"`
	Parcels       []fedexParcel  `json:"parcels"`
	Meta          map[string]any `json:"meta,omitempty"`
}

type fedexShipmentRequest struct {
	AccountNumber string         `json:"accountNumber"`
	OrderNumber   string         `json:"orderNumber"`
	ServiceType   string         `json:"serviceType"`
	PickupType    string         `json:"pickupType"`
	Currency      string         `json:"currency"`
	Shipper       fedexAddress   `json:"shipper"`
	Recipient     fedexAddress   `json:"recipient"`
	Parcels       []fedexParcel  `json:"parcels"`
	Meta          map[string]any `json:"meta,omitempty"`
}

type fedexAddress struct {
	Company     string `json:"company"`
	FirstName   string `json:"firstName"`
	LastName    string `json:"lastName"`
	Street      string `json:"street"`
	City        string `json:"city"`
	StateCode   string `json:"stateCode"`
	PostalCode  string `json:"postalCode"`
	CountryCode string `json:"countryCode"`
	PhoneNumber string `json:"phoneNumber"`
}

type fedexParcel struct {
	WeightLbs float64 `json:"weightLbs"`
	LengthIn  float64 `json:"lengthIn"`
	WidthIn   float64 `json:"widthIn"`
	HeightIn  float64 `json:"heightIn"`
}

type fedexAPIRate struct {
	ServiceType  string              `json:"serviceType"`
	ServiceName  string              `json:"serviceName"`
	Amount       float64             `json:"amount"`
	Currency     string              `json:"currency"`
	DeliveryDate *string             `json:"deliveryDate"`
	TransitDays  *fedexTransitWindow `json:"transitDays"`
}

type fedexTransitWindow struct {
	MinimumTransitTime string `json:"minimumTransitTime"`
	Description        string `json:"description"`
}

type fedexShipmentLabel struct {
	ParcelWeight float64 `json:"parcelWeight"`
	LabelPdf     string  `json:"labelPdf"`
}

type fedexShipmentResponse struct {
	TrackingNumber string               `json:"trackingNumber"`
	ShipmentID     int64                `json:"shipmentId"`
	Labels         []fedexShipmentLabel `json:"labels"`
	Status         string               `json:"status"`
	ShipmentDate   string               `json:"shipmentDate"`
	DeliveryDate   string               `json:"deliveryDate"`
	HoldLocation   *string              `json:"holdLocation"`
}

type fedexShipmentCancelRequest struct {
	AccountNumber     string `json:"accountNumber"`
	TrackingNumber    string `json:"trackingNumber"`
	SenderCountryCode string `json:"senderCountryCode"`
	EmailShipment     bool   `json:"emailShipment"`
	DeletionControl   string `json:"deletionControl"`
}

type fedexShipmentCancelResponse struct {
	TrackingNumber   string         `json:"trackingNumber"`
	Status           string         `json:"status"`
	ProviderResponse map[string]any `json:"providerResponse"`
}

type rateMeta struct {
	ServiceType string
	ServiceName string
}

var rateMetaByID sync.Map
var ratePriceByID sync.Map
var addressByInvoice sync.Map
var addressByClientID sync.Map

type fedexAddressPair struct {
	Shipper   fedexAddress
	Recipient fedexAddress
}

// ============================
// Start gRPC Server
// ============================
func StartGRPCServer(addr string, server *Server) {
	if strings.TrimSpace(addr) == "" {
		addr = "0.0.0.0:50051"
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}

	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			authentication.UnaryServerInterceptor(),
			ipresolver.UnaryServerInterceptor(),
			timer.UnaryServerInterceptor(),
		),
		grpc.ChainStreamInterceptor(
			ipresolver.StreamServerInterceptor(),
		),
	)

	if server == nil {
		log.Fatal("gRPC server missing dependencies")
	}
	shippingpluginpb.RegisterShippingsServer(grpcServer, server)

	log.Printf("🚀 gRPC SHIPPING PLUGIN listening on %s\n", addr)
	log.Fatal(grpcServer.Serve(lis))
}

// ============================
// Metadata Logger
// ============================
func logIncomingMetadata(ctx context.Context) {
	md, ok := metadata.FromIncomingContext(ctx)
	if ok {
		log.Printf("incoming metadata: %+v\n", md)
	} else {
		log.Println("no incoming metadata found")
	}
}

func clientIDFromContext(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	values := md.Get("x-client-id")
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

func clientIDFromRequest(ctx context.Context, req *shippingpluginpb.ShippingRateRequest) int64 {
	if req != nil {
		if auth := req.GetShippingAuth(); auth != nil {
			if store := auth.GetStoreInfo(); store != nil {
				if store.GetClientId() != 0 {
					return int64(store.GetClientId())
				}
			}
		}
	}
	if value := clientIDFromContext(ctx); value != "" {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil && parsed > 0 {
			return parsed
		}
	}
	return 0
}

func logRequestDetails(_ *shippingpluginpb.ShippingRateRequest, rates []*shippingpluginpb.ShippingRate) {
	if len(rates) == 0 {
		log.Println("request details: shipping_rates <empty>")
		return
	}

	for _, rate := range rates {
		log.Printf("rate: id=%s carrier=%s service=%s price=%d delivery_days=%d delivery_date=%s guaranteed=%t\n",
			rate.GetShippingrateId(),
			rate.GetShippingrateCarrierName(),
			rate.GetShippingrateServiceName(),
			rate.GetShippingratePrice(),
			rate.GetShippingrateDeliveryDays(),
			rate.GetShippingrateDeliveryDate(),
			rate.GetShippingrateDeliveryDateGuaranteed(),
		)
	}
}

func filterRatesByService(rates []*shippingpluginpb.ShippingRate, enabled map[string]bool) []*shippingpluginpb.ShippingRate {
	if len(enabled) == 0 {
		return []*shippingpluginpb.ShippingRate{}
	}
	filtered := make([]*shippingpluginpb.ShippingRate, 0, len(rates))
	for _, rate := range rates {
		if enabled[rate.GetShippingrateId()] {
			filtered = append(filtered, rate)
		}
	}
	return filtered
}

func unwrapStringValue(value *wrapperspb.StringValue) string {
	if value == nil {
		return ""
	}
	return value.GetValue()
}

func mapFedexAddress(address *address.Address) fedexAddress {
	if address == nil {
		return fedexAddress{}
	}
	return fedexAddress{
		Company:     unwrapStringValue(address.GetCompany()),
		FirstName:   unwrapStringValue(address.GetFirstName()),
		LastName:    unwrapStringValue(address.GetLastName()),
		Street:      unwrapStringValue(address.GetStreet1()),
		City:        unwrapStringValue(address.GetCity()),
		StateCode:   unwrapStringValue(address.GetProvinceCode()),
		PostalCode:  unwrapStringValue(address.GetZip()),
		CountryCode: unwrapStringValue(address.GetCountryCode()),
		PhoneNumber: unwrapStringValue(address.GetPhone()),
	}
}

func isEmptyFedexAddress(address fedexAddress) bool {
	return strings.TrimSpace(address.Street) == "" &&
		strings.TrimSpace(address.City) == "" &&
		strings.TrimSpace(address.PostalCode) == "" &&
		strings.TrimSpace(address.CountryCode) == ""
}

func storeFedexAddresses(invoiceID string, shipper fedexAddress, recipient fedexAddress) {
	invoiceID = strings.TrimSpace(invoiceID)
	if invoiceID == "" {
		return
	}
	if isEmptyFedexAddress(shipper) || isEmptyFedexAddress(recipient) {
		return
	}
	addressByInvoice.Store(invoiceID, fedexAddressPair{
		Shipper:   shipper,
		Recipient: recipient,
	})
}

func storeFedexAddressesForClient(clientID string, shipper fedexAddress, recipient fedexAddress) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return
	}
	if isEmptyFedexAddress(shipper) || isEmptyFedexAddress(recipient) {
		return
	}
	addressByClientID.Store(clientID, fedexAddressPair{
		Shipper:   shipper,
		Recipient: recipient,
	})
}

func loadFedexAddresses(invoiceID string) (fedexAddress, fedexAddress, bool) {
	invoiceID = strings.TrimSpace(invoiceID)
	if invoiceID == "" {
		return fedexAddress{}, fedexAddress{}, false
	}
	if value, ok := addressByInvoice.Load(invoiceID); ok {
		pair := value.(fedexAddressPair)
		return pair.Shipper, pair.Recipient, true
	}
	return fedexAddress{}, fedexAddress{}, false
}

func loadFedexAddressesForClient(clientID string) (fedexAddress, fedexAddress, bool) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return fedexAddress{}, fedexAddress{}, false
	}
	if value, ok := addressByClientID.Load(clientID); ok {
		pair := value.(fedexAddressPair)
		return pair.Shipper, pair.Recipient, true
	}
	return fedexAddress{}, fedexAddress{}, false
}

func validateFedexAddress(role string, address fedexAddress) error {
	if address == (fedexAddress{}) {
		return fmt.Errorf("%s address is required", role)
	}
	if strings.TrimSpace(address.Street) == "" ||
		strings.TrimSpace(address.City) == "" ||
		strings.TrimSpace(address.PostalCode) == "" ||
		strings.TrimSpace(address.CountryCode) == "" {
		return fmt.Errorf("%s address missing required fields", role)
	}
	return nil
}

func buildParcelsFromLabelRequest(parcel *labels.Parcel) ([]fedexParcel, error) {
	if parcel == nil {
		return nil, errors.New("parcel is required")
	}
	if parcel.GetWeight() <= 0 {
		return nil, errors.New("parcel weight is required")
	}

	dimensions := parcel.GetParcelDimensions()
	var length, width, height float64
	if dimensions != nil {
		length = float64(dimensions.GetLength())
		width = float64(dimensions.GetWidth())
		height = float64(dimensions.GetHeight())
	}

	return []fedexParcel{
		{
			WeightLbs: float64(parcel.GetWeight()),
			LengthIn:  length,
			WidthIn:   width,
			HeightIn:  height,
		},
	}, nil
}

func (s *Server) fetchRatesFromAPI(ctx context.Context, req *shippingpluginpb.ShippingRateRequest) ([]*shippingpluginpb.ShippingRate, error) {
	shipRequest := req.GetShipRequest()
	if shipRequest == nil {
		return nil, errors.New("missing ship_request")
	}
	if s.Fedex == nil {
		return nil, errors.New("fedex client not configured")
	}

	settings := database.ShippingSettings{}
	clientID := clientIDFromRequest(ctx, req)
	if clientID > 0 {
		loaded, err := s.Store.LoadShippingSettings(clientID)
		if err != nil {
			log.Println("failed to load shipping settings:", err)
		} else {
			settings = loaded
		}
	}

	accountNumber := defaultValue(os.Getenv("FEDEX_ACCOUNT_NUMBER"), "319538771")
	if strings.TrimSpace(settings.AccountNumber) != "" {
		accountNumber = settings.AccountNumber
	}

	payload := fedexRatesRequest{
		AccountNumber: accountNumber,
		OrderNumber:   defaultValue(shipRequest.GetInvoiceUuid(), "L-100"),
		PickupType:    defaultValue(os.Getenv("FEDEX_PICKUP_TYPE"), "DROPOFF_AT_FEDEX_LOCATION"),
		Currency:      defaultValue(os.Getenv("FEDEX_CURRENCY"), "USD"),
		Shipper:       mapFedexAddress(shipRequest.GetShipper()),
		Recipient:     mapFedexAddress(shipRequest.GetCustomer()),
		Parcels:       []fedexParcel{},
	}
	storeFedexAddresses(shipRequest.GetInvoiceUuid(), payload.Shipper, payload.Recipient)
	storeFedexAddressesForClient(clientIDFromContext(ctx), payload.Shipper, payload.Recipient)

	if err := validateFedexAddress("shipper", payload.Shipper); err != nil {
		return nil, err
	}
	if err := validateFedexAddress("recipient", payload.Recipient); err != nil {
		return nil, err
	}

	parcels, err := buildParcelsFromLabelRequest(shipRequest.GetParcel())
	if err != nil {
		return nil, err
	}
	payload.Parcels = parcels

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	log.Printf("fedex rates request payload: %s\n", string(body))

	apiRates, err := s.Fedex.GetRates(ctx, payload)
	if err != nil {
		return nil, err
	}
	if respBytes, err := json.Marshal(apiRates); err == nil {
		log.Printf("fedex rates response payload: %s\n", string(respBytes))
	}

	rates := mapAPIRates(apiRates)
	if len(settings.EnabledServices) > 0 {
		rates = filterRatesByService(rates, settings.EnabledServices)
	}
	return rates, nil
}

func (s *Server) createShipmentFromAPI(ctx context.Context, shipRequest *labels.LabelRequest) (fedexShipmentResponse, error) {
	if s.Fedex == nil {
		return fedexShipmentResponse{}, errors.New("fedex client not configured")
	}
	accountNumber := defaultValue(os.Getenv("FEDEX_ACCOUNT_NUMBER"), "319538771")
	if clientID := clientIDFromContext(ctx); clientID != "" {
		if parsed, err := strconv.ParseInt(clientID, 10, 64); err == nil && parsed > 0 {
			if settings, err := s.Store.LoadShippingSettings(parsed); err == nil {
				if strings.TrimSpace(settings.AccountNumber) != "" {
					accountNumber = settings.AccountNumber
				}
			}
		}
	}

	payload := fedexShipmentRequest{
		AccountNumber: accountNumber,
		OrderNumber:   defaultValue(shipRequest.GetInvoiceUuid(), "L-100"),
		ServiceType:   defaultValue(resolveServiceType(shipRequest.GetShippingRateId()), "FIRST_OVERNIGHT"),
		PickupType:    defaultValue(os.Getenv("FEDEX_PICKUP_TYPE"), "DROPOFF_AT_FEDEX_LOCATION"),
		Currency:      defaultValue(os.Getenv("FEDEX_CURRENCY"), "USD"),
		Shipper:       mapFedexAddress(shipRequest.GetShipper()),
		Recipient:     mapFedexAddress(shipRequest.GetCustomer()),
		Parcels:       []fedexParcel{},
	}

	if err := validateFedexAddress("shipper", payload.Shipper); err != nil {
		if shipper, recipient, ok := loadFedexAddresses(shipRequest.GetInvoiceUuid()); ok {
			payload.Shipper = shipper
			payload.Recipient = recipient
		} else if shipper, recipient, ok := loadFedexAddressesForClient(clientIDFromContext(ctx)); ok {
			payload.Shipper = shipper
			payload.Recipient = recipient
		} else {
			return fedexShipmentResponse{}, err
		}
	}
	if err := validateFedexAddress("recipient", payload.Recipient); err != nil {
		if shipper, recipient, ok := loadFedexAddresses(shipRequest.GetInvoiceUuid()); ok {
			payload.Shipper = shipper
			payload.Recipient = recipient
		} else if shipper, recipient, ok := loadFedexAddressesForClient(clientIDFromContext(ctx)); ok {
			payload.Shipper = shipper
			payload.Recipient = recipient
		} else {
			return fedexShipmentResponse{}, err
		}
	}

	parcels, err := buildParcelsFromLabelRequest(shipRequest.GetParcel())
	if err != nil {
		return fedexShipmentResponse{}, err
	}
	payload.Parcels = parcels

	body, err := json.Marshal(payload)
	if err != nil {
		return fedexShipmentResponse{}, err
	}
	log.Printf("fedex shipment request payload: %s\n", string(body))

	shipment, err := s.Fedex.CreateShipment(ctx, payload)
	if err != nil {
		return fedexShipmentResponse{}, err
	}
	if respBytes, err := json.Marshal(shipment); err == nil {
		log.Printf("fedex shipment response payload: %s\n", string(respBytes))
	}

	return shipment, nil
}

func (s *Server) cancelShipmentFromAPI(ctx context.Context, trackingNumber string, shipper *address.Address) (fedexShipmentCancelResponse, error) {
	if s.Fedex == nil {
		return fedexShipmentCancelResponse{}, errors.New("fedex client not configured")
	}
	senderCountry := "US"
	if shipper != nil {
		senderCountry = defaultValue(unwrapStringValue(shipper.GetCountryCode()), "US")
	}

	accountNumber := defaultValue(os.Getenv("FEDEX_ACCOUNT_NUMBER"), "319538771")
	if clientID := clientIDFromContext(ctx); clientID != "" {
		if parsed, err := strconv.ParseInt(clientID, 10, 64); err == nil && parsed > 0 {
			if settings, err := s.Store.LoadShippingSettings(parsed); err == nil {
				if strings.TrimSpace(settings.AccountNumber) != "" {
					accountNumber = settings.AccountNumber
				}
			}
		}
	}

	payload := fedexShipmentCancelRequest{
		AccountNumber:     accountNumber,
		TrackingNumber:    trackingNumber,
		SenderCountryCode: senderCountry,
		EmailShipment:     false,
		DeletionControl:   defaultValue(os.Getenv("FEDEX_DELETION_CONTROL"), "DELETE_ALL_PACKAGES"),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fedexShipmentCancelResponse{}, err
	}
	log.Printf("fedex shipment cancel request payload: %s\n", string(body))

	cancelResponse, err := s.Fedex.CancelShipment(ctx, payload)
	if err != nil {
		return fedexShipmentCancelResponse{}, err
	}
	if respBytes, err := json.Marshal(cancelResponse); err == nil {
		log.Printf("fedex shipment cancel response payload: %s\n", string(respBytes))
	}

	return cancelResponse, nil
}

func (s *Server) buildLabelURL(labelID string, shipment fedexShipmentResponse) (string, error) {
	if len(shipment.Labels) == 0 || strings.TrimSpace(shipment.Labels[0].LabelPdf) == "" {
		return "", errors.New("no label returned from upstream API")
	}

	labelID = strings.TrimSpace(labelID)
	if labelID == "" {
		labelID = generateLabelID()
	}

	labelData, err := decodeBase64(shipment.Labels[0].LabelPdf)
	if err != nil {
		return "", err
	}

	if err := saveLabelPDF(labelID, labelData); err != nil {
		log.Println("failed to save label PDF:", err)
		return "data:application/pdf;base64," + shipment.Labels[0].LabelPdf, nil
	}

	baseURL := strings.TrimRight(s.Config.PublicBaseURL, "/")
	if baseURL == "" {
		baseURL = "http://localhost:50050"
	}
	return fmt.Sprintf("%s/files/postage_label/%s.pdf", baseURL, labelID), nil
}

func saveLabelPDF(labelID string, data []byte) error {
	dir := filepath.Join("files", "postage_label")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, labelID+".pdf")
	return os.WriteFile(path, data, 0o644)
}

func decodeBase64(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("label data is empty")
	}
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	return base64.RawStdEncoding.DecodeString(value)
}

func mapAPIRates(apiRates []fedexAPIRate) []*shippingpluginpb.ShippingRate {
	bestByService := make(map[string]fedexAPIRate, len(apiRates))
	for _, rate := range apiRates {
		serviceID := strings.TrimSpace(rate.ServiceType)
		if serviceID == "" {
			serviceID = strings.TrimSpace(rate.ServiceName)
		}
		if serviceID == "" {
			continue
		}

		best, ok := bestByService[serviceID]
		if !ok {
			bestByService[serviceID] = rate
			continue
		}

		// Prefer lowest price; if equal, prefer earliest delivery date.
		if rate.Amount < best.Amount {
			bestByService[serviceID] = rate
			continue
		}

	}

	rates := make([]*shippingpluginpb.ShippingRate, 0, len(bestByService))
	for serviceID, rate := range bestByService {
		deliveryDate := ""
		deliveryDays := uint32(0)
		if rate.DeliveryDate != nil {
			deliveryDate = strings.TrimSpace(*rate.DeliveryDate)
			deliveryDays = deliveryDaysFromDeliveryDate(rate.DeliveryDate)
		}

		priceCents := int64(math.Round(rate.Amount * 100))
		meta := rateMeta{
			ServiceType: strings.TrimSpace(rate.ServiceType),
			ServiceName: strings.TrimSpace(rate.ServiceName),
		}
		storeRateMeta(getMD5Hash(serviceID), meta)
		storeRateMeta(serviceID, meta)
		storeRatePrice(serviceID, priceCents)

		rates = append(rates, &shippingpluginpb.ShippingRate{
			ShippingrateId:                     serviceID,
			ShippingrateCarrierName:            "FedEx",
			ShippingrateServiceName:            rate.ServiceName,
			ShippingratePrice:                  uint32(priceCents),
			ShippingrateDeliveryDays:           deliveryDays,
			ShippingrateDeliveryDate:           deliveryDate,
			ShippingrateDeliveryDateGuaranteed: false,
		})
	}
	return rates
}

func storeRateMeta(id string, meta rateMeta) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	rateMetaByID.Store(id, meta)
}

func storeRatePrice(id string, priceCents int64) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	ratePriceByID.Store(id, priceCents)
}

func lookupRatePrice(id string) int64 {
	id = strings.TrimSpace(id)
	if id == "" {
		return 0
	}
	if value, ok := ratePriceByID.Load(id); ok {
		if price, ok := value.(int64); ok {
			return price
		}
	}
	return 0
}

func resolveServiceType(rateID string) string {
	rateID = strings.TrimSpace(rateID)
	if rateID == "" {
		return ""
	}
	if value, ok := rateMetaByID.Load(rateID); ok {
		meta := value.(rateMeta)
		if meta.ServiceType != "" {
			return meta.ServiceType
		}
		if meta.ServiceName != "" {
			return meta.ServiceName
		}
	}
	return rateID
}

func (s *Server) resolveOrderID(ctx context.Context, invoiceUUID string) string {
	invoiceUUID = strings.TrimSpace(invoiceUUID)
	if invoiceUUID == "" {
		return ""
	}

	storeID := clientIDFromContext(ctx)
	if storeID == "" {
		return invoiceUUID
	}
	storeIDInt, err := strconv.Atoi(storeID)
	if err != nil || storeIDInt <= 0 {
		return invoiceUUID
	}

	accessToken := s.Store.GetAccessToken(storeIDInt)
	if strings.TrimSpace(accessToken) == "" {
		return invoiceUUID
	}

	orderID, err := s.fetchOrderIDFromOrders(ctx, invoiceUUID, storeID, accessToken)
	if err != nil {
		log.Println("failed to resolve order id:", err)
		return invoiceUUID
	}
	if orderID == "" {
		return invoiceUUID
	}
	return orderID
}

func (s *Server) fetchOrderIDFromOrders(ctx context.Context, invoiceUUID string, storeID string, accessToken string) (string, error) {
	addr := strings.TrimSpace(s.Config.OrdersGRPCAddr)
	if addr == "" {
		addr = "192.168.1.99:7000"
	}

	conn, err := grpc.Dial(addr, grpc.WithInsecure())
	if err != nil {
		return "", err
	}
	defer conn.Close()

	md := metadata.New(map[string]string{
		"authorization": "Bearer " + accessToken,
		"x-client-id":   storeID,
		"x-force-auth":  "true",
	})
	ctx = metadata.NewOutgoingContext(ctx, md)

	req := &orderspb.OrdersRequest{
		InvoiceUuid:         invoiceUUID,
		ShowOnlyUnpaidItems: false,
	}

	resp, err := orderspb.NewOrdersClient(conn).Invoice(ctx, req)
	if err != nil {
		return "", err
	}
	if resp.GetInvoice() == nil {
		return "", nil
	}
	return strings.TrimSpace(resp.GetInvoice().GetInvoiceId()), nil
}

func defaultValue(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func parseDeliveryDate(deliveryDate *string) (time.Time, bool) {
	if deliveryDate == nil || strings.TrimSpace(*deliveryDate) == "" {
		return time.Time{}, false
	}

	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
		"2006-01-02",
		time.RFC1123,
	}

	dateStr := strings.TrimSpace(*deliveryDate)
	for _, format := range formats {
		if parsed, err := time.Parse(format, dateStr); err == nil {
			return parsed, true
		}
	}

	return time.Time{}, false
}

func deliveryDaysFromDeliveryDate(deliveryDate *string) uint32 {
	parsedDelivery, ok := parseDeliveryDate(deliveryDate)
	if !ok {
		return 0
	}

	start := time.Now().UTC()
	if parsedDelivery.Before(start) {
		return 0
	}

	hours := parsedDelivery.Sub(start).Hours()
	return uint32(math.Ceil(hours / 24))
}

// ============================
// Dummy Provider Mapping
// ============================

func camelCaseSpace(value string) string {
	value = strings.ReplaceAll(value, "_", " ")
	value = strings.ReplaceAll(value, "-", " ")
	return strings.TrimSpace(value)
}

// ============================
// Helpers
// ============================

func getMD5Hash(value string) string {
	value = strings.TrimSpace(value)
	hash := md5.Sum([]byte(value))
	return hex.EncodeToString(hash[:])
}

func generateLabelID() string {
	identifier := make([]byte, 16)
	if _, err := rand.Read(identifier); err != nil {
		return getMD5Hash(time.Now().UTC().Format(time.RFC3339Nano))
	}
	return hex.EncodeToString(identifier)
}
