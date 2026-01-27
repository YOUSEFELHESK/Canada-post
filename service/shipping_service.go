package service

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"lexmodo-plugin/config"
	"lexmodo-plugin/database"
	"lexmodo-plugin/middleware/authentication"
	ipresolver "lexmodo-plugin/middleware/ip_resolver"
	"lexmodo-plugin/middleware/timer"
	"log"
	"math"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	address "bitbucket.org/lexmodo/proto/address"
	labels "bitbucket.org/lexmodo/proto/labels"
	shippingpluginpb "bitbucket.org/lexmodo/proto/shipping_plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// ============================
// Server
// ============================
type Server struct {
	shippingpluginpb.UnimplementedShippingsServer
	Store      *database.Store
	Config     config.Config
	CanadaPost *CanadaPostClient
}

func NewServer(store *database.Store, cfg config.Config) *Server {
	return &Server{
		Store:      store,
		Config:     cfg,
		CanadaPost: NewCanadaPostClient(
			cfg.CanadaPost.Username,
			cfg.CanadaPost.Password,
			cfg.CanadaPost.CustomerNumber,
			cfg.CanadaPost.BaseURL,
		),
	}
}

type rateMeta struct {
	ServiceCode string
	ServiceName string
}

var rateMetaByID sync.Map
var ratePriceByID sync.Map
var addressByInvoice sync.Map
var addressByClientID sync.Map

