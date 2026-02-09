package main

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"google.golang.org/protobuf/types/known/wrapperspb"

	//"google.golang.org/protobuf/types/known/emptypb"

	//orderspb "bitbucket.org/lexmodo/proto/orders"

	shippingpb "bitbucket.org/lexmodo/proto/shipping"
	//"lexmodo-plugin/customers"
	//"bitbucket.org/lexmodo/proto/address"
	//"google.golang.org/protobuf/types/known/wrapperspb"
)

// GetCustomer fetches a customer record from local gRPC server
type Customer struct {
	ID         string
	FirstName  string
	LastName   string
	Email      string
	TotalSpent int
}

func main() {
	fmt.Println("grpc-client is a helper binary; call GetCustomerData from code if needed.")
}

func GetCustomerData(storeID int, accessToken string) ([]Customer, error) {
	// Connect to gRPC server
	conn, err := grpc.Dial("192.168.1.99:7000", grpc.WithInsecure())
	if err != nil {
		return nil, fmt.Errorf("failed to connect to gRPC: %v", err)
	}
	defer conn.Close()

	// Add headers
	md := metadata.New(map[string]string{
		"authorization": "Bearer " + accessToken,
		"x-client-id":   fmt.Sprintf("%d", storeID),
		"x-force-auth":  "true",
	})
	ctx := metadata.NewOutgoingContext(context.Background(), md)

	var allCustomers []Customer
	client := shippingpb.NewShippingsClient(conn)
	shipping := &shippingpb.ShippingRequest{
		ShippingName:                   wrapperspb.String("Canada Post (Live "),
		ShippingCode:                   wrapperspb.String("CANADA_POST_LIVE"),
		ShippingStatus:                 wrapperspb.Bool(true),
		ShippingType:                   shippingpb.ShippingRequest_external_rate,
		ShippingMethodCalculationsType: shippingpb.ShippingRequest_none,
	}

	shippingRes, err := client.CreateAdminShippingMethods(ctx, shipping)
	if err != nil {
		return nil, err
	}
	fmt.Println("✅ Shipping Response:", shippingRes)

	return allCustomers, nil
}
