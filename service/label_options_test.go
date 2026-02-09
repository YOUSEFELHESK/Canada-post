package service

import (
	"testing"

	shippingpluginpb "bitbucket.org/lexmodo/proto/shipping_plugin"
)

func TestBuildCanadaPostOptions_Mapping(t *testing.T) {
	s := &Server{}
	customInfo := []*shippingpluginpb.ShippingDynamicData{
		{FieldName: fieldCODEnabled, FieldValue: "true"},
		{FieldName: fieldCODAmount, FieldValue: "50"},
		{FieldName: fieldCODIncludesShipping, FieldValue: "true"},
		{FieldName: fieldAgeVerification, FieldValue: "PA18"},
		{FieldName: fieldDeliveryMethod, FieldValue: "LAD"},
		{FieldName: fieldNonDeliveryHandling, FieldValue: "RASE"},
		{FieldName: fieldD2POEnabled, FieldValue: "true"},
		{FieldName: fieldD2POOfficeID, FieldValue: "12345"},
	}

	opts := s.buildCanadaPostOptions(customInfo, 1.0, 0)
	byCode := map[string]ShipmentOption{}
	for _, opt := range opts {
		byCode[opt.Code] = opt
	}

	if cod, ok := byCode["COD"]; !ok || cod.OptionAmount != 50 || cod.OptionQualifier1 != "true" {
		t.Fatalf("expected COD with amount 50 and qualifier true, got %+v", cod)
	}
	if _, ok := byCode["PA18"]; !ok {
		t.Fatalf("expected PA18 option")
	}
	if _, ok := byCode["LAD"]; !ok {
		t.Fatalf("expected LAD option")
	}
	if d2po, ok := byCode["D2PO"]; !ok || d2po.OptionQualifier2 != "12345" {
		t.Fatalf("expected D2PO with office id, got %+v", d2po)
	}
	if _, ok := byCode["RASE"]; !ok {
		t.Fatalf("expected RASE option")
	}
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