type canadaPostAddressPair struct {
	Origin      canadaPostOrigin
	Destination canadaPostDestination
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
		log.Printf("rate: id=%s carrier=%s service=%s price=%d\n",
			rate.GetShippingrateId(),
			rate.GetShippingrateCarrierName(),
			rate.GetShippingrateServiceName(),
			rate.GetShippingratePrice(),
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

// ============================
// Address Mapping
// ============================
type canadaPostOrigin struct {
	PostalCode string
	AddressLine string
	City        string
	Province    string
	Name        string
}

type canadaPostDestination struct {
	Name        string
	AddressLine string
	City        string
	Province    string
	PostalCode  string
	Country     string
}

func mapCanadaPostOrigin(addr *address.Address) canadaPostOrigin {
	if addr == nil {
		return canadaPostOrigin{}
	}
	return canadaPostOrigin{
		PostalCode:  unwrapStringValue(addr.GetZip()),
		AddressLine: unwrapStringValue(addr.GetStreet1()),
		City:        unwrapStringValue(addr.GetCity()),
		Province:    unwrapStringValue(addr.GetProvinceCode()),
		Name:        unwrapStringValue(addr.GetFullName()),
	}
}

func mapCanadaPostDestination(addr *address.Address) canadaPostDestination {
	if addr == nil {
		return canadaPostDestination{}
	}
	return canadaPostDestination{
		Name:        unwrapStringValue(addr.GetFullName()),
		AddressLine: unwrapStringValue(addr.GetStreet1()),
		City:        unwrapStringValue(addr.GetCity()),
		Province:    unwrapStringValue(addr.GetProvinceCode()),
		PostalCode:  unwrapStringValue(addr.GetZip()),
		Country:     unwrapStringValue(addr.GetCountryCode()),
	}
}

func isEmptyCanadaPostAddress(origin canadaPostOrigin, dest canadaPostDestination) bool {
	return strings.TrimSpace(origin.PostalCode) == "" ||
		strings.TrimSpace(dest.PostalCode) == "" ||
		strings.TrimSpace(dest.Country) == ""
}

func storeCanadaPostAddresses(invoiceID string, origin canadaPostOrigin, dest canadaPostDestination) {
	invoiceID = strings.TrimSpace(invoiceID)
	if invoiceID == "" {
		return
	}
	if isEmptyCanadaPostAddress(origin, dest) {
		return
	}
	addressByInvoice.Store(invoiceID, canadaPostAddressPair{
		Origin:      origin,
		Destination: dest,
	})
}

func loadCanadaPostAddresses(invoiceID string) (canadaPostOrigin, canadaPostDestination, bool) {
	invoiceID = strings.TrimSpace(invoiceID)
	if invoiceID == "" {
		return canadaPostOrigin{}, canadaPostDestination{}, false
	}
	if value, ok := addressByInvoice.Load(invoiceID); ok {
		pair := value.(canadaPostAddressPair)
		return pair.Origin, pair.Destination, true
	}
	return canadaPostOrigin{}, canadaPostDestination{}, false
}

func validateCanadaPostAddress(origin canadaPostOrigin, dest canadaPostDestination) error {
	if isEmptyCanadaPostAddress(origin, dest) {
		return errors.New("origin or destination address is missing required fields")
	}
	return nil
}

// ============================
// Parcel Mapping
// ============================
type parcelMetrics struct {
	Weight float64
	Length float64
	Width  float64
	Height float64
}

func buildParcelsFromLabelRequest(parcel *labels.Parcel) (parcelMetrics, error) {
	if parcel == nil {
		return parcelMetrics{}, errors.New("parcel is required")
	}
	if parcel.GetWeight() <= 0 {
		return parcelMetrics{}, errors.New("parcel weight is required")
	}

	return parcelMetrics{
		Weight: float64(parcel.GetWeight()),
	}, nil
}

// ============================
// Rates
// ============================
func (s *Server) fetchRatesFromAPI(ctx context.Context, req *shippingpluginpb.ShippingRateRequest) ([]*shippingpluginpb.ShippingRate, error) {
	shipRequest := req.GetShipRequest()
	if shipRequest == nil {
		return nil, errors.New("missing ship_request")
	}
	if s.CanadaPost == nil {
		return nil, errors.New("canada post client not configured")
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

	origin := mapCanadaPostOrigin(shipRequest.GetShipper())
	dest := mapCanadaPostDestination(shipRequest.GetCustomer())

	storeCanadaPostAddresses(shipRequest.GetInvoiceUuid(), origin, dest)

	if err := validateCanadaPostAddress(origin, dest); err != nil {
		return nil, err
	}

	parcel, err := buildParcelsFromLabelRequest(shipRequest.GetParcel())
	if err != nil {
		return nil, err
	}

	payload := &RateRequest{}
	if strings.TrimSpace(s.Config.CanadaPost.CustomerNumber) != "" {
		payload.CustomerNumber = s.Config.CanadaPost.CustomerNumber
	}
	payload.OriginPostalCode = origin.PostalCode
	payload.ParcelCharacteristics.Weight = parcel.Weight
	if parcel.Length > 0 || parcel.Width > 0 || parcel.Height > 0 {
		payload.ParcelCharacteristics.Dimensions.Length = parcel.Length
		payload.ParcelCharacteristics.Dimensions.Width = parcel.Width
		payload.ParcelCharacteristics.Dimensions.Height = parcel.Height
	}

	switch strings.ToUpper(strings.TrimSpace(dest.Country)) {
	case "CA":
		payload.Destination.Domestic.PostalCode = dest.PostalCode
	case "US":
		payload.Destination.UnitedStates.ZipCode = dest.PostalCode
	default:
		payload.Destination.International.CountryCode = dest.Country
	}

	body, _ := json.Marshal(payload)
	log.Printf("canada post rates request payload: %s\n", string(body))

	apiRates, err := s.CanadaPost.GetRates(ctx, payload)
	if err != nil {
		return nil, err
	}

	rates := mapAPIRates(apiRates)
	if len(settings.EnabledServices) > 0 {
		rates = filterRatesByService(rates, settings.EnabledServices)
	}
	return rates, nil
}

func mapAPIRates(apiRates *RateResponse) []*shippingpluginpb.ShippingRate {
	if apiRates == nil {
		return []*shippingpluginpb.ShippingRate{}
	}
	bestByService := make(map[string]PriceQuote, len(apiRates.PriceQuotes))
	for _, rate := range apiRates.PriceQuotes {
		serviceID := strings.TrimSpace(rate.ServiceCode)
		if serviceID == "" {
			continue
		}

		best, ok := bestByService[serviceID]
		if !ok {
			bestByService[serviceID] = rate
			continue
		}

		if rate.PriceDetails.Due < best.PriceDetails.Due {
			bestByService[serviceID] = rate
		}
	}

	rates := make([]*shippingpluginpb.ShippingRate, 0, len(bestByService))
	for serviceID, rate := range bestByService {
		priceCents := int64(math.Round(rate.PriceDetails.Due * 100))
		meta := rateMeta{
			ServiceCode: strings.TrimSpace(rate.ServiceCode),
			ServiceName: strings.TrimSpace(rate.ServiceName),
		}
		storeRateMeta(getMD5Hash(serviceID), meta)
		storeRateMeta(serviceID, meta)
		storeRatePrice(serviceID, priceCents)

		rates = append(rates, &shippingpluginpb.ShippingRate{
			ShippingrateId:          serviceID,
			ShippingrateCarrierName: "Canada Post",
			ShippingrateServiceName: rate.ServiceName,
			ShippingratePrice:       uint32(priceCents),
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

func resolveServiceCode(rateID string) string {
	rateID = strings.TrimSpace(rateID)
	if rateID == "" {
		return ""
	}
	if value, ok := rateMetaByID.Load(rateID); ok {
		meta := value.(rateMeta)
		if meta.ServiceCode != "" {
			return meta.ServiceCode
		}
		if meta.ServiceName != "" {
			return meta.ServiceName
		}
	}
	return rateID
}

// ============================
// Shipment
// ============================
func (s *Server) createShipmentFromAPI(ctx context.Context, shipRequest *labels.LabelRequest) (*ShipmentResponse, error) {
	if s.CanadaPost == nil {
		return nil, errors.New("canada post client not configured")
	}

	origin := mapCanadaPostOrigin(shipRequest.GetShipper())
	dest := mapCanadaPostDestination(shipRequest.GetCustomer())

	// fallback to cached addresses if missing
	if err := validateCanadaPostAddress(origin, dest); err != nil {
		if o, d, ok := loadCanadaPostAddresses(shipRequest.GetInvoiceUuid()); ok {
			origin = o
			dest = d
		} else {
			return nil, err
		}
	}

	parcel, err := buildParcelsFromLabelRequest(shipRequest.GetParcel())
	if err != nil {
		return nil, err
	}

	payload := &ShipmentRequest{}
	payload.RequestedShippingPoint = origin.PostalCode
	payload.DeliverySpec.ServiceCode = resolveServiceCode(shipRequest.GetShippingRateId())

	senderName := strings.TrimSpace(origin.Name)
	if senderName == "" {
		senderName = "Sender"
	}
	payload.DeliverySpec.Sender.Name = senderName
	payload.DeliverySpec.Sender.Company = defaultValue(senderName, "Sender")
	payload.DeliverySpec.Sender.ContactPhone = "0000000000"
	payload.DeliverySpec.Sender.AddressDetails.AddressLine1 = defaultValue(origin.AddressLine, "")
	payload.DeliverySpec.Sender.AddressDetails.City = defaultValue(origin.City, "")
	payload.DeliverySpec.Sender.AddressDetails.ProvState = defaultValue(origin.Province, "")
	payload.DeliverySpec.Sender.AddressDetails.PostalCode = defaultValue(origin.PostalCode, "")

	payload.DeliverySpec.Destination.Name = defaultValue(dest.Name, "Recipient")
	payload.DeliverySpec.Destination.Company = ""
	payload.DeliverySpec.Destination.AddressDetails.AddressLine1 = defaultValue(dest.AddressLine, "")
	payload.DeliverySpec.Destination.AddressDetails.City = defaultValue(dest.City, "")
	payload.DeliverySpec.Destination.AddressDetails.ProvState = defaultValue(dest.Province, "")
	payload.DeliverySpec.Destination.AddressDetails.CountryCode = defaultValue(dest.Country, "")
	payload.DeliverySpec.Destination.AddressDetails.PostalCode = defaultValue(dest.PostalCode, "")

	payload.DeliverySpec.ParcelCharacteristics.Weight = parcel.Weight
	payload.DeliverySpec.ParcelCharacteristics.Dimensions.Length = parcel.Length
	payload.DeliverySpec.ParcelCharacteristics.Dimensions.Width = parcel.Width
	payload.DeliverySpec.ParcelCharacteristics.Dimensions.Height = parcel.Height
	payload.DeliverySpec.Preferences.ShowPackingInstructions = true

	body, _ := json.Marshal(payload)
	log.Printf("canada post shipment request payload: %s\n", string(body))

	shipment, err := s.CanadaPost.CreateShipment(ctx, payload)
	if err != nil {
		return nil, err
	}

	return shipment, nil
}

// ============================
// Label
// ============================
func (s *Server) buildLabelURL(labelID string) string {
	labelID = strings.TrimSpace(labelID)
	if labelID == "" {
		labelID = generateLabelID()
	}

	baseURL := strings.TrimRight(s.Config.PublicBaseURL, "/")
	if baseURL == "" {
		baseURL = "http://localhost:50050"
	}
	return fmt.Sprintf("%s/labels/%s", baseURL, labelID)
}

// ============================
// Helpers
// ============================

// func (s *Server) fetchOrderIDFromOrders(ctx context.Context, invoiceUUID string, storeID string, accessToken string) (string, error) {
// 	addr := strings.TrimSpace(s.Config.OrdersGRPCAddr)
// 	if addr == "" {
// 		addr = "192.168.1.99:7000"
// 	}

// 	conn, err := grpc.Dial(addr, grpc.WithInsecure())
// 	if err != nil {
// 		return "", err
// 	}
// 	defer conn.Close()

// 	md := metadata.New(map[string]string{
// 		"authorization": "Bearer " + accessToken,
// 		"x-client-id":   storeID,
// 		"x-force-auth":  "true",
// 	})
// 	ctx = metadata.NewOutgoingContext(ctx, md)

// 	req := &orderspb.OrdersRequest{
// 		InvoiceUuid:         invoiceUUID,
// 		ShowOnlyUnpaidItems: false,
// 	}

// 	resp, err := orderspb.NewOrdersClient(conn).Invoice(ctx, req)
// 	if err != nil {
// 		return "", err
// 	}
// 	if resp.GetInvoice() == nil {
// 		return "", nil
// 	}
// 	return strings.TrimSpace(resp.GetInvoice().GetInvoiceId()), nil
// }

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
