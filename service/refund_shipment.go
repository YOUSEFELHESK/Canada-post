package service

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	orderspb "bitbucket.org/lexmodo/proto/orders"
	shippingpluginpb "bitbucket.org/lexmodo/proto/shipping_plugin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// RefundShipment submits a Canada Post non-contract shipment refund request.
// Request Non-Contract Shipment Refund – REST
func (s *Server) RefundShipment(
	ctx context.Context,
	req *shippingpluginpb.ShippingRateRequest,
) (*shippingpluginpb.ResultResponse, error) {
	log.Println("📥 RefundShipment RECEIVED")
	log.Printf("%+v\n", req)
	logIncomingMetadata(ctx)

	shipRequest := req.GetShipRequest()
	if shipRequest == nil {
		return &shippingpluginpb.ResultResponse{
			Success: false,
			Failure: true,
			Code:    "400",
			Message: "RefundShipment missing ship_request",
		}, nil
	}

	labelID := strings.TrimSpace(shipRequest.GetLabelId())
	if labelID == "" {
		return &shippingpluginpb.ResultResponse{
			Success: false,
			Failure: true,
			Code:    "400",
			Message: "RefundShipment missing label_id",
		}, nil
	}

	record, err := s.Store.LoadLabelRecordByLabelID(labelID)
	if err != nil {
		log.Println("❌ Failed to load label record:", err)
		return &shippingpluginpb.ResultResponse{
			Success: false,
			Failure: true,
			Code:    "500",
			Message: "RefundShipment failed to load label record",
		}, nil
	}
	if strings.TrimSpace(record.ID) == "" {
		return &shippingpluginpb.ResultResponse{
			Success: false,
			Failure: true,
			Code:    "404",
			Message: "RefundShipment label not found",
		}, nil
	}
	if strings.TrimSpace(record.RefundLink) == "" {
		return &shippingpluginpb.ResultResponse{
			Success: false,
			Failure: true,
			Code:    "400",
			Message: "RefundShipment refund link not found",
		}, nil
	}
	if strings.TrimSpace(record.InvoiceUUID) == "" {
		return &shippingpluginpb.ResultResponse{
			Success: false,
			Failure: true,
			Code:    "400",
			Message: "RefundShipment invoice uuid not found",
		}, nil
	}

	log.Printf("refund shipment label_id=%s invoice_uuid=%s refund_link=%s", labelID, record.InvoiceUUID, record.RefundLink)

	clientID := int64(0)
	if req.GetShippingAuth() != nil && req.GetShippingAuth().GetStoreInfo() != nil {
		clientID = int64(req.GetShippingAuth().GetStoreInfo().GetClientId())
	}
	ordersToken := ""
	if clientID > 0 {
		ordersToken = strings.TrimSpace(s.Store.GetAccessToken(int(clientID)))
	}
	email, err := fetchCustomerEmailFromOrders(ctx, s.Config.OrdersGRPCAddr, record.InvoiceUUID, clientID, ordersToken)
	if err != nil {
		log.Println("❌ Failed to fetch customer email from orders:", err)
		return &shippingpluginpb.ResultResponse{
			Success: false,
			Failure: true,
			Code:    "500",
			Message: "RefundShipment failed to load customer email",
		}, nil
	}
	if strings.TrimSpace(email) == "" {
		return &shippingpluginpb.ResultResponse{
			Success: false,
			Failure: true,
			Code:    "400",
			Message: "customer email missing (required by Canada Post refund request)",
		}, nil
	}

	refundResp, err := s.CanadaPost.RefundShipment(ctx, record.RefundLink, email)
	if err != nil {
		log.Println("❌ RefundShipment error:", err)
		return &shippingpluginpb.ResultResponse{
			Success: false,
			Failure: true,
			Code:    "400",
			Message: err.Error(),
		}, nil
	}

	log.Printf("✅ RefundShipment ticket id=%s date=%s\n", strings.TrimSpace(refundResp.ServiceTicketID), strings.TrimSpace(refundResp.ServiceTicketDate))
	return &shippingpluginpb.ResultResponse{
		Success: true,
		Code:    "200",
		Message: fmt.Sprintf("RefundShipment OK ticket_id=%s ticket_date=%s", strings.TrimSpace(refundResp.ServiceTicketID), strings.TrimSpace(refundResp.ServiceTicketDate)),
	}, nil
}

