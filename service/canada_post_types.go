package service

import "encoding/xml"

type RateRequest struct {
	XMLName               xml.Name `xml:"mailing-scenario"`
	XMLNS                 string   `xml:"xmlns,attr"`
	CustomerNumber        string   `xml:"customer-number,omitempty"`
	ParcelCharacteristics struct {
		Weight     float64 `xml:"weight"`
		Dimensions struct {
			Length float64 `xml:"length"`
			Width  float64 `xml:"width"`
			Height float64 `xml:"height"`
		} `xml:"dimensions,omitempty"`
	} `xml:"parcel-characteristics"`
	OriginPostalCode string `xml:"origin-postal-code"`
	Destination      struct {
		Domestic struct {
			PostalCode string `xml:"postal-code"`
		} `xml:"domestic,omitempty"`
		UnitedStates struct {
			ZipCode string `xml:"zip-code"`
		} `xml:"united-states,omitempty"`
		International struct {
			CountryCode string `xml:"country-code"`
		} `xml:"international,omitempty"`
	} `xml:"destination"`
}

type RateResponse struct {
	XMLName     xml.Name     `xml:"price-quotes"`
	PriceQuotes []PriceQuote `xml:"price-quote"`
}

type PriceQuote struct {
	ServiceCode  string `xml:"service-code"`
	ServiceName  string `xml:"service-name"`
	PriceDetails struct {
		Base  float64 `xml:"base"`
		Taxes struct {
			GST float64 `xml:"gst"`
			PST float64 `xml:"pst"`
			HST float64 `xml:"hst"`
		} `xml:"taxes"`
		Due float64 `xml:"due"`
	} `xml:"price-details"`
	ServiceStandard struct {
		AMDelivery           bool   `xml:"am-delivery"`
		GuaranteedDelivery   bool   `xml:"guaranteed-delivery"`
		ExpectedTransitTime  int    `xml:"expected-transit-time"`
		ExpectedDeliveryDate string `xml:"expected-delivery-date"`
	} `xml:"service-standard"`
}

type ShipmentRequest struct {
	XMLName                xml.Name `xml:"non-contract-shipment"`
	XMLNS                  string   `xml:"xmlns,attr"`
	RequestedShippingPoint string   `xml:"requested-shipping-point,omitempty"`

	DeliverySpec struct {
		ServiceCode string `xml:"service-code"`

		Sender struct {
			Name         string `xml:"name,omitempty"`
			Company      string `xml:"company"`
			ContactPhone string `xml:"contact-phone"`
			AddressDetails struct {
				AddressLine1 string `xml:"address-line-1"`
				AddressLine2 string `xml:"address-line-2,omitempty"`
				City         string `xml:"city"`
				ProvState    string `xml:"prov-state"`
				PostalCode   string `xml:"postal-zip-code"`
			} `xml:"address-details"`
		} `xml:"sender"`

		Destination struct {
			Name    string `xml:"name"`
			Company string `xml:"company,omitempty"`
			AddressDetails struct {
				AddressLine1 string `xml:"address-line-1"`
				AddressLine2 string `xml:"address-line-2,omitempty"`
				City         string `xml:"city"`
				ProvState    string `xml:"prov-state"`
				CountryCode  string `xml:"country-code"`
				PostalCode   string `xml:"postal-zip-code"`
			} `xml:"address-details"`
		} `xml:"destination"`

		ParcelCharacteristics struct {
			Weight     float64 `xml:"weight"`
			Dimensions struct {
				Length float64 `xml:"length"`
				Width  float64 `xml:"width"`
				Height float64 `xml:"height"`
			} `xml:"dimensions"`
		} `xml:"parcel-characteristics"`

		Preferences struct {
			ShowPackingInstructions bool `xml:"show-packing-instructions"`
		} `xml:"preferences"`
	} `xml:"delivery-spec"`
}

type ShipmentResponse struct {
	XMLName     xml.Name `xml:"non-contract-shipment-info"`
	ShipmentID  string   `xml:"shipment-id"`
	TrackingPIN string   `xml:"tracking-pin"`
	Links       struct {
		Link []Link `xml:"link"`
	} `xml:"links"`
}

type Link struct {
	Rel       string `xml:"rel,attr"`
	Href      string `xml:"href,attr"`
	MediaType string `xml:"media-type,attr"`
	Index     string `xml:"index,attr,omitempty"`
}
