package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseRefundResponseSuccess(t *testing.T) {
	body := []byte(`<?xml version="1.0" encoding="utf-8"?>
<non-contract-shipment-refund-request-info xmlns="http://www.canadapost.ca/ws/ncshipment-v4">
  <service-ticket-date>2026-02-03</service-ticket-date>
  <service-ticket-id>0123456789</service-ticket-id>
</non-contract-shipment-refund-request-info>`)

	resp, msg, err := parseRefundResponse(body)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if msg != nil {
		t.Fatalf("expected no message, got %+v", msg)
	}
	if resp == nil {
		t.Fatal("expected refund response")
	}
	if resp.ServiceTicketID != "0123456789" || resp.ServiceTicketDate != "2026-02-03" {
		t.Fatalf("unexpected refund response: %+v", resp)
	}
}

func TestParseRefundResponseMessages(t *testing.T) {
	body := []byte(`<messages>
  <message>
    <code>7292</code>
    <description>Refund already submitted</description>
  </message>
</messages>`)

	resp, msg, err := parseRefundResponse(body)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp != nil {
		t.Fatalf("expected nil response, got %+v", resp)
	}
	if msg == nil || msg.Code != "7292" {
		t.Fatalf("expected message code 7292, got %+v", msg)
	}
}

func TestRefundShipment404(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewCanadaPostClient("user", "pass", "123", server.URL)
	_, err := client.RefundShipment(context.Background(), server.URL+"/refund", "name@example.ca")
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if refundErr, ok := err.(*CPRefundError); !ok || refundErr.StatusCode != http.StatusNotFound {
		t.Fatalf("expected CPRefundError 404, got %T %+v", err, err)
	}
}

func TestRefundShipmentUnexpectedPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.cpc.ncshipment-v4+xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<html>oops</html>`))
	}))
	defer server.Close()

	client := NewCanadaPostClient("user", "pass", "123", server.URL)
	_, err := client.RefundShipment(context.Background(), server.URL+"/refund", "name@example.ca")
	if err == nil {
		t.Fatal("expected error for unexpected payload")
	}
}
