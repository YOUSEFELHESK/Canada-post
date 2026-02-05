# Canada Post XML (Requests/Responses)

This doc lists every XML payload the service sends to, and receives from, Canada Post, based on the current code.

Scope (source of truth in code):
- Requests: `service/canada_post_client.go`, `service/shipping_service.go`, `service/canada_post_types.go`
- Responses: `service/canada_post_types.go`, `service/canada_post_client.go`, `service/canada_post_refund_test.go`

---

## 1) Rates: Request XML (RateRequest)
Endpoint:
- POST `/rs/ship/price`
Headers:
- `Content-Type: application/vnd.cpc.ship.rate-v4+xml`
- `Accept: application/vnd.cpc.ship.rate-v4+xml`
- `Accept-Language: en-CA`
Auth:
- HTTP Basic Auth
Root element:
- `<mailing-scenario xmlns="http://www.canadapost.ca/ws/ship/rate-v4">`

### Fields (what they do)
- `customer-number` (optional): Canada Post customer number. Included when configured.
- `parcel-characteristics/weight`: Parcel weight in **kilograms**.
- `parcel-characteristics/dimensions` (optional): Parcel dimensions in **cm** (length/width/height). Included only if any dimension > 0.
- `origin-postal-code`: Origin postal code. Required by Canada Post.
- `destination`: Exactly one of the following blocks must be used:
  - `domestic/postal-code` for Canada (CA)
  - `united-states/zip-code` for US
  - `international/country-code` for other countries

### Example (domestic)
```xml
<mailing-scenario xmlns="http://www.canadapost.ca/ws/ship/rate-v4">
  <customer-number>123456789</customer-number>
  <parcel-characteristics>
    <weight>1.25</weight>
    <dimensions>
      <length>20</length>
      <width>15</width>
      <height>10</height>
    </dimensions>
  </parcel-characteristics>
  <origin-postal-code>K1A0B1</origin-postal-code>
  <destination>
    <domestic>
      <postal-code>M5V3L9</postal-code>
    </domestic>
  </destination>
</mailing-scenario>
```

---

## 2) Rates: Response XML (RateResponse)
Root element:
- `<price-quotes xmlns="http://www.canadapost.ca/ws/ship/rate-v4">`

### Fields (what they do)
- `price-quote` (list): One quote per service.
  - `service-code`: Canada Post service code (e.g., `DOM.EP`, `USA.EP`).
  - `service-name`: Human name of service.
  - `service-link`: Links for the service.
    - `rel`, `href`, `media-type` (attributes)
  - `price-details`: Cost breakdown.
    - `base`: Base cost before options/taxes.
    - `due`: Total due for the shipment.
    - `taxes`: Tax breakdown.
      - `gst`, `pst`, `hst`: Each has value with `percent` attribute.
    - `options`: Selected/available options.
      - `option` list: `option-code`, `option-name`, `option-price`, `qualifier`.
    - `adjustments`: Pricing adjustments.
      - `adjustment` list: `adjustment-code`, `adjustment-name`, `adjustment-cost`, `qualifier` (optional).
  - `weight-details`: Placeholder in current code (not used by parsing).
  - `service-standard`:
    - `am-delivery` (bool)
    - `guaranteed-delivery` (bool)
    - `expected-transit-time` (int days)
    - `expected-delivery-date` (YYYY-MM-DD)

### Example
```xml
<price-quotes xmlns="http://www.canadapost.ca/ws/ship/rate-v4">
  <price-quote>
    <service-code>DOM.EP</service-code>
    <service-name>Expedited Parcel</service-name>
    <service-link rel="service" href="https://..." media-type="application/vnd.cpc.ship.rate-v4+xml" />
    <price-details>
      <base>10.50</base>
      <due>11.87</due>
      <taxes>
        <gst percent="5">0.53</gst>
        <pst percent="8">0.84</pst>
        <hst percent="0">0</hst>
      </taxes>
      <options>
        <option>
          <option-code>DC</option-code>
          <option-name>Delivery Confirmation</option-name>
          <option-price>0</option-price>
          <qualifier>
            <included>true</included>
          </qualifier>
        </option>
      </options>
      <adjustments>
        <adjustment>
          <adjustment-code>DISC</adjustment-code>
          <adjustment-name>Discount</adjustment-name>
          <adjustment-cost>-1.00</adjustment-cost>
        </adjustment>
      </adjustments>
    </price-details>
    <weight-details />
    <service-standard>
      <am-delivery>false</am-delivery>
      <guaranteed-delivery>true</guaranteed-delivery>
      <expected-transit-time>2</expected-transit-time>
      <expected-delivery-date>2026-02-05</expected-delivery-date>
    </service-standard>
  </price-quote>
</price-quotes>
```

