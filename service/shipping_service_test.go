package service

import "testing"

func TestValidateCanadaPostPhone_AllowsPlaceholder(t *testing.T) {
	if err := validateCanadaPostPhone("0000000000"); err != nil {
		t.Fatalf("expected placeholder phone to be valid, got error: %v", err)
	}
}

func TestValidateCanadaPostPhone_DisallowsPlusInMiddle(t *testing.T) {
	if err := validateCanadaPostPhone("123+456"); err == nil {
		t.Fatalf("expected error for plus in middle, got nil")
	}
}

func TestValidatePostalCode_CA_AllowsSpace(t *testing.T) {
	if err := validatePostalCode("CA", "K1A 0B1"); err != nil {
		t.Fatalf("expected CA postal with space to be valid, got error: %v", err)
	}
}

func TestValidatePostalCode_InternationalLength(t *testing.T) {
	if err := validatePostalCode("FR", "12345678901234"); err != nil {
		t.Fatalf("expected 14-char international postal to be valid, got error: %v", err)
	}
	if err := validatePostalCode("FR", "123456789012345"); err == nil {
		t.Fatalf("expected 15-char international postal to be invalid")
	}
}

func TestValidateShipmentSnapshot_InternationalPostalOptional(t *testing.T) {
	snapshot := RateSnapshot{
		Shipper: addressSnapshot{
			Street1:      "1 Main St",
			City:         "Toronto",
			ProvinceCode: "ON",
			Zip:          "M5V1E3",
			Phone:        "0000000000",
			FullName:     "Sender Name",
			Company:      "Sender Co",
		},
		Customer: addressSnapshot{
			Street1:     "2 Rue Example",
			CountryCode: "FR",
			FullName:    "Recipient Name",
		},
		Origin: canadaPostOrigin{
			PostalCode:  "M5V1E3",
			AddressLine: "1 Main St",
			City:        "Toronto",
			Province:    "ON",
		},
		Destination: canadaPostDestination{
			Country:     "FR",
			AddressLine: "2 Rue Example",
		},
		Parcel: parcelMetrics{Weight: 1.0},
	}

	if err := validateShipmentSnapshot(snapshot, "FR"); err != nil {
		t.Fatalf("expected international shipment without postal to be valid, got error: %v", err)
	}
}

func TestValidateCustomsWeight(t *testing.T) {
	snapshot := RateSnapshot{
		Parcel: parcelMetrics{Weight: 1.0},
		CustomsInfo: &customsSnapshot{
			CustomItems: []customItemSnapshot{
				{Description: "Item", Quantity: 2, Weight: 0.4, OriginCountry: "US"},
			},
		},
	}

	if err := validateCustoms(snapshot); err != nil {
		t.Fatalf("expected customs weight within parcel weight to be valid, got error: %v", err)
	}

	snapshot.CustomsInfo.CustomItems[0].Weight = 0.6
	if err := validateCustoms(snapshot); err == nil {
		t.Fatalf("expected customs weight exceeding parcel weight to be invalid")
	}
}
