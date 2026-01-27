package service

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
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

	url := c.BaseURL + "/rs/ship/price"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(xmlData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
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

	var rateResp RateResponse
	if err := xml.NewDecoder(resp.Body).Decode(&rateResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
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

	xmlData, err := xml.MarshalIndent(req, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

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

func (c *CanadaPostClient) httpClient() *http.Client {
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: 20 * time.Second}
	}
	return c.HTTPClient
}
