package service

import (
	"fmt"
	"strings"

	shippingpluginpb "bitbucket.org/lexmodo/proto/shipping_plugin"
)

func buildOptionsMap(customInfo []*shippingpluginpb.ShippingDynamicData) map[string]string {
	return customInfoToMap(customInfo)
}

func cloneOptionsMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = strings.TrimSpace(value)
	}
	return cloned
}

func mergeOptionsMaps(stored map[string]string, incoming map[string]string) map[string]string {
	if len(stored) == 0 && len(incoming) == 0 {
		return map[string]string{}
	}
	merged := cloneOptionsMap(stored)
	if merged == nil {
		merged = make(map[string]string, len(incoming))
	}
	for key, value := range incoming {
		merged[key] = strings.TrimSpace(value)
	}
	return merged
}

func buildGetRatesOptions(values map[string]string, rateToCad float64, signatureValue string) ([]RateOption, error) {
	if rateToCad <= 0 {
		rateToCad = 1
	}

	options := make([]RateOption, 0, 2)
	if parseBool(values[fieldSOEnabled]) || signatureEnabled(signatureValue) {
		options = append(options, RateOption{Code: "SO"})
	}
	covAmount, covAmountOK := parseAmount(values[fieldCOVAmount])
	if isCOVEnabled(values, covAmountOK) {
		if !covAmountOK {
			return nil, fmt.Errorf("COV amount is required and must be a positive number")
		}
		options = append(options, RateOption{Code: "COV", OptionAmount: covAmount * rateToCad})
	}

	return dedupeRateOptions(options), nil
}

func (s *Server) buildCreateLabelOptions(values map[string]string, rateToCad float64, clientID int64, destCountry string) ([]ShipmentOption, *ShipmentNotification, error) {
	if rateToCad <= 0 {
		rateToCad = 1
	}

	options := make([]ShipmentOption, 0, 10)

	if parseBool(values[fieldSOEnabled]) {
		options = append(options, ShipmentOption{Code: "SO"})
	}

	age := resolveMappedValue(values[fieldAgeVerification], ageVerificationMap)

	covAmount, covAmountOK := parseAmount(values[fieldCOVAmount])
	if isCOVEnabled(values, covAmountOK) {
		if !covAmountOK {
			return nil, nil, fmt.Errorf("COV amount is required and must be a positive number")
		}
		options = append(options, ShipmentOption{Code: "COV", OptionAmount: covAmount * rateToCad})
	}

	codAmount, codAmountOK := parseAmount(values[fieldCODAmount])
	if isCODEnabled(values, codAmountOK) {
		if !codAmountOK {
			return nil, nil, fmt.Errorf("COD amount is required and must be a positive number")
		}
		options = append(options, ShipmentOption{
			Code:             "COD",
			OptionAmount:     codAmount * rateToCad,
			OptionQualifier1: normalizeBoolString(values[fieldCODIncludesShipping]),
		})
	}

	delivery := resolveMappedValue(values[fieldDeliveryMethod], deliveryMethodMap)
	d2poEnabled := isD2POEnabled(values)
	if d2poEnabled && (delivery == "HFP" || delivery == "DNS" || delivery == "LAD") {
		return nil, nil, fmt.Errorf("delivery method is mutually exclusive with Deliver to Post Office")
	}

	var notification *ShipmentNotification
	if d2poEnabled {
		officeID := strings.TrimSpace(values[fieldD2POOfficeID])
		if officeID == "" {
			selection := strings.TrimSpace(values[fieldD2POOfficeSelection])
			if isNoD2POSelection(selection) {
				selection = ""
			}
			if selection != "" {
				resolved, err := s.resolveOfficeIDFromSelection(clientID, selection)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to resolve post office selection: %w", err)
				}
				officeID = resolved
			}
		}
		if strings.TrimSpace(officeID) == "" {
			return nil, nil, fmt.Errorf("D2PO office selection is required when Deliver to Post Office is enabled")
		}
		options = append(options, ShipmentOption{
			Code:             "D2PO",
			OptionQualifier2: officeID,
		})

		email := strings.TrimSpace(values[fieldD2PONotificationEmail])
		if email == "" {
			return nil, nil, fmt.Errorf("D2PO notification email is required")
		}
		notification = &ShipmentNotification{
			Email:       email,
			OnShipment:  true,
			OnException: true,
			OnDelivery:  true,
		}
	} else {
		switch delivery {
		case "HFP", "DNS", "LAD":
			options = append(options, ShipmentOption{Code: delivery})
		}
	}

	if age == "PA18" || age == "PA19" {
		options = append(options, ShipmentOption{Code: age})
	}

	nonDelivery := resolveMappedValue(values[fieldNonDeliveryHandling], nonDeliveryMap)
	if nonDelivery == "RASE" || nonDelivery == "RTS" || nonDelivery == "ABAN" {
		options = append(options, ShipmentOption{Code: nonDelivery})
	}

	return dedupeShipmentOptions(options), notification, nil
}

