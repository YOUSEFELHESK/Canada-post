package service

import (
	"context"
	"testing"

	shippingpluginpb "bitbucket.org/lexmodo/proto/shipping_plugin"
)

func TestBuildCreateLabelOptions_Mapping(t *testing.T) {
	s := &Server{}
	customInfo := []*shippingpluginpb.ShippingDynamicData{
		{FieldName: fieldCODAmount, FieldValue: "50"},
		{FieldName: fieldCODIncludesShipping, FieldValue: ""},
		{FieldName: fieldAgeVerification, FieldValue: "PA18"},
		{FieldName: fieldDeliveryMethod, FieldValue: "Standard Delivery"},
		{FieldName: fieldD2POOfficeID, FieldValue: "12345"},
		{FieldName: fieldD2PONotificationEmail, FieldValue: "test@example.com"},
	}

	values := buildOptionsMap(customInfo)
	opts, _, err := s.buildCreateLabelOptions(values, 1.0, 0, "CA")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	byCode := map[string]ShipmentOption{}
	for _, opt := range opts {
		byCode[opt.Code] = opt
	}

	if cod, ok := byCode["COD"]; !ok || cod.OptionAmount != 50 || cod.OptionQualifier1 != "false" {
		t.Fatalf("expected COD with amount 50 and qualifier false, got %+v", cod)
	}
	if _, ok := byCode["PA18"]; !ok {
		t.Fatalf("expected PA18 option")
	}
	if d2po, ok := byCode["D2PO"]; !ok || d2po.OptionQualifier2 != "12345" {
		t.Fatalf("expected D2PO with office id, got %+v", d2po)
	}
}

func TestBuildCreateLabelOptions_NonDelivery(t *testing.T) {
	s := &Server{}
	customInfo := []*shippingpluginpb.ShippingDynamicData{
		{FieldName: fieldNonDeliveryHandling, FieldValue: "Return to Sender"},
	}
	values := buildOptionsMap(customInfo)
	opts, _, err := s.buildCreateLabelOptions(values, 1.0, 0, "US")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	byCode := map[string]ShipmentOption{}
	for _, opt := range opts {
		byCode[opt.Code] = opt
	}
	if _, ok := byCode["RTS"]; !ok {
		t.Fatalf("expected RTS option")
	}
}

func TestBuildGetRatesOptions_UsesSelectedSignatureOnly(t *testing.T) {
	values := map[string]string{
		fieldAgeVerification: "Proof of Age 19+",
		fieldCOVAmount:       "10",
	}
	opts, err := buildGetRatesOptions(values, 2.0, "NO_SIGNATURE")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	byCode := map[string]RateOption{}
	for _, opt := range opts {
		byCode[opt.Code] = opt
	}
	if _, ok := byCode["SO"]; ok {
		t.Fatalf("did not expect SO when signature is not selected")
	}
	if cov, ok := byCode["COV"]; !ok || cov.OptionAmount != 20 {
		t.Fatalf("expected COV amount 20, got %+v", cov)
	}
}

func TestBuildGetRatesOptions_AddsSOWhenSignatureSelected(t *testing.T) {
	values := map[string]string{
		fieldCOVAmount: "10",
	}
	opts, err := buildGetRatesOptions(values, 2.0, "ADULT_SIGNATURE")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	byCode := map[string]RateOption{}
	for _, opt := range opts {
		byCode[opt.Code] = opt
	}
	if _, ok := byCode["SO"]; !ok {
		t.Fatalf("expected SO when signature is selected")
	}
}

func TestBuildLabelOptionsCredentials_NoEnableFields(t *testing.T) {
	s := &Server{}
	fields := s.buildLabelOptionsCredentials(context.Background())
	byName := map[string]bool{}
	for _, field := range fields {
		byName[field.GetFieldName()] = true
	}
	if byName[fieldCODEnabled] {
		t.Fatalf("did not expect %s to be exposed", fieldCODEnabled)
	}
	if byName[fieldD2POEnabled] {
		t.Fatalf("did not expect %s to be exposed", fieldD2POEnabled)
	}
}

