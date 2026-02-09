package service

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	shippingpluginpb "bitbucket.org/lexmodo/proto/shipping_plugin"
	"google.golang.org/protobuf/types/known/emptypb"
)

const (
	fieldCODEnabled            = "COD_enabled"
	fieldCODAmount             = "COD_amount"
	fieldCODIncludesShipping   = "COD_includes_shipping"
	fieldAgeVerification       = "age_verification"
	fieldDeliveryMethod        = "delivery_method"
	fieldNonDeliveryHandling   = "non_delivery_handling"
	fieldD2POEnabled           = "D2PO_enabled"
	fieldD2POOfficeID          = "D2PO_office_id"
	fieldD2POOfficeSelection   = "D2PO_office_selection"
	fieldD2PONotificationEmail = "D2PO_notification_email"
)

var (
	ageVerificationLabels = []string{"No Age Verification", "Proof of Age 18+", "Proof of Age 19+"}
	deliveryMethodLabels  = []string{"Standard Delivery", "Hold at Post Office", "Do Not Safe Drop", "Leave at Door"}
	nonDeliveryLabels     = []string{"Return at Sender's Expense", "Return to Sender", "Abandon Shipment"}

	ageVerificationMap = map[string]string{
		"NO AGE VERIFICATION": "NONE",
		"PROOF OF AGE 18+":    "PA18",
		"PROOF OF AGE 19+":    "PA19",
		"NONE":                "NONE",
		"PA18":                "PA18",
		"PA19":                "PA19",
	}
	deliveryMethodMap = map[string]string{
		"STANDARD DELIVERY":   "STANDARD",
		"HOLD AT POST OFFICE": "HFP",
		"DO NOT SAFE DROP":    "DNS",
		"LEAVE AT DOOR":       "LAD",
		"STANDARD":            "STANDARD",
		"HFP":                 "HFP",
		"DNS":                 "DNS",
		"LAD":                 "LAD",
	}
	nonDeliveryMap = map[string]string{
		"RETURN AT SENDER'S EXPENSE": "RASE",
		"RETURN TO SENDER":           "RTS",
		"ABANDON SHIPMENT":           "ABAN",
		"RASE":                       "RASE",
		"RTS":                        "RTS",
		"ABAN":                       "ABAN",
	}
)

// ListLabelShippingOptions returns the full list of Canada Post label options.
func (s *Server) ListLabelShippingOptions(ctx context.Context, _ *emptypb.Empty) (*shippingpluginpb.ResultResponse, error) {
	log.Println("ListLabelShippingOptions request received")
	logIncomingMetadata(ctx)
	officeOptions := []string{}
	if s.PostOffices != nil && s.Store != nil {
		clientID := clientIDFromContextInt(ctx)
		if clientID > 0 {
			defaultPostal := ""
			if settings, err := s.Store.LoadShippingSettings(clientID); err == nil {
				defaultPostal = strings.TrimSpace(settings.DefaultPostalCode)
			}
			var offices []PostOffice
			var err error
			if defaultPostal != "" {
				offices, err = s.PostOffices.GetStoredOfficesByPostalCode(clientID, defaultPostal)
			} else {
				offices, err = s.PostOffices.GetAllStoredOffices(clientID)
			}
			if err != nil {
				log.Printf("failed to load cached post offices: %v", err)
			} else {
				for _, office := range offices {
					officeOptions = append(officeOptions, formatPostOfficeDisplay(office))
				}
			}
		}
	}
	credentials := []*shippingpluginpb.ShippingDynamicData{
		buildField(fieldCODEnabled, "Enable Collect on Delivery", shippingpluginpb.FIELD_TYPE_checkbox, ""),
		buildField(fieldCODAmount, "COD amount (in your currency)", shippingpluginpb.FIELD_TYPE_text, ""),
		buildField(fieldCODIncludesShipping, "COD amount includes shipping cost", shippingpluginpb.FIELD_TYPE_checkbox, ""),
		buildField(fieldDeliveryMethod, "How should the package be delivered?", shippingpluginpb.FIELD_TYPE_radio, "", deliveryMethodLabels...),
		buildField(fieldAgeVerification, "Recipient age verification", shippingpluginpb.FIELD_TYPE_radio, "", ageVerificationLabels...),
		buildField(fieldD2POEnabled, "Deliver to post office instead of address", shippingpluginpb.FIELD_TYPE_checkbox, ""),
		buildField(fieldD2POOfficeSelection, "Select post office for delivery", shippingpluginpb.FIELD_TYPE_radio, "", officeOptions...),
		buildField(fieldD2POOfficeID, "Post office ID", shippingpluginpb.FIELD_TYPE_text, ""),
		buildField(fieldD2PONotificationEmail, "Email for pickup notification", shippingpluginpb.FIELD_TYPE_text, ""),
		buildField(fieldNonDeliveryHandling, "What should happen if delivery fails?", shippingpluginpb.FIELD_TYPE_radio, "", nonDeliveryLabels...),
	}
	resp := &shippingpluginpb.ResultResponse{
		Success: true,
		Failure: false,
		ShippingMethod: &shippingpluginpb.ShippingPluginReqeust{
			ShippingpluginreqeustCredentials: credentials,
		},
	}
	log.Printf("ListLabelShippingOptions response: %+v", resp)
	return resp, nil
}

