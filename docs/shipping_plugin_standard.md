# Shipping Plugin Standard

This document captures the production pattern we implemented for Canada Post and turns it into a reusable standard for any shipping plugin.

## Goal

For any shipping plugin:

1. Keep **one generic method** in `Settings > Shipping` (plugin-level entry).
2. Return **live rates** from `GetShippingRate` based on the order destination/parcel.
3. Make plugin setup **idempotent** after OAuth/install (safe to run many times).

## What We Standardized

### 1. Generic admin-shipping sync engine

Reusable engine file:

- `service/plugin_admin_shipping_standard.go`

Main exported API:

- `EnsurePluginAdminShippingMethods(ctx, grpcAddr, storeID, accessToken, cfg)`

Config model:

- `PluginAdminSyncConfig`
  - `PluginDisplayName`: plugin name shown to admins (ex: `Canada Post`)
  - `Methods`: visible methods in admin settings (usually one generic row)
  - `PreferredPluginCodes`: preferred plugin code(s)
  - `FallbackPluginCodes`: fallback codes if plugin discovery is not allowed
  - `IsPluginMethod`: optional matcher for legacy rows

What this engine does:

1. Reads current admin shipping methods (`GetAdminShippingMethods`).
2. Resolves plugin code candidates from `Plugins.GetAllPlugins` (when permitted).
3. Renames legacy plugin-specific rows to one generic name.
4. Creates missing generic method(s) using `CreateAdminShippingMethods`.
5. Works with multiple auth header formats/fallbacks.
6. Returns count of **newly created** methods only.

### 2. Plugin-specific wrapper is tiny

Canada Post wrapper:

- `service/admin_shipping_methods.go`

It only supplies config to the standard engine.

### 3. OAuth callback triggers sync

After token is available, call plugin sync once:

- `protocol/http/handlers.go` → `syncAdminShippingMethods(...)`

This ensures every store gets its plugin method(s) automatically after install/auth.

### 4. Live rates remain in `GetShippingRate`

Admin settings shows plugin-level method(s), while order modal shows live services/rates returned by plugin logic.

For external rates to display correctly, always return:

- price
- ETA date
- ETA days
- guaranteed flag (if available)

## Code: Reusable Pattern

### A. Generic Engine Usage (for any plugin)

```go
// service/<plugin>_admin_shipping_methods.go
package service

import (
    "context"

    shippingpb "bitbucket.org/lexmodo/proto/shipping"
)

var acmePluginMethods = []PluginAdminMethod{
    {Name: "Acme Shipping"},
}

func EnsureAcmeAdminShippingMethods(
    ctx context.Context,
    grpcAddr string,
    storeID int64,
    accessToken string,
) (int, error) {
    cfg := PluginAdminSyncConfig{
        PluginDisplayName:    "Acme Shipping",
        Methods:              acmePluginMethods,
        PreferredPluginCodes: []string{"acme_shipping"},
        FallbackPluginCodes:  []string{"acme_shipping", "acme", "Acme Shipping"},
        IsPluginMethod: func(item *shippingpb.ShippingRequest, pluginCodes []string) bool {
            return pluginMethodMatchesByNameOrCode(item, "Acme Shipping", pluginCodes)
        },
    }

    return EnsurePluginAdminShippingMethods(ctx, grpcAddr, storeID, accessToken, cfg)
}
```

### B. Hook in OAuth/install callback

```go
// after token exchange or when valid access token already exists
_, err := service.EnsureAcmeAdminShippingMethods(ctx, grpcAddr, storeID, accessToken)
if err != nil {
    log.Printf("acme shipping sync failed for store %d: %v", storeID, err)
}
```

### C. Return live rates (minimum required fields)

```go
rates = append(rates, &shippingpluginpb.ShippingRate{
    ShippingrateId:                     rateID,
    ShippingrateCarrierName:            "Acme Shipping",
    ShippingrateServiceName:            serviceName,
    ShippingratePrice:                  uint32(priceCents),
    ShippingrateDeliveryDays:           etaDays,
    ShippingrateDeliveryDate:           etaDate,   // YYYY-MM-DD
    ShippingrateDeliveryDateGuaranteed: guaranteed,
})
```

## Implementation Checklist (for new plugins)

1. Implement plugin API client (`GetRates`, `CreateLabel`, `RefundLabel` if needed).
2. Add plugin wrapper using `EnsurePluginAdminShippingMethods`.
3. Call wrapper from OAuth callback/session flow.
4. Ensure `GetShippingRate` returns full live-rate fields (price + ETA).
5. Add fallback plugin codes for environments where plugin discovery is denied.
6. Keep only one generic row in admin settings unless business requires multiple rows.

## Why This Standard Works

1. **Idempotent**: repeated callback/sync does not duplicate rows.
2. **Store-safe**: runs per `store_id` with store auth headers.
3. **UI-consistent**: admin settings shows plugin identity, order modal shows live services.
4. **Permission-tolerant**: still works if plugin discovery endpoint is denied.
5. **Portable**: same engine works for Canada Post, FedEx, DHL, UPS, etc.