func TestBuildLabelOptionsCredentials_RadioHasNoneOption(t *testing.T) {
	s := &Server{}
	fields := s.buildLabelOptionsCredentials(context.Background())
	fieldByName := map[string]*shippingpluginpb.ShippingDynamicData{}
	for _, field := range fields {
		fieldByName[field.GetFieldName()] = field
	}

	assertContains := func(values []string, expected string) {
		for _, value := range values {
			if value == expected {
				return
			}
		}
		t.Fatalf("expected %q in value set: %v", expected, values)
	}

	assertContains(fieldByName[fieldDeliveryMethod].GetFieldValueSet(), labelNoDeliveryMethod)
	assertContains(fieldByName[fieldD2POOfficeSelection].GetFieldValueSet(), labelNoD2POSelection)
	assertContains(fieldByName[fieldNonDeliveryHandling].GetFieldValueSet(), labelNoNonDeliveryHandling)
}

func TestValidateOptions_CODRequiresHFPOrD2PO(t *testing.T) {
	s := &Server{}
	options := []ShipmentOption{
		{Code: "COD", OptionAmount: 10, OptionQualifier1: "false"},
	}
	if err := s.validateOptions(options, "DOM.EP", "CA"); err == nil {
		t.Fatalf("expected error when COD selected without HFP or D2PO")
	}
}

func TestValidateOptions_D2POOfficeIDRequired(t *testing.T) {
	s := &Server{}
	options := []ShipmentOption{
		{Code: "D2PO"},
	}
	if err := s.validateOptions(options, "DOM.EP", "CA"); err == nil {
		t.Fatalf("expected error when D2PO office id missing")
	}
}

func TestValidateCustomInfoValues_InvalidEnum(t *testing.T) {
	s := &Server{}
	customInfo := []*shippingpluginpb.ShippingDynamicData{
		{FieldName: fieldAgeVerification, FieldValue: "BAD"},
	}
	if err := s.validateCustomInfoValues(customInfo); err == nil {
		t.Fatalf("expected error for invalid enum value")
	}
}

func TestValidateCustomInfoForDestination_NonDeliveryInCanada(t *testing.T) {
	values := map[string]string{
		fieldNonDeliveryHandling: "Return at Sender's Expense",
	}
	err := validateCustomInfoForDestination(values, "CA")
	if err == nil {
		t.Fatalf("expected error for non-delivery handling with destination CA")
	}
	if got := err.Error(); got != "non_delivery_handling=Return at Sender's Expense is not allowed for destination CA. choose non-delivery handling only for USA/International shipments" {
		t.Fatalf("unexpected error message: %s", got)
	}
}

func TestValidateSignatureRequirement_RequiresSignatureForAge(t *testing.T) {
	values := map[string]string{
		fieldAgeVerification: "Proof of Age 18+",
	}
	err := validateSignatureRequirement(values, "NO_SIGNATURE")
	if err == nil {
		t.Fatalf("expected signature requirement error")
	}
	if got := err.Error(); got != "age verification requires signature option. please enable signature to continue" {
		t.Fatalf("unexpected error message: %s", got)
	}
}

func TestValidateSignatureRequirement_RejectsLeaveAtDoorWithSignature(t *testing.T) {
	values := map[string]string{
		fieldDeliveryMethod: "Leave at Door",
	}
	err := validateSignatureRequirement(values, "ADULT_SIGNATURE")
	if err == nil {
		t.Fatalf("expected conflict for Leave at Door with signature")
	}
	if got := err.Error(); got != "Leave at Door cannot be combined with signature option. please choose standard delivery or another delivery method" {
		t.Fatalf("unexpected error message: %s", got)
	}
}

func TestValidateSignatureRequirement_AllowsAgeWhenSignatureEnabled(t *testing.T) {
	values := map[string]string{
		fieldAgeVerification: "Proof of Age 19+",
	}
	if err := validateSignatureRequirement(values, "ADULT_SIGNATURE"); err != nil {
		t.Fatalf("expected no error when signature enabled, got: %v", err)
	}
}

