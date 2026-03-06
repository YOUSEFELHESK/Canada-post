package service

import (
	"context"

	shippingpb "bitbucket.org/lexmodo/proto/shipping"
)

var canadaPostAdminShippingMethods = []PluginAdminMethod{
	// Keep one generic plugin entry in admin shipping settings.
	{Name: "Canada Post"},
}

// EnsureCanadaPostAdminShippingMethods syncs Canada Post admin method(s) for a store.
func EnsureCanadaPostAdminShippingMethods(ctx context.Context, grpcAddr string, storeID int64, accessToken string) (int, error) {
	config := PluginAdminSyncConfig{
		PluginDisplayName:    "Canada Post",
		Methods:              canadaPostAdminShippingMethods,
		PreferredPluginCodes: []string{"shipstation"},
		FallbackPluginCodes:  []string{"shipstation", "canada_post", "canadapost", "Canada Post", "CANADA_POST_LIVE"},
		IsPluginMethod: func(item *shippingpb.ShippingRequest, pluginCodes []string) bool {
			return pluginMethodMatchesByNameOrCode(item, "Canada Post", pluginCodes)
		},
	}

	return EnsurePluginAdminShippingMethods(ctx, grpcAddr, storeID, accessToken, config)
}