func fetchCustomerEmailFromOrders(ctx context.Context, addr string, invoiceUUID string, clientID int64, accessToken string) (string, error) {
	addr = strings.TrimSpace(addr)
	invoiceUUID = strings.TrimSpace(invoiceUUID)
	if addr == "" {
		return "", fmt.Errorf("orders grpc addr is required")
	}
	if invoiceUUID == "" {
		return "", fmt.Errorf("invoice uuid is required")
	}
	conn, err := grpc.Dial(addr, grpc.WithInsecure())
	if err != nil {
		return "", err
	}
	defer conn.Close()

	incomingAuth := ""
	incomingClientID := ""
	incomingSource := ""
	if incoming, ok := metadata.FromIncomingContext(ctx); ok {
		if auths := incoming.Get("authorization"); len(auths) > 0 {
			incomingAuth = strings.TrimSpace(auths[0])
		}
		if ids := incoming.Get("x-client-id"); len(ids) > 0 {
			incomingClientID = strings.TrimSpace(ids[0])
		}
		if sources := incoming.Get("x-request-source"); len(sources) > 0 {
			incomingSource = strings.TrimSpace(sources[0])
		}
	}
	accessToken = strings.TrimSpace(accessToken)
	if incomingAuth == "" && accessToken == "" {
		return "", fmt.Errorf("authorization is missing for orders request")
	}

	callOrders := func(authHeader string) (string, error) {
		md := metadata.New(map[string]string{
			"x-force-auth": "true",
		})
		if strings.TrimSpace(authHeader) != "" {
			md.Set("authorization", strings.TrimSpace(authHeader))
		}
		if incomingClientID != "" {
			md.Set("x-client-id", incomingClientID)
		} else if clientID > 0 {
			md.Set("x-client-id", strconv.FormatInt(clientID, 10))
		} else if ctxID := strings.TrimSpace(clientIDFromContext(ctx)); ctxID != "" {
			md.Set("x-client-id", ctxID)
		}
		if incomingSource != "" {
			md.Set("x-request-source", incomingSource)
		}

		outCtx := metadata.NewOutgoingContext(ctx, md)
		req := &orderspb.OrdersRequest{
			InvoiceUuid:         invoiceUUID,
			ShowOnlyUnpaidItems: false,
		}
		resp, err := orderspb.NewOrdersClient(conn).Invoice(outCtx, req)
		if err != nil {
			return "", err
		}
		if resp.GetInvoice() == nil {
			return "", nil
		}
		email := strings.TrimSpace(resp.GetInvoice().GetCustomersEmailAddress())
		log.Printf("orders grpc: invoice_uuid=%s customer_email=%s", invoiceUUID, redactEmail(email))
		return email, nil
	}

	if accessToken != "" {
		email, err := callOrders("Bearer " + accessToken)
		if err == nil {
			return email, nil
		}
		if st, ok := status.FromError(err); ok && st.Code() != codes.Unauthenticated {
			return "", err
		}
	}

	if incomingAuth == "" {
		return "", fmt.Errorf("authorization is missing for orders request")
	}

	rawAuth := incomingAuth
	if strings.HasPrefix(strings.ToLower(rawAuth), "bearer ") {
		rawAuth = strings.TrimSpace(rawAuth[7:])
	}
	email, err := callOrders(rawAuth)
	if err == nil {
		return email, nil
	}
	if st, ok := status.FromError(err); ok && st.Code() == codes.Unauthenticated {
		bearerAuth := incomingAuth
		if !strings.HasPrefix(strings.ToLower(bearerAuth), "bearer ") {
			bearerAuth = "Bearer " + bearerAuth
		}
		return callOrders(bearerAuth)
	}
	return "", err
}

func redactEmail(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parts := strings.SplitN(value, "@", 2)
	if len(parts) != 2 {
		return value
	}
	local := parts[0]
	if len(local) <= 2 {
		return "***@" + parts[1]
	}
	return local[:2] + "***@" + parts[1]
}
