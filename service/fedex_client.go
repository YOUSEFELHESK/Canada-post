package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"lexmodo-plugin/config"
)

type FedexClient struct {
	RatesURL     string
	ShipmentsURL string
	CancelURL    string
	HTTP         *http.Client
}

func NewFedexClient(cfg config.Config) *FedexClient {
	return &FedexClient{
		RatesURL:     cfg.FedexRatesAPIURL,
		ShipmentsURL: cfg.FedexShipmentsURL,
		CancelURL:    cfg.FedexCancelURL,
		HTTP:         &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *FedexClient) GetRates(ctx context.Context, payload fedexRatesRequest) ([]fedexAPIRate, error) {
	var rates []fedexAPIRate
	if err := c.postJSON(ctx, c.RatesURL, payload, &rates); err != nil {
		return nil, err
	}
	if len(rates) == 0 {
		return nil, errors.New("no rates returned from upstream API")
	}
	return rates, nil
}

func (c *FedexClient) CreateShipment(ctx context.Context, payload fedexShipmentRequest) (fedexShipmentResponse, error) {
	var shipment fedexShipmentResponse
	if err := c.postJSON(ctx, c.ShipmentsURL, payload, &shipment); err != nil {
		return fedexShipmentResponse{}, err
	}
	return shipment, nil
}

func (c *FedexClient) CancelShipment(ctx context.Context, payload fedexShipmentCancelRequest) (fedexShipmentCancelResponse, error) {
	var cancelResp fedexShipmentCancelResponse
	if err := c.postJSON(ctx, c.CancelURL, payload, &cancelResp); err != nil {
		return fedexShipmentCancelResponse{}, err
	}
	return cancelResp, nil
}

func (c *FedexClient) postJSON(ctx context.Context, url string, payload any, out any) error {
	if c == nil {
		return errors.New("fedex client is nil")
	}
	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: 20 * time.Second}
	}
	if url == "" {
		return errors.New("fedex API URL is empty")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		bodyBytes, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return errors.New("upstream API error")
		}
		return errors.New(string(bodyBytes))
	}

	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