func buildField(name, label string, fieldType shippingpluginpb.FIELD_TYPE, value string, valueSet ...string) *shippingpluginpb.ShippingDynamicData {
	return &shippingpluginpb.ShippingDynamicData{
		FieldName:     name,
		FieldLabel:    label,
		FieldValue:    value,
		FieldType:     fieldType,
		FieldValueSet: valueSet,
	}
}

func (s *Server) buildCanadaPostOptions(customInfo []*shippingpluginpb.ShippingDynamicData, rateToCad float64, clientID int64) []ShipmentOption {
	values := customInfoToMap(customInfo)
	if rateToCad <= 0 {
		rateToCad = 1
	}

	options := make([]ShipmentOption, 0, 8)
	if parseBool(values[fieldCODEnabled]) {
		amount, _ := parseAmount(values[fieldCODAmount])
		options = append(options, ShipmentOption{
			Code:             "COD",
			OptionAmount:     amount * rateToCad,
			OptionQualifier1: normalizeBoolString(values[fieldCODIncludesShipping]),
		})
	}

	if val := resolveMappedValue(values[fieldAgeVerification], ageVerificationMap); val == "PA18" || val == "PA19" {
		options = append(options, ShipmentOption{Code: val})
	}

	if val := resolveMappedValue(values[fieldDeliveryMethod], deliveryMethodMap); val == "HFP" || val == "DNS" || val == "LAD" {
		options = append(options, ShipmentOption{Code: val})
	}

	if parseBool(values[fieldD2POEnabled]) {
		officeID := strings.TrimSpace(values[fieldD2POOfficeID])
		if officeID == "" {
			if selection := strings.TrimSpace(values[fieldD2POOfficeSelection]); selection != "" {
				if resolved, err := s.resolveOfficeIDFromSelection(clientID, selection); err == nil {
					officeID = resolved
				} else {
					log.Printf("failed to resolve post office selection: %v", err)
				}
			}
		}
		options = append(options, ShipmentOption{
			Code:             "D2PO",
			OptionQualifier2: officeID,
		})
	}

	if val := resolveMappedValue(values[fieldNonDeliveryHandling], nonDeliveryMap); val == "RASE" || val == "RTS" || val == "ABAN" {
		options = append(options, ShipmentOption{Code: val})
	}

	return dedupeShipmentOptions(options)
}

func (s *Server) validateCustomInfoValues(customInfo []*shippingpluginpb.ShippingDynamicData) error {
	values := customInfoToMap(customInfo)

	if err := validateBoolField(values, fieldCODEnabled); err != nil {
		return err
	}
	if err := validateBoolField(values, fieldCODIncludesShipping); err != nil {
		return err
	}
	if err := validateBoolField(values, fieldD2POEnabled); err != nil {
		return err
	}

	if err := validateEnumField(values, fieldAgeVerification, append(ageVerificationLabels, "NONE", "PA18", "PA19")...); err != nil {
		return err
	}
	if err := validateEnumField(values, fieldDeliveryMethod, append(deliveryMethodLabels, "STANDARD", "HFP", "DNS", "LAD")...); err != nil {
		return err
	}
	if err := validateEnumField(values, fieldNonDeliveryHandling, append(nonDeliveryLabels, "RASE", "RTS", "ABAN")...); err != nil {
		return err
	}

	if parseBool(values[fieldCODEnabled]) {
		if _, ok := parseAmount(values[fieldCODAmount]); !ok {
			return fmt.Errorf("COD amount is required and must be a positive number")
		}
		if strings.TrimSpace(values[fieldCODIncludesShipping]) == "" {
			return fmt.Errorf("COD includes_shipping must be provided")
		}
	}
	if parseBool(values[fieldD2POEnabled]) &&
		strings.TrimSpace(values[fieldD2POOfficeID]) == "" &&
		strings.TrimSpace(values[fieldD2POOfficeSelection]) == "" {
		return fmt.Errorf("D2PO office selection is required when Deliver to Post Office is enabled")
	}

	return nil
}