func dedupeRateOptions(options []RateOption) []RateOption {
	if len(options) == 0 {
		return options
	}
	seen := make(map[string]struct{}, len(options))
	result := make([]RateOption, 0, len(options))
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

func isCODEnabled(values map[string]string, codAmountOK bool) bool {
	return parseBool(values[fieldCODEnabled]) || codAmountOK
}

func isCOVEnabled(values map[string]string, covAmountOK bool) bool {
	return parseBool(values[fieldCOVEnabled]) || covAmountOK
}

func isD2POEnabled(values map[string]string) bool {
	selection := strings.TrimSpace(values[fieldD2POOfficeSelection])
	return parseBool(values[fieldD2POEnabled]) ||
		strings.TrimSpace(values[fieldD2POOfficeID]) != "" ||
		(selection != "" && !isNoD2POSelection(selection))
}

func validateCanadaPostOptionRules(values map[string]string, signatureValue string, recipientPhone string, destinationCountry string, rateToCad float64) error {
	if err := validateSignatureRequirement(values, signatureValue); err != nil {
		return err
	}
	if err := validateDeliveryMethodExclusivity(values); err != nil {
		return err
	}
	if err := validateCODRequirements(values, destinationCountry, rateToCad); err != nil {
		return err
	}
	if err := validateD2PORequirements(values, recipientPhone, destinationCountry); err != nil {
		return err
	}
	if err := validateNonDeliveryHandling(values, destinationCountry); err != nil {
		return err
	}
	return nil
}

func validateSignatureRequirement(values map[string]string, signatureValue string) error {
	delivery := resolveMappedValue(values[fieldDeliveryMethod], deliveryMethodMap)
	age := resolveMappedValue(values[fieldAgeVerification], ageVerificationMap)
	ageRequiresSignature := age == "PA18" || age == "PA19"
	hasSignature := signatureEnabled(signatureValue) || parseBool(values[fieldSOEnabled])

	if ageRequiresSignature && !hasSignature {
		return fmt.Errorf("age verification requires signature option. please enable signature to continue")
	}
	if delivery == "LAD" && hasSignature {
		return fmt.Errorf("Leave at Door cannot be combined with signature option. please choose standard delivery or another delivery method")
	}
	if ageRequiresSignature {
		if delivery == "LAD" {
			return fmt.Errorf("Leave at Door cannot be combined with age verification. please choose standard delivery")
		}
	}
	return nil
}

func validateDeliveryMethodExclusivity(values map[string]string) error {
	selected := make([]string, 0, 2)
	switch resolveMappedValue(values[fieldDeliveryMethod], deliveryMethodMap) {
	case "HFP":
		selected = append(selected, "Hold for Pickup (Pay at Post Office)")
	case "DNS":
		selected = append(selected, "Do Not Safe Drop")
	case "LAD":
		selected = append(selected, "Leave at Door")
	}
	if isD2POEnabled(values) {
		selected = append(selected, "Deliver to Post Office")
	}
	if len(selected) <= 1 {
		return nil
	}
	return fmt.Errorf("only one delivery method can be selected, but found: %s", strings.Join(selected, ", "))
}

func validateCODRequirements(values map[string]string, destinationCountry string, rateToCad float64) error {
	codRaw := strings.TrimSpace(values[fieldCODAmount])
	codAmount, codAmountOK := parseAmount(codRaw)
	codEnabled := parseBool(values[fieldCODEnabled]) || codAmountOK
	if !codEnabled {
		return nil
	}
	if !codAmountOK {
		return fmt.Errorf("COD amount is required and must be a positive number")
	}

	destCountry := strings.ToUpper(strings.TrimSpace(destinationCountry))
	if destCountry != "" && destCountry != "CA" {
		return fmt.Errorf("COD is only available for Canadian destinations")
	}

	if rateToCad <= 0 {
		rateToCad = 1
	}
	if codAmount*rateToCad > 1000 {
		return fmt.Errorf("COD amount cannot exceed $1,000 CAD")
	}

	delivery := resolveMappedValue(values[fieldDeliveryMethod], deliveryMethodMap)
	if delivery == "LAD" {
		return fmt.Errorf("COD cannot be combined with Leave at Door")
	}
	if delivery == "DNS" {
		return fmt.Errorf("COD cannot be combined with Do Not Safe Drop")
	}

	hasPickup := delivery == "HFP" || isD2POEnabled(values)
	if !hasPickup {
		return fmt.Errorf("COD requires Hold for Pickup or Deliver to Post Office to be selected")
	}
	return nil
}

func validateD2PORequirements(values map[string]string, recipientPhone string, destinationCountry string) error {
	if !isD2POEnabled(values) {
		return nil
	}

	destCountry := strings.ToUpper(strings.TrimSpace(destinationCountry))
	if destCountry != "" && destCountry != "CA" {
		return fmt.Errorf("Deliver to Post Office is only available for Canadian destinations")
	}

	officeID := strings.TrimSpace(values[fieldD2POOfficeID])
	officeSelection := strings.TrimSpace(values[fieldD2POOfficeSelection])
	if isNoD2POSelection(officeSelection) {
		officeSelection = ""
	}
	if officeID == "" && officeSelection == "" {
		return fmt.Errorf("post office selection is required when Deliver to Post Office is selected")
	}
	if strings.TrimSpace(values[fieldD2PONotificationEmail]) == "" {
		return fmt.Errorf("email is required for post office delivery notifications")
	}
	if strings.TrimSpace(recipientPhone) == "" {
		return fmt.Errorf("recipient phone number is required when using Deliver to Post Office")
	}

	switch resolveMappedValue(values[fieldDeliveryMethod], deliveryMethodMap) {
	case "HFP":
		return fmt.Errorf("cannot select both Hold for Pickup (Pay at Post Office) and Deliver to Post Office")
	case "LAD":
		return fmt.Errorf("cannot select both Leave at Door and Deliver to Post Office")
	case "DNS":
		return fmt.Errorf("cannot select both Do Not Safe Drop and Deliver to Post Office")
	}
	return nil
}

func validateNonDeliveryHandling(values map[string]string, destinationCountry string) error {
	nonDelivery := resolveMappedValue(values[fieldNonDeliveryHandling], nonDeliveryMap)
	if nonDelivery == "" {
		return nil
	}
	destCountry := strings.ToUpper(strings.TrimSpace(destinationCountry))
	if destCountry != "CA" {
		return nil
	}
	selected := strings.TrimSpace(values[fieldNonDeliveryHandling])
	if selected == "" {
		selected = nonDelivery
	}
	return fmt.Errorf("non-delivery handling option '%s' is not available for Canadian destinations. these options are only available for USA and international shipments", selected)
}

func signatureEnabled(signatureValue string) bool {
	signature := strings.ToUpper(strings.TrimSpace(signatureValue))
	switch signature {
	case "", "NO_SIGNATURE", "SIGNATURE_UNSPECIFIED", "UNSPECIFIED":
		return false
	default:
		return true
	}
}

func isNoD2POSelection(selection string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(selection))
	switch normalized {
	case "", "NONE", strings.ToUpper(labelNoD2POSelection):
		return true
	default:
		return false
	}
}
