package service

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"
)

type CanadaPostClient struct {
	BaseURL        string
	Username       string
	Password       string
	CustomerNumber string
	HTTPClient     *http.Client
}

func NewCanadaPostClient(username, password, customerNumber, baseURL string) *CanadaPostClient {
	client := &http.Client{Timeout: 20 * time.Second}
	return &CanadaPostClient{
		BaseURL:        baseURL,
		Username:       username,
		Password:       password,
		CustomerNumber: customerNumber,
		HTTPClient:     client,
	}
}

func (c *CanadaPostClient) GetRates(ctx context.Context, req *RateRequest) (*RateResponse, error) {
	if c == nil {
		return nil, fmt.Errorf("canada post client is nil")
	}
	if req == nil {
		return nil, fmt.Errorf("rate request is nil")
	}
	req.XMLNS = "http://www.canadapost.ca/ws/ship/rate-v4"

	xmlData, err := xml.MarshalIndent(req, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	log.Printf("Canada Post request XML:\n%s\n", string(xmlData))

	url := c.BaseURL + "/rs/ship/price"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(xmlData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	dump, err := httputil.DumpRequestOut(httpReq, true)
	if err != nil {
		log.Printf("Failed to dump request: %v", err)
	} else {
		log.Printf("Full HTTP request:\n%s\n", string(dump))
	}

	httpReq.Header.Set("Content-Type", "application/vnd.cpc.ship.rate-v4+xml")
	httpReq.Header.Set("Accept", "application/vnd.cpc.ship.rate-v4+xml")
	httpReq.Header.Set("Accept-Language", "en-CA")
	httpReq.SetBasicAuth(c.Username, c.Password)

	resp, err := c.httpClient().Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	log.Printf("Canada Post raw response:\n%s\n", string(bodyBytes))

	var rateResp RateResponse
	if err := xml.Unmarshal(bodyBytes, &rateResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &rateResp, nil
}

func (c *CanadaPostClient) CreateShipment(ctx context.Context, req *ShipmentRequest) (*ShipmentResponse, error) {
	if c == nil {
		return nil, fmt.Errorf("canada post client is nil")
	}
	if req == nil {
		return nil, fmt.Errorf("shipment request is nil")
	}

	
	req.XMLNS = "http://www.canadapost.ca/ws/ncshipment-v4"

	
	log.Printf("ShipmentRequest RAW: %+v\n", req)

	xmlData, err := xml.MarshalIndent(req, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	
	log.Println("ShipmentRequest XML to Canada Post:\n", string(xmlData))

	url := fmt.Sprintf("%s/rs/%s/ncshipment", c.BaseURL, c.CustomerNumber)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(xmlData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/vnd.cpc.ncshipment-v4+xml")
	httpReq.Header.Set("Accept", "application/vnd.cpc.ncshipment-v4+xml")
	httpReq.Header.Set("Accept-Language", "en-CA")
	httpReq.SetBasicAuth(c.Username, c.Password)

	resp, err := c.httpClient().Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var shipmentResp ShipmentResponse
	if err := xml.NewDecoder(resp.Body).Decode(&shipmentResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &shipmentResp, nil
}


func (c *CanadaPostClient) GetArtifact(ctx context.Context, artifactURL string) ([]byte, error) {
	if c == nil {
		return nil, fmt.Errorf("canada post client is nil")
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, artifactURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Accept", "application/pdf")
	httpReq.SetBasicAuth(c.Username, c.Password)

	resp, err := c.httpClient().Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to download artifact: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download artifact: status %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// Request Non-Contract Shipment Refund – REST
func (c *CanadaPostClient) RefundShipment(ctx context.Context, refundURL string, email string) (*RefundResponse, error) {
	if c == nil {
		return nil, fmt.Errorf("canada post client is nil")
	}
	refundURL = strings.TrimSpace(refundURL)
	if refundURL == "" {
		return nil, fmt.Errorf("refund url is required")
	}
	email = strings.TrimSpace(email)
	if email == "" {
		return nil, fmt.Errorf("refund email is required")
	}

	req := RefundRequest{
		XMLNS: "http://www.canadapost.ca/ws/ncshipment-v4",
		Email: email,
	}
	xmlData, err := xml.MarshalIndent(req, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal refund request: %w", err)
	}
	xmlData = append([]byte(xml.Header), xmlData...)
	log.Printf("Canada Post refund request XML:\n%s\n", string(xmlData))

	bodyReader := bytes.NewReader(xmlData)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, refundURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create refund request: %w", err)
	}
	httpReq.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(xmlData)), nil
	}
	httpReq.Header.Set("Content-Type", "application/vnd.cpc.ncshipment-v4+xml")
	httpReq.Header.Set("Accept", "application/vnd.cpc.ncshipment-v4+xml")
	httpReq.Header.Set("Accept-Language", "en-CA")
	httpReq.SetBasicAuth(c.Username, c.Password)

	logRefundRequest(httpReq)

	resp, err := c.httpClient().Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send refund request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read refund response: %w", err)
	}
	log.Printf("Canada Post refund response status=%d\n", resp.StatusCode)
	log.Printf("Canada Post refund raw response:\n%s\n", string(bodyBytes))

	if resp.StatusCode == http.StatusNotFound {
		return nil, &CPRefundError{StatusCode: resp.StatusCode, Code: "404", Description: "invalid shipment id or refund link"}
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		if msg := parseRefundMessage(bodyBytes); msg != nil {
			return nil, &CPRefundError{StatusCode: resp.StatusCode, Code: msg.Code, Description: msg.Description}
		}
		return nil, fmt.Errorf("refund API error %d: %s", resp.StatusCode, string(bodyBytes))
	}

	refundResp, msg, err := parseRefundResponse(bodyBytes)
	if err != nil {
		return nil, err
	}
	if msg != nil {
		return nil, &CPRefundError{StatusCode: resp.StatusCode, Code: msg.Code, Description: msg.Description}
	}
	if refundResp == nil {
		return nil, fmt.Errorf("refund response is empty")
	}
	return refundResp, nil
}

// Request Non-Contract Shipment Refund – REST
type CPRefundError struct {
	StatusCode  int
	Code        string
	Description string
}

func (e *CPRefundError) Error() string {
	if e == nil {
		return "refund error"
	}
	if e.Code != "" && e.Description != "" {
		return fmt.Sprintf("refund error code=%s: %s", e.Code, e.Description)
	}
	if e.Description != "" {
		return e.Description
	}
	return "refund error"
}

func parseRefundResponse(body []byte) (*RefundResponse, *CPMessage, error) {
	if len(body) == 0 {
		return nil, nil, fmt.Errorf("refund response is empty")
	}
	var resp RefundResponse
	if err := xml.Unmarshal(body, &resp); err == nil {
		if strings.TrimSpace(resp.ServiceTicketID) != "" || strings.TrimSpace(resp.ServiceTicketDate) != "" {
			return &resp, nil, nil
		}
	}
	if msg := parseRefundMessage(body); msg != nil {
		return nil, msg, nil
	}
	return nil, nil, fmt.Errorf("unexpected refund response payload")
}

func parseRefundMessage(body []byte) *CPMessage {
	var msgs CPMessages
	if err := xml.Unmarshal(body, &msgs); err != nil {
		return nil
	}
	if len(msgs.Messages) == 0 {
		return nil
	}
	msg := msgs.Messages[0]
	msg.Code = strings.TrimSpace(msg.Code)
	msg.Description = strings.TrimSpace(msg.Description)
	if msg.Code == "" && msg.Description == "" {
		return nil
	}
	return &msg
}

func logRefundRequest(req *http.Request) {
	if req == nil {
		return
	}
	cloned := req.Clone(req.Context())
	if cloned.Header.Get("Authorization") != "" {
		cloned.Header.Set("Authorization", "Basic [REDACTED]")
	}
	if cloned.GetBody != nil {
		body, err := cloned.GetBody()
		if err == nil {
			cloned.Body = body
		}
	}
	dump, err := httputil.DumpRequestOut(cloned, true)
	if err != nil {
		log.Printf("Failed to dump refund request: %v", err)
		return
	}
	log.Printf("Refund request:\n%s\n", string(dump))
}

func (c *CanadaPostClient) httpClient() *http.Client {
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: 20 * time.Second}
	}
	return c.HTTPClient
}
