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
		Domestic *struct {
			PostalCode string `xml:"postal-code"`
		} `xml:"domestic,omitempty"`

		UnitedStates *struct {
			ZipCode string `xml:"zip-code"`
		} `xml:"united-states,omitempty"`

		International *struct {
			CountryCode string `xml:"country-code"`
		} `xml:"international,omitempty"`
	} `xml:"destination"`
}

type RateResponse struct {
	XMLName     xml.Name     `xml:"http://www.canadapost.ca/ws/ship/rate-v4 price-quotes"`
	PriceQuotes []PriceQuote `xml:"price-quote"`
}

type PriceQuote struct {
	ServiceCode     string          `xml:"service-code"`
	ServiceName     string          `xml:"service-name"`
	ServiceLink     ServiceLink     `xml:"service-link"`
	PriceDetails    PriceDetails    `xml:"price-details"`
	WeightDetails   WeightDetails   `xml:"weight-details"`
	ServiceStandard ServiceStandard `xml:"service-standard"`
}

type ServiceLink struct {
	Rel       string `xml:"rel,attr"`
	Href      string `xml:"href,attr"`
	MediaType string `xml:"media-type,attr"`
}

type PriceDetails struct {
	Base        float64     `xml:"base"`
	Due         float64     `xml:"due"`
	Taxes       Taxes       `xml:"taxes"`
	Options     Options     `xml:"options"`
	Adjustments Adjustments `xml:"adjustments"`
}

type Taxes struct {
	GST Tax `xml:"gst"`
	PST Tax `xml:"pst"`
	HST Tax `xml:"hst"`
}

type Tax struct {
	Value   float64 `xml:",chardata"`
	Percent float64 `xml:"percent,attr"`
}

type Options struct {
	Option []Option `xml:"option"`
}

type Option struct {
	Code      string    `xml:"option-code"`
	Name      string    `xml:"option-name"`
	Price     float64   `xml:"option-price"`
	Qualifier Qualifier `xml:"qualifier"`
}

type Qualifier struct {
	Included bool    `xml:"included"`
	Percent  float64 `xml:"percent,omitempty"`
}

type Adjustments struct {
	Adjustment []Adjustment `xml:"adjustment"`
}

type Adjustment struct {
	Code      string     `xml:"adjustment-code"`
	Name      string     `xml:"adjustment-name"`
	Cost      float64    `xml:"adjustment-cost"`
	Qualifier *Qualifier `xml:"qualifier,omitempty"`
}

type WeightDetails struct {
	// حسب الـ XML الحالي، ممكن يكون فاضي، خليها placeholder
}

type ServiceStandard struct {
	AMDelivery           bool   `xml:"am-delivery"`
	GuaranteedDelivery   bool   `xml:"guaranteed-delivery"`
	ExpectedTransitTime  int    `xml:"expected-transit-time"`
	ExpectedDeliveryDate string `xml:"expected-delivery-date"`
}

type ShipmentRequest struct {
	XMLName                xml.Name `xml:"non-contract-shipment"`
	XMLNS                  string   `xml:"xmlns,attr"`
	RequestedShippingPoint string   `xml:"requested-shipping-point,omitempty"`

	DeliverySpec struct {
		ServiceCode string `xml:"service-code"`

		Sender struct {
			Name           string `xml:"name,omitempty"`
			Company        string `xml:"company"`
			ContactPhone   string `xml:"contact-phone"`
			AddressDetails struct {
				AddressLine1 string `xml:"address-line-1"`
				AddressLine2 string `xml:"address-line-2,omitempty"`
				City         string `xml:"city"`
				ProvState    string `xml:"prov-state"`
				PostalCode   string `xml:"postal-zip-code"`
			} `xml:"address-details"`
		} `xml:"sender"`

		Destination struct {
			Name              string `xml:"name"`
			Company           string `xml:"company,omitempty"`
			ClientVoiceNumber string `xml:"client-voice-number,omitempty"`
			AddressDetails    struct {
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

		Options *ShipmentOptions `xml:"options,omitempty"`
		Customs *ShipmentCustoms `xml:"customs,omitempty"`
	} `xml:"delivery-spec"`
}

type ShipmentOptions struct {
	Option []ShipmentOption `xml:"option"`
}

type ShipmentOption struct {
	Code string `xml:"option-code"`
}

type ShipmentCustoms struct {
	USDeclarationID   string              `xml:"us-declaration-id,omitempty"`
	Currency          string              `xml:"currency,omitempty"`
	ConversionFromCAD string              `xml:"conversion-from-cad,omitempty"`
	ReasonForExport   string              `xml:"reason-for-export,omitempty"`
	SkuList           ShipmentCustomsList `xml:"sku-list"`
}

type ShipmentCustomsList struct {
	Item []ShipmentCustomsItem `xml:"item"`
}

type ShipmentCustomsItem struct {
	CustomsNumberOfUnits int     `xml:"customs-number-of-units,omitempty"`
	CustomsDescription   string  `xml:"customs-description,omitempty"`
	UnitWeight           float64 `xml:"unit-weight,omitempty"`
	CustomsValuePerUnit  float64 `xml:"customs-value-per-unit,omitempty"`
	HSTariffCode         string  `xml:"hs-tariff-code,omitempty"`
	SKU                  string  `xml:"sku,omitempty"`
	CountryOfOrigin      string  `xml:"country-of-origin,omitempty"`
	ProvinceOfOrigin     string  `xml:"province-of-origin,omitempty"`
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

// Request Non-Contract Shipment Refund – REST
type RefundRequest struct {
	XMLName xml.Name `xml:"non-contract-shipment-refund-request"`
	XMLNS   string   `xml:"xmlns,attr"`
	Email   string   `xml:"email"`
}

// Request Non-Contract Shipment Refund – REST
type RefundResponse struct {
	XMLName           xml.Name `xml:"http://www.canadapost.ca/ws/ncshipment-v4 non-contract-shipment-refund-request-info"`
	ServiceTicketDate string   `xml:"service-ticket-date"`
	ServiceTicketID   string   `xml:"service-ticket-id"`
}

type CPMessages struct {
	XMLName  xml.Name    `xml:"messages"`
	Messages []CPMessage `xml:"message"`
}

type CPMessage struct {
	Code        string `xml:"code"`
	Description string `xml:"description"`
}
