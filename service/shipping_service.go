package service

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	address "bitbucket.org/lexmodo/proto/address"
	labels "bitbucket.org/lexmodo/proto/labels"
	shippingpluginpb "bitbucket.org/lexmodo/proto/shipping_plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// ============================
// Server
// ============================
type Server struct {
	shippingpluginpb.UnimplementedShippingsServer
	Store         *database.Store
	Config        config.Config
	CanadaPost    *CanadaPostClient
	RateSnapshots *RateSnapshotStore
	PostOffices   *PostOfficeService
}

func NewServer(store *database.Store, cfg config.Config) *Server {
	canadaPost := NewCanadaPostClient(
		cfg.CanadaPost.Username,
		cfg.CanadaPost.Password,
		cfg.CanadaPost.CustomerNumber,
		cfg.CanadaPost.BaseURL,
	)
	var postOffices *PostOfficeService
	if store != nil {
		postOffices = NewPostOfficeService(canadaPost, store.DB)
	}
	return &Server{
		Store:         store,
		Config:        cfg,
		CanadaPost:    canadaPost,
		RateSnapshots: NewRateSnapshotStore(cfg.Redis),
		PostOffices:   postOffices,
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
	reflection.Register(grpcServer)
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
	PostalCode  string
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
	if strings.TrimSpace(origin.PostalCode) == "" {
		return true
	}
	country := strings.ToUpper(strings.TrimSpace(dest.Country))
	if country == "" {
		return true
	}
	if country == "CA" || country == "US" {
		return strings.TrimSpace(dest.PostalCode) == ""
	}
	return false
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

type rateCandidate struct {
	ServiceCode  string
	ServiceName  string
	PriceCents   int64
	DeliveryDate string
}

const ouncesPerKilogram = 35.27396195

func ouncesToKilograms(ounces float64) float64 {
	if ounces <= 0 {
		return 0
	}
	return math.Round(ounces/ouncesPerKilogram*100) / 100
}

func buildParcelsFromLabelRequest(parcel *labels.Parcel) (parcelMetrics, error) {
	if parcel == nil {
		return parcelMetrics{}, errors.New("parcel is required")
	}
	if parcel.GetWeight() <= 0 {
		return parcelMetrics{}, errors.New("parcel weight is required")
	}
	// Convert ounces (request payload) to kilograms (Canada Post).
	weightKg := ouncesToKilograms(float64(parcel.GetWeight()))
	return parcelMetrics{
		Weight: weightKg,
		Length: float64(parcel.GetParcelDimensions().GetLength()),
		Width:  float64(parcel.GetParcelDimensions().GetWidth()),
		Height: float64(parcel.GetParcelDimensions().GetHeight()),
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

	customInfo := req.GetShippingpluginreqeustCustomInfo()
	customValues := buildOptionsMap(customInfo)
	if err := s.validateCustomInfoMapValues(customValues); err != nil {
		return nil, err
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
	currencyCode := resolveRequestCurrency(ctx, req)
	rateToCad := 1.0
	if currencyCode != "CAD" {
		if clientID == 0 {
			return nil, errors.New("client_id required for currency conversion")
		}
		if s.Store == nil {
			return nil, errors.New("currency rates store not configured")
		}
		rate, ok, err := s.Store.LoadCurrencyRate(clientID, currencyCode)
		if err != nil {
			return nil, fmt.Errorf("failed to load currency rate: %w", err)
		}
		if !ok {
			return nil, fmt.Errorf("missing conversion rate for %s. set it in settings", currencyCode)
		}
		rateToCad = rate
	}
	log.Printf("currency conversion: client_id=%d currency=%s rate_to_cad=%.6f", clientID, currencyCode, rateToCad)

	origin := mapCanadaPostOrigin(shipRequest.GetShipper())
	dest := mapCanadaPostDestination(shipRequest.GetCustomer())
	recipientPhone := unwrapStringValue(shipRequest.GetCustomer().GetPhone())
	if err := validateCanadaPostOptionRules(customValues, shipRequest.GetSignature().String(), recipientPhone, dest.Country, rateToCad); err != nil {
		return nil, err
	}

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
	country := strings.ToUpper(strings.TrimSpace(dest.Country))
	switch country {
	case "CA":
		payload.Destination.Domestic = &struct {
			PostalCode string `xml:"postal-code"`
		}{
			PostalCode: dest.PostalCode,
		}

	case "US":
		payload.Destination.UnitedStates = &struct {
			ZipCode string `xml:"zip-code"`
		}{
			ZipCode: dest.PostalCode,
		}

	default:
		payload.Destination.International = &struct {
			CountryCode string `xml:"country-code"`
		}{
			CountryCode: dest.Country,
		}
	}

	rateOptions, err := buildGetRatesOptions(customValues, rateToCad, shipRequest.GetSignature().String())
	if err != nil {
		return nil, err
	}
	if len(rateOptions) > 0 {
		payload.Options = &RateOptions{Option: rateOptions}
	}

	body, _ := xml.MarshalIndent(payload, "", "  ")

	log.Printf("canada post rates request payload:\n%s\n", string(body))

	apiRates, err := s.CanadaPost.GetRates(ctx, payload)
	if err != nil {
		return nil, err
	}

	candidates := mapAPIRates(apiRates)
	if len(settings.EnabledServices) > 0 {
		candidates = filterRateCandidatesByService(candidates, settings.EnabledServices)
	}

	rates := make([]*shippingpluginpb.ShippingRate, 0, len(candidates))
	for _, candidate := range candidates {
		rateID := generateRateSessionID()
		displayPriceCents := candidate.PriceCents
		if currencyCode != "CAD" {
			displayPriceCents = convertCadToCurrencyCents(candidate.PriceCents, rateToCad)
		}
		log.Printf("rate conversion: service=%s cad_cents=%d converted_cents=%d currency=%s rate_to_cad=%.6f",
			candidate.ServiceCode,
			candidate.PriceCents,
			displayPriceCents,
			currencyCode,
			rateToCad,
		)
		snapshot := RateSnapshot{
			RateID:        rateID,
			ServiceCode:   candidate.ServiceCode,
			ServiceName:   candidate.ServiceName,
			PriceCents:    candidate.PriceCents,
			CurrencyCode:  currencyCode,
			RateToCad:     rateToCad,
			DeliveryDate:  candidate.DeliveryDate,
			Signature:     shipRequest.GetSignature().String(),
			CustomOptions: cloneOptionsMap(customValues),
			Shipper:       snapshotAddress(shipRequest.GetShipper()),
			Customer:      snapshotAddress(shipRequest.GetCustomer()),
			Origin:        origin,
			Destination:   dest,
			Parcel:        parcel,
			CustomsInfo:   snapshotCustoms(shipRequest.GetCustomsInfo()),
			Insurance:     snapshotInsurance(shipRequest.GetInsurance()),
			InvoiceUUID:   shipRequest.GetInvoiceUuid(),
			ClientID:      clientID,
			CreatedAt:     time.Now().UTC(),
		}
		if s.RateSnapshots != nil {
			if err := s.RateSnapshots.Save(ctx, snapshot); err != nil {
				logSnapshotStoreError(rateID, err)
			} else {
				log.Printf("✅ Snapshot stored: rate_id=%s service_code=%s service_name=%s dest_country=%s",
					rateID,
					snapshot.ServiceCode,
					snapshot.ServiceName,
					defaultValue(snapshot.Customer.CountryCode, snapshot.Destination.Country),
				)
			}
		} else {
			log.Printf("rate snapshot store not configured; skipping snapshot for %s\n", rateID)
		}

		meta := rateMeta{
			ServiceCode: candidate.ServiceCode,
			ServiceName: candidate.ServiceName,
		}
		storeRateMeta(rateID, meta)
		storeRatePrice(rateID, candidate.PriceCents)

		rates = append(rates, &shippingpluginpb.ShippingRate{
			ShippingrateId:          rateID,
			ShippingrateCarrierName: "Canada Post",
			ShippingrateServiceName: candidate.ServiceName,
			ShippingratePrice:       uint32(displayPriceCents),
		})
	}
	return rates, nil
}

func mapAPIRates(apiRates *RateResponse) []rateCandidate {
	if apiRates == nil {
		return []rateCandidate{}
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

	rates := make([]rateCandidate, 0, len(bestByService))
	for _, rate := range bestByService {
		priceCents := int64(math.Round(rate.PriceDetails.Due * 100))
		serviceName := strings.TrimSpace(rate.ServiceName)
		if serviceName == "" {
			serviceName = fallbackServiceName(rate.ServiceCode)
		}
		rates = append(rates, rateCandidate{
			ServiceCode:  strings.TrimSpace(rate.ServiceCode),
			ServiceName:  serviceName,
			PriceCents:   priceCents,
			DeliveryDate: strings.TrimSpace(rate.ServiceStandard.ExpectedDeliveryDate),
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

func filterRateCandidatesByService(candidates []rateCandidate, enabled map[string]bool) []rateCandidate {
	if len(enabled) == 0 || len(candidates) == 0 {
		return []rateCandidate{}
	}
	filtered := make([]rateCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if enabled[candidate.ServiceCode] {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

func fallbackServiceName(serviceCode string) string {
	switch strings.TrimSpace(strings.ToUpper(serviceCode)) {
	case "DOM.RP":
		return "Regular Parcel"
	case "DOM.EP":
		return "Expedited Parcel"
	case "DOM.XP":
		return "Xpresspost"
	case "DOM.XP.CERT":
		return "Xpresspost Certified"
	case "DOM.PC":
		return "Priority"
	case "DOM.LIB":
		return "Library Materials"
	case "USA.EP":
		return "Expedited Parcel USA"
	case "USA.SP.AIR":
		return "Small Packet USA Air"
	case "USA.TP":
		return "Tracked Packet USA"
	case "USA.TP.LVM":
		return "Tracked Packet USA (LVM)"
	case "USA.XP":
		return "Xpresspost USA"
	case "INT.XP":
		return "Xpresspost International"
	case "INT.IP.AIR":
		return "International Parcel Air"
	case "INT.IP.SURF":
		return "International Parcel Surface"
	case "INT.SP.AIR":
		return "Small Packet International Air"
	case "INT.SP.SURF":
		return "Small Packet International Surface"
	case "INT.TP":
		return "Tracked Packet International"
	default:
		return strings.TrimSpace(serviceCode)
	}
}

// ============================
// Shipment
// ============================
func (s *Server) createShipmentFromAPI(ctx context.Context, shipRequest *labels.LabelRequest) (*ShipmentResponse, error) {
	log.Println("🔵 createShipmentFromAPI STARTED")

	if s.CanadaPost == nil {
		return nil, errors.New("canada post client not configured")
	}

	log.Printf("🔵 Shipper: %+v\n", shipRequest.GetShipper())
	log.Printf("🔵 Customer: %+v\n", shipRequest.GetCustomer())
	origin := mapCanadaPostOrigin(shipRequest.GetShipper())
	dest := mapCanadaPostDestination(shipRequest.GetCustomer())
	log.Printf("🔵 Origin mapped: %+v\n", origin)
	log.Printf("🔵 Destination mapped: %+v\n", dest)

	// fallback to cached addresses if missing
	if err := validateCanadaPostAddress(origin, dest); err != nil {
		log.Printf("⚠️  Address validation failed: %v\n", err)
		if o, d, ok := loadCanadaPostAddresses(shipRequest.GetInvoiceUuid()); ok {
			log.Println("✅ Using cached addresses")
			origin = o
			dest = d
		} else {
			log.Println("❌ No cached addresses found")
			return nil, err
		}
	}

	parcel, err := buildParcelsFromLabelRequest(shipRequest.GetParcel())
	if err != nil {
		log.Printf("❌ Parcel build failed: %v\n", err)
		return nil, err
	}

	log.Printf("🔵 Parcel: %+v\n", parcel)
	payload := buildShipmentRequest(origin, dest, parcel, resolveServiceCode(shipRequest.GetShippingRateId()))

	body, _ := json.Marshal(payload)
	log.Printf("canada post shipment request payload: %s\n", string(body))

	shipment, err := s.CanadaPost.CreateShipment(ctx, payload)
	if err != nil {
		return nil, err
	}

	return shipment, nil
}

func (s *Server) createShipmentFromSnapshot(ctx context.Context, snapshot RateSnapshot, options []ShipmentOption, notification *ShipmentNotification) (*ShipmentResponse, error) {
	if s.CanadaPost == nil {
		return nil, errors.New("canada post client not configured")
	}
	destCountry := resolveDestinationCountry(snapshot)
	hasCustoms := snapshot.CustomsInfo != nil
	phoneErr := validateCanadaPostPhone(snapshot.Shipper.Phone)
	log.Printf("🔍 Validating: country=%s has_customs=%v phone_valid=%v",
		destCountry,
		hasCustoms,
		phoneErr == nil,
	)
	if err := validateShipmentSnapshot(snapshot, destCountry); err != nil {
		return nil, err
	}
	if err := validateCanadaPostAddress(snapshot.Origin, snapshot.Destination); err != nil {
		return nil, err
	}
	if snapshot.Parcel.Weight <= 0 {
		return nil, errors.New("parcel weight is required")
	}
	if requiresCustoms(destCountry) {
		if snapshot.CustomsInfo == nil || len(snapshot.CustomsInfo.CustomItems) == 0 {
			return nil, errors.New("customs info required for international shipments")
		}
		if err := validateCustoms(snapshot); err != nil {
			return nil, err
		}
		if customsNeedsConversion(snapshot) && conversionFromCAD(snapshot) == "" {
			return nil, errors.New("conversion-from-cad required when customs currency is not CAD")
		}
	}
	if requiresClientVoice(snapshot.ServiceCode) {
		if strings.TrimSpace(snapshot.Customer.Phone) == "" {
			return nil, errors.New("customer phone required for selected service")
		}
		if err := validateCanadaPostPhone(snapshot.Customer.Phone); err != nil {
			return nil, fmt.Errorf("invalid customer phone number: %w", err)
		}
	}

	payload := buildShipmentRequestFromSnapshot(snapshot, destCountry, options, notification)
	body, _ := json.Marshal(payload)
	log.Printf("canada post shipment request payload: %s\n", string(body))

	return s.CanadaPost.CreateShipment(ctx, payload)
}

func buildShipmentRequest(origin canadaPostOrigin, dest canadaPostDestination, parcel parcelMetrics, serviceCode string) *ShipmentRequest {
	payload := &ShipmentRequest{}
	payload.RequestedShippingPoint = origin.PostalCode
	payload.DeliverySpec.ServiceCode = strings.TrimSpace(serviceCode)

	senderName := strings.TrimSpace(origin.Name)
	if senderName == "" {
		senderName = "Sender"
	}
	payload.DeliverySpec.Sender.Name = senderName
	payload.DeliverySpec.Sender.Company = defaultValue(senderName, "Sender")
	payload.DeliverySpec.Sender.ContactPhone = "0000000000"
	payload.DeliverySpec.Sender.AddressDetails.AddressLine1 = sanitizeAddressLine(defaultValue(origin.AddressLine, ""))
	payload.DeliverySpec.Sender.AddressDetails.City = defaultValue(origin.City, "")
	payload.DeliverySpec.Sender.AddressDetails.ProvState = defaultValue(origin.Province, "")
	payload.DeliverySpec.Sender.AddressDetails.PostalCode = defaultValue(origin.PostalCode, "")

	payload.DeliverySpec.Destination.Name = defaultValue(dest.Name, "Recipient")
	payload.DeliverySpec.Destination.Company = ""
	payload.DeliverySpec.Destination.AddressDetails.AddressLine1 = sanitizeAddressLine(defaultValue(dest.AddressLine, ""))
	payload.DeliverySpec.Destination.AddressDetails.City = defaultValue(dest.City, "")
	payload.DeliverySpec.Destination.AddressDetails.ProvState = defaultValue(dest.Province, "")
	payload.DeliverySpec.Destination.AddressDetails.CountryCode = defaultValue(dest.Country, "")
	payload.DeliverySpec.Destination.AddressDetails.PostalCode = defaultValue(dest.PostalCode, "")

	payload.DeliverySpec.ParcelCharacteristics.Weight = parcel.Weight
	payload.DeliverySpec.ParcelCharacteristics.Dimensions.Length = parcel.Length
	payload.DeliverySpec.ParcelCharacteristics.Dimensions.Width = parcel.Width
	payload.DeliverySpec.ParcelCharacteristics.Dimensions.Height = parcel.Height
	payload.DeliverySpec.Preferences.ShowPackingInstructions = true
	return payload
}

func buildShipmentRequestFromSnapshot(snapshot RateSnapshot, destCountry string, options []ShipmentOption, notification *ShipmentNotification) *ShipmentRequest {
	payload := &ShipmentRequest{}
	payload.RequestedShippingPoint = defaultValue(snapshot.Origin.PostalCode, snapshot.Shipper.Zip)
	payload.DeliverySpec.ServiceCode = strings.TrimSpace(snapshot.ServiceCode)

	senderName := defaultValue(snapshot.Shipper.FullName, "Sender")
	payload.DeliverySpec.Sender.Name = senderName
	payload.DeliverySpec.Sender.Company = defaultValue(snapshot.Shipper.Company, senderName)
	payload.DeliverySpec.Sender.ContactPhone = strings.TrimSpace(snapshot.Shipper.Phone)
	payload.DeliverySpec.Sender.AddressDetails.AddressLine1 = sanitizeAddressLine(defaultValue(snapshot.Shipper.Street1, snapshot.Origin.AddressLine))
	payload.DeliverySpec.Sender.AddressDetails.AddressLine2 = sanitizeAddressLine(strings.TrimSpace(snapshot.Shipper.Street2))
	payload.DeliverySpec.Sender.AddressDetails.City = defaultValue(snapshot.Shipper.City, snapshot.Origin.City)
	payload.DeliverySpec.Sender.AddressDetails.ProvState = defaultValue(defaultValue(snapshot.Shipper.ProvinceCode, snapshot.Shipper.Province), snapshot.Origin.Province)
	payload.DeliverySpec.Sender.AddressDetails.PostalCode = defaultValue(snapshot.Shipper.Zip, snapshot.Origin.PostalCode)

	recipientName := strings.TrimSpace(snapshot.Customer.FullName)
	if recipientName == "" {
		recipientName = strings.TrimSpace(strings.TrimSpace(snapshot.Customer.FirstName + " " + snapshot.Customer.LastName))
	}
	payload.DeliverySpec.Destination.Name = defaultValue(recipientName, "Recipient")
	payload.DeliverySpec.Destination.Company = strings.TrimSpace(snapshot.Customer.Company)
	// D2PO requires recipient phone in destination/client-voice-number.
	if requiresClientVoice(snapshot.ServiceCode) || hasShipmentOptionCode(options, "D2PO") {
		payload.DeliverySpec.Destination.ClientVoiceNumber = strings.TrimSpace(snapshot.Customer.Phone)
	}
	payload.DeliverySpec.Destination.AddressDetails.AddressLine1 = sanitizeAddressLine(defaultValue(snapshot.Customer.Street1, snapshot.Destination.AddressLine))
	payload.DeliverySpec.Destination.AddressDetails.AddressLine2 = sanitizeAddressLine(strings.TrimSpace(snapshot.Customer.Street2))
	payload.DeliverySpec.Destination.AddressDetails.City = defaultValue(snapshot.Customer.City, snapshot.Destination.City)
	payload.DeliverySpec.Destination.AddressDetails.ProvState = defaultValue(defaultValue(snapshot.Customer.ProvinceCode, snapshot.Customer.Province), snapshot.Destination.Province)
	payload.DeliverySpec.Destination.AddressDetails.CountryCode = defaultValue(defaultValue(snapshot.Customer.CountryCode, snapshot.Customer.Country), snapshot.Destination.Country)
	payload.DeliverySpec.Destination.AddressDetails.PostalCode = defaultValue(snapshot.Customer.Zip, snapshot.Destination.PostalCode)

	payload.DeliverySpec.ParcelCharacteristics.Weight = snapshot.Parcel.Weight
	payload.DeliverySpec.ParcelCharacteristics.Dimensions.Length = snapshot.Parcel.Length
	payload.DeliverySpec.ParcelCharacteristics.Dimensions.Width = snapshot.Parcel.Width
	payload.DeliverySpec.ParcelCharacteristics.Dimensions.Height = snapshot.Parcel.Height
	if notification != nil {
		payload.DeliverySpec.Notification = notification
	}
	payload.DeliverySpec.Preferences.ShowPackingInstructions = true

	finalOptions := mergeShipmentOptions(options, destCountry)
	if len(finalOptions) > 0 {
		payload.DeliverySpec.Options = &ShipmentOptions{Option: finalOptions}
	}

	if requiresCustoms(destCountry) {
		payload.DeliverySpec.Customs = buildShipmentCustoms(snapshot.CustomsInfo, snapshot.CurrencyCode, snapshot.RateToCad, conversionFromCAD(snapshot))
	}
	return payload
}

func hasShipmentOptionCode(options []ShipmentOption, code string) bool {
	for _, opt := range options {
		if strings.EqualFold(strings.TrimSpace(opt.Code), code) {
			return true
		}
	}
	return false
}

func buildShipmentCustoms(info *customsSnapshot, fallbackCurrency string, rateToCad float64, conversion string) *ShipmentCustoms {
	if info == nil {
		return nil
	}
	currency := normalizeCurrencyCode(info.Currency)
	if currency == "" {
		currency = normalizeCurrencyCode(fallbackCurrency)
	}
	if currency == "" {
		currency = "USD"
	}
	convertValuesToCAD := currency != "CAD" && rateToCad > 0
	currencyToSend := currency
	conversionToSend := strings.TrimSpace(conversion)
	customs := &ShipmentCustoms{
		USDeclarationID:   strings.TrimSpace(info.EelPfc),
		Currency:          currencyToSend,
		ConversionFromCAD: conversionToSend,
		ReasonForExport:   mapReasonForExport(info.ContentsType),
	}
	for _, item := range info.CustomItems {
		units := item.Quantity
		if units <= 0 {
			units = 1
		}
		valuePerUnit := 0.0
		if item.TotalValueCents > 0 && units > 0 {
			valuePerUnit = float64(item.TotalValueCents) / 100.0 / float64(units)
			if convertValuesToCAD {
				valuePerUnit *= rateToCad
			}
		}
		customs.SkuList.Item = append(customs.SkuList.Item, ShipmentCustomsItem{
			CustomsNumberOfUnits: units,
			CustomsDescription:   defaultValue(item.Description, item.Code),
			UnitWeight:           item.Weight,
			CustomsValuePerUnit:  valuePerUnit,
			HSTariffCode:         item.HSTariffNumber,
			SKU:                  item.Code,
			CountryOfOrigin:      item.OriginCountry,
		})
	}
	return customs
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
	return fmt.Sprintf("%s/labels/%s.pdf", baseURL, labelID)
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

func generateRateSessionID() string {
	identifier := make([]byte, 16)
	if _, err := rand.Read(identifier); err != nil {
		return getMD5Hash(time.Now().UTC().Format(time.RFC3339Nano))
	}
	identifier[6] = (identifier[6] & 0x0f) | 0x40
	identifier[8] = (identifier[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		identifier[0:4],
		identifier[4:6],
		identifier[6:8],
		identifier[8:10],
		identifier[10:16],
	)
}

func defaultValue(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

const defaultNonDeliveryOption = "RASE"

var clientVoiceRequiredServices = map[string]struct{}{
	"USA.EP": {},
	"USA.XP": {},
	"USA.TP": {},
	"INT.XP": {},
	"INT.TP": {},
}

func requiresClientVoice(serviceCode string) bool {
	serviceCode = strings.TrimSpace(strings.ToUpper(serviceCode))
	_, ok := clientVoiceRequiredServices[serviceCode]
	return ok
}

func requiresCustoms(countryCode string) bool {
	code := strings.TrimSpace(strings.ToUpper(countryCode))
	return code != "" && code != "CA"
}

func validatePhone(phone string) bool {
	return validateCanadaPostPhone(phone) == nil
}

func normalizeCurrencyCode(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func currencyFromMetadata(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	values := md.Get("x-currency-code")
	if len(values) == 0 {
		return ""
	}
	return normalizeCurrencyCode(values[0])
}

func resolveRequestCurrency(ctx context.Context, req *shippingpluginpb.ShippingRateRequest) string {
	if code := currencyFromMetadata(ctx); code != "" {
		return code
	}
	if req == nil {
		return "CAD"
	}
	ship := req.GetShipRequest()
	if ship == nil {
		return "CAD"
	}
	if parcel := ship.GetParcel(); parcel != nil {
		if money := parcel.GetInsurance(); money != nil {
			if code := normalizeCurrencyCode(money.GetCurrencyCode()); code != "" {
				return code
			}
		}
		for _, item := range parcel.GetParcelItems() {
			if item == nil {
				continue
			}
			if money := item.GetItemsRequestPrice(); money != nil {
				if code := normalizeCurrencyCode(money.GetCurrencyCode()); code != "" {
					return code
				}
			}
			if money := item.GetItemsRequestTotalPrice(); money != nil {
				if code := normalizeCurrencyCode(money.GetCurrencyCode()); code != "" {
					return code
				}
			}
		}
	}
	if insurance := ship.GetInsurance(); insurance != nil {
		if code := normalizeCurrencyCode(insurance.GetCurrencyCode()); code != "" {
			return code
		}
	}
	if customs := ship.GetCustomsInfo(); customs != nil {
		for _, item := range customs.GetCustomItems() {
			if item == nil || item.GetTotalValue() == nil {
				continue
			}
			if code := normalizeCurrencyCode(item.GetTotalValue().GetCurrencyCode()); code != "" {
				return code
			}
		}
	}
	return "CAD"
}

func resolveSnapshotCurrency(snapshot RateSnapshot) string {
	if snapshot.CustomsInfo != nil {
		if code := normalizeCurrencyCode(snapshot.CustomsInfo.Currency); code != "" {
			return code
		}
	}
	if code := normalizeCurrencyCode(snapshot.CurrencyCode); code != "" {
		return code
	}
	if code := normalizeCurrencyCode(snapshot.Insurance.CurrencyCode); code != "" {
		return code
	}
	return "CAD"
}

func convertCadToCurrencyCents(cadCents int64, rateToCad float64) int64 {
	if rateToCad <= 0 {
		return cadCents
	}
	return int64(math.Round(float64(cadCents) / rateToCad))
}

func convertCurrencyToCadCents(amountCents int64, rateToCad float64) int64 {
	if rateToCad <= 0 {
		return amountCents
	}
	return int64(math.Round(float64(amountCents) * rateToCad))
}

func resolveDestinationCountry(snapshot RateSnapshot) string {
	if code := strings.TrimSpace(snapshot.Customer.CountryCode); code != "" {
		return strings.ToUpper(code)
	}
	if country := strings.TrimSpace(snapshot.Customer.Country); country != "" {
		return strings.ToUpper(country)
	}
	if country := strings.TrimSpace(snapshot.Destination.Country); country != "" {
		return strings.ToUpper(country)
	}
	return ""
}

func customsNeedsConversion(snapshot RateSnapshot) bool {
	if snapshot.CustomsInfo == nil {
		return false
	}
	currency := normalizeCurrencyCode(snapshot.CustomsInfo.Currency)
	if currency == "" {
		currency = resolveSnapshotCurrency(snapshot)
	}
	return currency != "" && currency != "CAD"
}

func conversionFromCAD(snapshot RateSnapshot) string {
	if !customsNeedsConversion(snapshot) {
		return ""
	}
	if snapshot.RateToCad > 0 && normalizeCurrencyCode(snapshot.CurrencyCode) != "CAD" {
		return formatRate(1.0 / snapshot.RateToCad)
	}
	if value := normalizeDecimal(snapshot.Insurance.Decimal); value != "" {
		return value
	}
	if snapshot.Insurance.Amount > 0 {
		return fmt.Sprintf("%.2f", float64(snapshot.Insurance.Amount)/100.0)
	}
	return ""
}

func formatRate(value float64) string {
	if value <= 0 {
		return ""
	}
	formatted := fmt.Sprintf("%.6f", value)
	formatted = strings.TrimRight(formatted, "0")
	formatted = strings.TrimRight(formatted, ".")
	return formatted
}

func mapReasonForExport(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "gift", "gft":
		return "GFT"
	case "documents", "document", "doc":
		return "DOC"
	case "sample", "sam":
		return "SAM"
	case "return", "returned_goods", "ret":
		return "RET"
	case "repair", "rep":
		return "REP"
	case "intercompany_transfer", "intercompany", "int":
		return "INT"
	case "merchandise", "sale", "commercial", "merch":
		return "SOG"
	default:
		return "SOG"
	}
}

func sanitizeAddressLine(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	const maxLen = 44
	runes := []rune(value)
	if len(runes) > maxLen {
		return strings.TrimSpace(string(runes[:maxLen]))
	}
	return value
}

const (
	maxCompanyLen        = 44
	maxNameLen           = 44
	maxPhoneLen          = 25
	maxAddressLineLen    = 44
	maxCityLen           = 40
	maxProvStateDomestic = 2
	maxProvStateIntl     = 20
	maxPostalIntl        = 14
	maxCustomsDescLen    = 45
	maxSkuLen            = 15
)

var (
	canadaPostalCode = regexp.MustCompile(`^[A-Z]\d[A-Z]\d[A-Z]\d$`)
	usPostalCode     = regexp.MustCompile(`^\d{5}(-\d{4})?$`)
)

func validateShipmentSnapshot(snapshot RateSnapshot, destCountry string) error {
	destCountry = strings.ToUpper(strings.TrimSpace(destCountry))
	if destCountry == "" {
		destCountry = strings.ToUpper(strings.TrimSpace(defaultValue(snapshot.Customer.CountryCode, snapshot.Customer.Country)))
	}

	// Canada Post requirement: origin postal code (A9A9A9) is required.
	originPostal := strings.TrimSpace(defaultValue(snapshot.Origin.PostalCode, snapshot.Shipper.Zip))
	if originPostal == "" {
		return errors.New("origin postal code is required")
	}
	if err := validatePostalCode("CA", originPostal); err != nil {
		return fmt.Errorf("invalid origin postal code: %w", err)
	}

	shipperAddress := strings.TrimSpace(defaultValue(snapshot.Shipper.Street1, snapshot.Origin.AddressLine))
	if shipperAddress == "" {
		return errors.New("shipper address-line-1 is required")
	}
	if runeLen(shipperAddress) > maxAddressLineLen {
		return errors.New("shipper address-line-1 exceeds 44 characters")
	}

	shipperCity := strings.TrimSpace(defaultValue(snapshot.Shipper.City, snapshot.Origin.City))
	if shipperCity == "" {
		return errors.New("shipper city is required")
	}
	if runeLen(shipperCity) > maxCityLen {
		return errors.New("shipper city exceeds 40 characters")
	}

	shipperProv := strings.TrimSpace(defaultValue(defaultValue(snapshot.Shipper.ProvinceCode, snapshot.Shipper.Province), snapshot.Origin.Province))
	if shipperProv == "" {
		return errors.New("shipper prov-state is required")
	}
	if runeLen(shipperProv) != maxProvStateDomestic {
		return errors.New("shipper prov-state must be 2 characters")
	}

	// Canada Post requirement: sender contact-phone is required (max 25 chars).
	if strings.TrimSpace(snapshot.Shipper.Phone) == "" {
		return errors.New("shipper phone is required")
	}
	if err := validateCanadaPostPhone(snapshot.Shipper.Phone); err != nil {
		return fmt.Errorf("invalid shipper phone number: %w", err)
	}

	shipperName := strings.TrimSpace(snapshot.Shipper.FullName)
	if shipperName == "" {
		shipperName = strings.TrimSpace(strings.TrimSpace(snapshot.Shipper.FirstName + " " + snapshot.Shipper.LastName))
	}
	shipperCompany := strings.TrimSpace(snapshot.Shipper.Company)
	if shipperCompany == "" {
		shipperCompany = shipperName
	}
	if shipperName != "" && runeLen(shipperName) > maxNameLen {
		return errors.New("shipper name exceeds 44 characters")
	}
	if shipperCompany != "" && runeLen(shipperCompany) > maxCompanyLen {
		return errors.New("shipper company exceeds 44 characters")
	}

	destAddress := strings.TrimSpace(defaultValue(snapshot.Customer.Street1, snapshot.Destination.AddressLine))
	if destAddress == "" {
		return errors.New("destination address-line-1 is required")
	}
	if runeLen(destAddress) > maxAddressLineLen {
		return errors.New("destination address-line-1 exceeds 44 characters")
	}

	destName := strings.TrimSpace(snapshot.Customer.FullName)
	if destName == "" {
		destName = strings.TrimSpace(strings.TrimSpace(snapshot.Customer.FirstName + " " + snapshot.Customer.LastName))
	}
	destCompany := strings.TrimSpace(snapshot.Customer.Company)
	if destName == "" && destCompany == "" {
		return errors.New("destination name or company is required")
	}
	if destName != "" && runeLen(destName) > maxNameLen {
		return errors.New("destination name exceeds 44 characters")
	}
	if destCompany != "" && runeLen(destCompany) > maxCompanyLen {
		return errors.New("destination company exceeds 44 characters")
	}

	// Canada Post requirement: destination country-code must be 2 characters.
	if destCountry == "" {
		return errors.New("destination country-code is required")
	}
	if runeLen(destCountry) != 2 {
		return errors.New("destination country-code must be 2 characters")
	}

	destCity := strings.TrimSpace(defaultValue(snapshot.Customer.City, snapshot.Destination.City))
	destProv := strings.TrimSpace(defaultValue(defaultValue(snapshot.Customer.ProvinceCode, snapshot.Customer.Province), snapshot.Destination.Province))
	destPostal := strings.TrimSpace(defaultValue(snapshot.Customer.Zip, snapshot.Destination.PostalCode))

	switch destCountry {
	case "CA", "US":
		// Canada Post requirement: destination city/prov/postal required for CA/US.
		if destCity == "" {
			return errors.New("destination city is required")
		}
		if runeLen(destCity) > maxCityLen {
			return errors.New("destination city exceeds 40 characters")
		}
		if destProv == "" {
			return errors.New("destination prov-state is required")
		}
		if runeLen(destProv) != maxProvStateDomestic {
			return errors.New("destination prov-state must be 2 characters")
		}
		if destPostal == "" {
			return errors.New("destination postal/zip code is required")
		}
		if err := validatePostalCode(destCountry, destPostal); err != nil {
			return fmt.Errorf("invalid destination postal/zip code: %w", err)
		}
	default:
		// Canada Post requirement: international city/prov/postal optional; postal max 14 chars if provided.
		if destCity != "" && runeLen(destCity) > maxCityLen {
			return errors.New("destination city exceeds 40 characters")
		}
		if destProv != "" && runeLen(destProv) > maxProvStateIntl {
			return errors.New("destination prov-state exceeds 20 characters")
		}
		if destPostal != "" {
			if err := validatePostalCode(destCountry, destPostal); err != nil {
				return fmt.Errorf("invalid destination postal/zip code: %w", err)
			}
		}
	}

	return nil
}

// Canada Post requirement: phone max 25 chars; allowed chars 0-9 + . - ( ) space x p; plus only first char.
func validateCanadaPostPhone(phone string) error {
	trimmed := strings.TrimSpace(phone)
	if trimmed == "" {
		return nil
	}
	if runeLen(trimmed) > maxPhoneLen {
		return errors.New("phone exceeds 25 characters")
	}
	for i, r := range trimmed {
		switch {
		case r >= '0' && r <= '9':
			continue
		case r == '+':
			if i == 0 {
				continue
			}
			return errors.New("plus sign must be first character")
		case r == '.' || r == '-' || r == '(' || r == ')' || r == ' ' || r == 'x' || r == 'p' || r == 'X' || r == 'P':
			continue
		default:
			return errors.New("phone contains invalid characters")
		}
	}
	if trimmed == "0000000000" {
		log.Printf("⚠️  Phone appears to be placeholder value")
	}
	return nil
}

func validatePostalCode(country, postal string) error {
	postal = strings.TrimSpace(postal)
	if postal == "" {
		return nil
	}
	upperCountry := strings.ToUpper(strings.TrimSpace(country))
	switch upperCountry {
	case "CA":
		normalized := strings.ReplaceAll(strings.ToUpper(postal), " ", "")
		if !canadaPostalCode.MatchString(normalized) {
			return errors.New("invalid Canadian postal code")
		}
	case "US":
		if !usPostalCode.MatchString(postal) {
			return errors.New("invalid US zip code")
		}
	default:
		if runeLen(postal) > maxPostalIntl {
			return errors.New("postal code exceeds 14 characters")
		}
	}
	return nil
}

func validateCustoms(snapshot RateSnapshot) error {
	if snapshot.CustomsInfo == nil {
		return nil
	}
	if len(snapshot.CustomsInfo.CustomItems) == 0 {
		return errors.New("customs sku-list must include at least one item")
	}
	// Canada Post requirement: total customs weight must be <= parcel weight.
	totalWeight := 0.0
	for _, item := range snapshot.CustomsInfo.CustomItems {
		if item.Quantity > 9999 {
			return errors.New("customs number-of-units exceeds 4 digits")
		}
		description := strings.TrimSpace(defaultValue(item.Description, item.Code))
		if description == "" {
			return errors.New("customs description is required")
		}
		if runeLen(description) > maxCustomsDescLen {
			return errors.New("customs description exceeds 45 characters")
		}
		if item.Code != "" && runeLen(item.Code) > maxSkuLen {
			return errors.New("customs sku exceeds 15 characters")
		}
		if strings.TrimSpace(item.OriginCountry) == "" {
			return errors.New("customs country-of-origin is required")
		}
		if item.Quantity <= 0 {
			continue
		}
		totalWeight += item.Weight * float64(item.Quantity)
	}
	if totalWeight > snapshot.Parcel.Weight {
		return errors.New("customs weight exceeds parcel weight")
	}
	return nil
}

func runeLen(value string) int {
	return len([]rune(strings.TrimSpace(value)))
}

func normalizeDecimal(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var builder strings.Builder
	dotSeen := false
	for _, r := range value {
		if r >= '0' && r <= '9' {
			builder.WriteRune(r)
			continue
		}
		if r == '.' && !dotSeen {
			builder.WriteRune(r)
			dotSeen = true
		}
	}
	clean := strings.TrimSpace(builder.String())
	if clean == "" || clean == "." {
		return ""
	}
	return clean
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