func (s *Server) validateOptions(options []ShipmentOption, _ string, destination string) error {
	if len(options) == 0 {
		return nil
	}

	ageCount := 0
	deliveryCount := 0
	nonDeliveryCount := 0

	hasCOD := false
	hasHFP := false
	hasD2PO := false

	for _, opt := range options {
		code := strings.ToUpper(strings.TrimSpace(opt.Code))
		switch code {
		case "PA18", "PA19":
			ageCount++
		case "HFP":
			deliveryCount++
			hasHFP = true
		case "DNS", "LAD":
			deliveryCount++
		case "RASE", "RTS", "ABAN":
			nonDeliveryCount++
		case "COD":
			hasCOD = true
			if opt.OptionAmount <= 0 {
				return fmt.Errorf("COD amount must be greater than zero")
			}
			if strings.TrimSpace(opt.OptionQualifier1) == "" {
				return fmt.Errorf("COD includes_shipping must be provided")
			}
		case "COV":
			if opt.OptionAmount <= 0 {
				return fmt.Errorf("coverage amount must be greater than zero")
			}
		case "D2PO":
			hasD2PO = true
			if strings.TrimSpace(opt.OptionQualifier2) == "" {
				return fmt.Errorf("D2PO office_id is required")
			}
		}
	}

	if ageCount > 1 {
		return fmt.Errorf("only one age verification option can be selected")
	}
	if deliveryCount > 1 {
		return fmt.Errorf("only one delivery method option can be selected")
	}
	if nonDeliveryCount > 1 {
		return fmt.Errorf("only one non-delivery handling option can be selected")
	}

	if hasCOD && !(hasHFP || hasD2PO) {
		return fmt.Errorf("COD requires Hold for Pickup or Deliver to Post Office to be selected")
	}

	return nil
}

func customInfoToMap(customInfo []*shippingpluginpb.ShippingDynamicData) map[string]string {
	values := make(map[string]string, len(customInfo))
	for _, item := range customInfo {
		if item == nil {
			continue
		}
		name := strings.TrimSpace(item.GetFieldName())
		if name == "" {
			continue
		}
		values[name] = strings.TrimSpace(item.GetFieldValue())
	}
	return values
}

func parseBool(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "true", "1", "yes":
		return true
	default:
		return false
	}
}

func normalizeBoolString(value string) string {
	if parseBool(value) {
		return "true"
	}
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return "false"
}

func parseAmount(value string) (float64, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, false
	}
	if parsed <= 0 {
		return 0, false
	}
	return parsed, true
}

func resolveMappedValue(value string, mapping map[string]string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	if mapped, ok := mapping[value]; ok {
		return mapped
	}
	return value
}

func validateBoolField(values map[string]string, field string) error {
	value, ok := values[field]
	if !ok || strings.TrimSpace(value) == "" {
		return nil
	}
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "true", "false", "1", "0", "yes", "no":
		return nil
	default:
		return fmt.Errorf("invalid boolean value for %s", field)
	}
}

func validateEnumField(values map[string]string, field string, allowed ...string) error {
	value, ok := values[field]
	if !ok || strings.TrimSpace(value) == "" {
		return nil
	}
	value = strings.ToUpper(strings.TrimSpace(value))
	for _, candidate := range allowed {
		if value == strings.ToUpper(strings.TrimSpace(candidate)) {
			return nil
		}
	}
	return fmt.Errorf("invalid value for %s", field)
}

func dedupeShipmentOptions(options []ShipmentOption) []ShipmentOption {
	if len(options) == 0 {
		return options
	}
	seen := make(map[string]struct{}, len(options))
	result := make([]ShipmentOption, 0, len(options))
	for _, opt := range options {
		code := strings.ToUpper(strings.TrimSpace(opt.Code))
		if code == "" {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		result = append(result, opt)
	}
	return result
}

func mergeShipmentOptions(options []ShipmentOption, destCountry string) []ShipmentOption {
	finalOptions := dedupeShipmentOptions(options)
	if requiresCustoms(destCountry) && !hasNonDeliveryOption(finalOptions) {
		finalOptions = append(finalOptions, ShipmentOption{Code: defaultNonDeliveryOption})
	}
	return finalOptions
}

func hasNonDeliveryOption(options []ShipmentOption) bool {
	for _, opt := range options {
		code := strings.ToUpper(strings.TrimSpace(opt.Code))
		switch code {
		case "RASE", "RTS", "ABAN":
			return true
		}
	}
	return false
}

func buildSnapshotOptions(snapshot RateSnapshot) []ShipmentOption {
	options := make([]ShipmentOption, 0, 2)
	signature := strings.ToUpper(strings.TrimSpace(snapshot.Signature))
	if signature != "" && signature != "NO_SIGNATURE" {
		options = append(options, ShipmentOption{Code: "SO"})
	}

	if amount := insuranceAmountCAD(snapshot); amount > 0 {
		options = append(options, ShipmentOption{Code: "COV", OptionAmount: amount})
	}
	return options
}

func insuranceAmountCAD(snapshot RateSnapshot) float64 {
	rateToCad := snapshot.RateToCad
	if rateToCad <= 0 {
		rateToCad = 1
	}
	if snapshot.Insurance.Amount > 0 {
		cadCents := convertCurrencyToCadCents(snapshot.Insurance.Amount, rateToCad)
		return float64(cadCents) / 100.0
	}
	if strings.TrimSpace(snapshot.Insurance.Decimal) != "" {
		if amount, ok := parseAmount(snapshot.Insurance.Decimal); ok {
			return amount * rateToCad
		}
	}
	return 0
}