---

## 3) Shipment: Request XML (ShipmentRequest)
Endpoint:
- POST `/rs/{customerNumber}/ncshipment`
Headers:
- `Content-Type: application/vnd.cpc.ncshipment-v4+xml`
- `Accept: application/vnd.cpc.ncshipment-v4+xml`
- `Accept-Language: en-CA`
Auth:
- HTTP Basic Auth
Root element:
- `<non-contract-shipment xmlns="http://www.canadapost.ca/ws/ncshipment-v4">`

### Fields (what they do)
- `requested-shipping-point` (optional): Origin postal code used by Canada Post.
- `delivery-spec`: Main block.
  - `service-code`: Canada Post service code (resolved from rate).
  - `sender`:
    - `name` (optional): Sender name. Defaults to `Sender` if missing.
    - `company`: Sender company. Defaults to name if missing.
    - `contact-phone`: Sender phone (required by CP; max 25 chars).
    - `address-details`:
      - `address-line-1` (required)
      - `address-line-2` (optional)
      - `city` (required)
      - `prov-state` (required)
      - `postal-zip-code` (required)
  - `destination`:
    - `name` (required)
    - `company` (optional)
    - `client-voice-number` (optional; **required for certain services**: `USA.EP`, `USA.XP`, `USA.TP`, `INT.XP`, `INT.TP`)
    - `address-details`:
      - `address-line-1` (required)
      - `address-line-2` (optional)
      - `city` (required)
      - `prov-state` (required)
      - `country-code` (required, 2-letter ISO)
      - `postal-zip-code` (required)
  - `parcel-characteristics`:
    - `weight` (required, kg)
    - `dimensions` (length/width/height, required in current request builder)
  - `preferences`:
    - `show-packing-instructions`: `true` or `false`
  - `options` (optional): Included for international shipments to set a default non-delivery option.
    - `option` list, each with `option-code`
  - `customs` (optional): Required for non-CA destinations.
    - `us-declaration-id` (optional)
    - `currency` (optional, defaults to USD)
    - `conversion-from-cad` (optional, required if customs currency != CAD)
    - `reason-for-export` (mapped to CP codes: `GFT`, `DOC`, `SAM`, `RET`, `REP`, `INT`, `SOG`)
    - `sku-list`:
      - `item` list:
        - `customs-number-of-units` (optional; defaults to 1 per item)
        - `customs-description` (optional)
        - `unit-weight` (optional)
        - `customs-value-per-unit` (optional)
        - `hs-tariff-code` (optional)
        - `sku` (optional)
        - `country-of-origin` (optional)
        - `province-of-origin` (optional)

### Example (CA -> US with customs)
```xml
<non-contract-shipment xmlns="http://www.canadapost.ca/ws/ncshipment-v4">
  <requested-shipping-point>K1A0B1</requested-shipping-point>
  <delivery-spec>
    <service-code>USA.EP</service-code>
    <sender>
      <name>Sender Name</name>
      <company>Sender Name</company>
      <contact-phone>4165551212</contact-phone>
      <address-details>
        <address-line-1>123 King St</address-line-1>
        <city>Ottawa</city>
        <prov-state>ON</prov-state>
        <postal-zip-code>K1A0B1</postal-zip-code>
      </address-details>
    </sender>
    <destination>
      <name>Recipient Name</name>
      <company>Recipient Co</company>
      <client-voice-number>2125559898</client-voice-number>
      <address-details>
        <address-line-1>500 5th Ave</address-line-1>
        <city>New York</city>
        <prov-state>NY</prov-state>
        <country-code>US</country-code>
        <postal-zip-code>10018</postal-zip-code>
      </address-details>
    </destination>
    <parcel-characteristics>
      <weight>1.25</weight>
      <dimensions>
        <length>20</length>
        <width>15</width>
        <height>10</height>
      </dimensions>
    </parcel-characteristics>
    <preferences>
      <show-packing-instructions>true</show-packing-instructions>
    </preferences>
    <options>
      <option>
        <option-code>RASE</option-code>
      </option>
    </options>
    <customs>
      <us-declaration-id>ABC123</us-declaration-id>
      <currency>USD</currency>
      <conversion-from-cad>1.35</conversion-from-cad>
      <reason-for-export>SOG</reason-for-export>
      <sku-list>
        <item>
          <customs-number-of-units>2</customs-number-of-units>
          <customs-description>Shirt</customs-description>
          <unit-weight>0.3</unit-weight>
          <customs-value-per-unit>25.00</customs-value-per-unit>
          <hs-tariff-code>610910</hs-tariff-code>
          <sku>SKU-001</sku>
          <country-of-origin>CN</country-of-origin>
        </item>
      </sku-list>
    </customs>
  </delivery-spec>
</non-contract-shipment>
```