func TestValidateCanadaPostOptionRules_D2PORequiresEmail(t *testing.T) {
	values := map[string]string{
		fieldD2POOfficeSelection: "EATON CENTRE PO",
	}
	err := validateCanadaPostOptionRules(values, "NO_SIGNATURE", "+12015550123", "CA", 1)
	if err == nil {
		t.Fatalf("expected D2PO email validation error")
	}
	if got := err.Error(); got != "email is required for post office delivery notifications" {
		t.Fatalf("unexpected error message: %s", got)
	}
}

func TestIsD2POEnabled_NoSelectionLabelIsDisabled(t *testing.T) {
	values := map[string]string{
		fieldD2POOfficeSelection: labelNoD2POSelection,
	}
	if isD2POEnabled(values) {
		t.Fatalf("expected D2PO to be disabled when selecting %q", labelNoD2POSelection)
	}
}

func TestBuildCreateLabelOptions_NoD2POSelectionDoesNotSendD2PO(t *testing.T) {
	s := &Server{}
	values := map[string]string{
		fieldD2POOfficeSelection:   labelNoD2POSelection,
		fieldD2PONotificationEmail: "yousef@gmail.com",
	}
	options, notification, err := s.buildCreateLabelOptions(values, 1.0, 1, "CA")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, option := range options {
		if option.Code == "D2PO" {
			t.Fatalf("did not expect D2PO option when selecting %q", labelNoD2POSelection)
		}
	}
	if notification != nil {
		t.Fatalf("did not expect notification when D2PO is disabled")
	}
}

func TestValidateCanadaPostOptionRules_CODMax1000CAD(t *testing.T) {
	values := map[string]string{
		fieldCODAmount:      "2812",
		fieldDeliveryMethod: "Hold for Pickup (Pay at Post Office)",
	}
	err := validateCanadaPostOptionRules(values, "NO_SIGNATURE", "+12015550123", "CA", 4)
	if err == nil {
		t.Fatalf("expected COD max validation error")
	}
	if got := err.Error(); got != "COD amount cannot exceed $1,000 CAD" {
		t.Fatalf("unexpected error message: %s", got)
	}
}

func TestValidateCanadaPostOptionRules_NonDeliveryNotAllowedInCA(t *testing.T) {
	values := map[string]string{
		fieldNonDeliveryHandling: "Return to Sender",
	}
	err := validateCanadaPostOptionRules(values, "NO_SIGNATURE", "+12015550123", "CA", 1)
	if err == nil {
		t.Fatalf("expected non-delivery geography validation error")
	}
	if got := err.Error(); got != "non-delivery handling option 'Return to Sender' is not available for Canadian destinations. these options are only available for USA and international shipments" {
		t.Fatalf("unexpected error message: %s", got)
	}
}

func TestMergeOptionsMaps_UsesStoredWhenIncomingMissing(t *testing.T) {
	stored := map[string]string{
		fieldCODAmount:      "2812",
		fieldDeliveryMethod: "Hold for Pickup (Pay at Post Office)",
	}
	merged := mergeOptionsMaps(stored, map[string]string{})
	if got := merged[fieldCODAmount]; got != "2812" {
		t.Fatalf("expected stored COD amount, got %q", got)
	}
	if got := merged[fieldDeliveryMethod]; got != "Hold for Pickup (Pay at Post Office)" {
		t.Fatalf("expected stored delivery method, got %q", got)
	}
}

func TestMergeOptionsMaps_IncomingOverridesStored(t *testing.T) {
	stored := map[string]string{
		fieldCODAmount: "2812",
	}
	incoming := map[string]string{
		fieldCODAmount: "",
	}
	merged := mergeOptionsMaps(stored, incoming)
	if got := merged[fieldCODAmount]; got != "" {
		t.Fatalf("expected incoming value to override stored value, got %q", got)
	}
}