---

## 4) Shipment: Response XML (ShipmentResponse)
Root element:
- `<non-contract-shipment-info>`

### Fields (what they do)
- `shipment-id`: Canada Post shipment ID.
- `tracking-pin`: Tracking number.
- `links/link` (list): URLs for related artifacts.
  - `rel` (attribute): `label`, `refund`, etc.
  - `href` (attribute): URL to artifact.
  - `media-type` (attribute): MIME type (e.g., PDF).
  - `index` (attribute, optional)

### Example
```xml
<non-contract-shipment-info>
  <shipment-id>1234567890123</shipment-id>
  <tracking-pin>9876543210</tracking-pin>
  <links>
    <link rel="label" href="https://.../artifact.pdf" media-type="application/pdf" />
    <link rel="refund" href="https://.../refund" media-type="application/vnd.cpc.ncshipment-v4+xml" />
  </links>
</non-contract-shipment-info>
```

---

## 5) Refund: Request XML (RefundRequest)
Endpoint:
- POST to `refund` URL from shipment response links
Headers:
- `Content-Type: application/vnd.cpc.ncshipment-v4+xml`
- `Accept: application/vnd.cpc.ncshipment-v4+xml`
- `Accept-Language: en-CA`
Auth:
- HTTP Basic Auth
Root element:
- `<non-contract-shipment-refund-request xmlns="http://www.canadapost.ca/ws/ncshipment-v4">`

### Fields (what they do)
- `email`: Email address to receive refund confirmation.

### Example
```xml
<?xml version="1.0" encoding="utf-8"?>
<non-contract-shipment-refund-request xmlns="http://www.canadapost.ca/ws/ncshipment-v4">
  <email>name@example.ca</email>
</non-contract-shipment-refund-request>
```

---

## 6) Refund: Response XML (RefundResponse)
Successful refund response:
- `<non-contract-shipment-refund-request-info xmlns="http://www.canadapost.ca/ws/ncshipment-v4">`

### Fields (what they do)
- `service-ticket-date`: Date of refund ticket (YYYY-MM-DD)
- `service-ticket-id`: Ticket ID

### Example (success)
```xml
<?xml version="1.0" encoding="utf-8"?>
<non-contract-shipment-refund-request-info xmlns="http://www.canadapost.ca/ws/ncshipment-v4">
  <service-ticket-date>2026-02-03</service-ticket-date>
  <service-ticket-id>0123456789</service-ticket-id>
</non-contract-shipment-refund-request-info>
```

---

## 7) Error Response XML (CPMessages)
Some failures return a messages block:
- `<messages>`

### Fields (what they do)
- `message/code`: Error code
- `message/description`: Human message

### Example
```xml
<messages>
  <message>
    <code>7292</code>
    <description>Refund already submitted</description>
  </message>
</messages>
```

---

## Notes / قواعد مهمة من الكود
- الوزن في الطلبات هو بالكيلو جرام، والتحويل يتم من أوقية في `service/shipping_service.go`.
- `client-voice-number` مطلوب فقط لبعض الخدمات الدولية/أمريكا (مذكورة بالأعلى).
- للطلبات الدولية، لازم `customs` وأصناف داخل `sku-list`.
- `conversion-from-cad` مطلوب لو العملة في الجمارك ليست CAD.
- في الشحن الدولي يتم إضافة option افتراضي `RASE` كـ non-delivery option.

If you want this in Arabic-only or want the docs split per endpoint, tell me.
