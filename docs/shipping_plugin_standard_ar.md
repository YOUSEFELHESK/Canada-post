# معيار توحيد أي Shipping Plugin

هذا المستند يشرح الحل الصحيح الذي تم تطبيقه بنجاح، وكيف نستخدمه كـ standard لأي plugin شحن (Canada Post, FedEx, DHL, ...).

## الفكرة الأساسية

1. في صفحة `Settings > Shipping` نُظهر **method واحدة عامة باسم الـ plugin** (مثال: `Canada Post`).
2. في وقت الطلب (`GetShippingRate`) نُرجع **live rates** من مزوّد الشحن نفسه (Regular/Expedited/Priority... حسب العنوان والوزن).
3. مزامنة الـ admin methods تكون **idempotent**: نفس الدالة تُشغّل أكثر من مرة بدون تكرار/نسخ duplicate.

## لماذا هذا هو الحل الصحيح

- لا نحتاج إنشاء method منفصلة لكل service داخل admin settings.
- الـ UI تبقى نظيفة ومفهومة: plugin واحد في الإعدادات، وخدماته تظهر ديناميكياً وقت checkout/order.
- تعمل لكل store بشكل مستقل باستخدام `store_id` و`access token` الخاص به.
- تتحمل اختلافات auth headers وفشل plugin discovery permissions.

## الكود المعياري الذي تم اعتماده

### 1) Generic Sync Engine

الملف: `service/plugin_admin_shipping_standard.go`

الدالة الأساسية:

```go
created, err := EnsurePluginAdminShippingMethods(ctx, grpcAddr, storeID, accessToken, cfg)
```

الـ config المستخدمة:

```go
type PluginAdminSyncConfig struct {
    PluginDisplayName    string
    Methods              []PluginAdminMethod
    PreferredPluginCodes []string
    FallbackPluginCodes  []string
    IsPluginMethod       func(item *shippingpb.ShippingRequest, pluginCodes []string) bool
}
```

### 2) Wrapper صغير لكل Plugin

الملف: `service/admin_shipping_methods.go`

```go
func EnsureCanadaPostAdminShippingMethods(ctx context.Context, grpcAddr string, storeID int64, accessToken string) (int, error) {
    cfg := PluginAdminSyncConfig{
        PluginDisplayName:    "Canada Post",
        Methods:              []PluginAdminMethod{{Name: "Canada Post"}},
        PreferredPluginCodes: []string{"shipstation"},
        FallbackPluginCodes:  []string{"shipstation", "canada_post", "canadapost", "Canada Post", "CANADA_POST_LIVE"},
        IsPluginMethod: func(item *shippingpb.ShippingRequest, pluginCodes []string) bool {
            return pluginMethodMatchesByNameOrCode(item, "Canada Post", pluginCodes)
        },
    }
    return EnsurePluginAdminShippingMethods(ctx, grpcAddr, storeID, accessToken, cfg)
}
```

### 3) ربطه في OAuth/Install flow

الملف: `protocol/http/handlers.go`

```go
created, err := service.EnsureCanadaPostAdminShippingMethods(ctx, targetGRPCAddr, clientID, accessToken)
```

## Template جاهز لأي Plugin جديد

```go
func EnsureXAdminShippingMethods(ctx context.Context, grpcAddr string, storeID int64, accessToken string) (int, error) {
    cfg := PluginAdminSyncConfig{
        PluginDisplayName:    "X Shipping",
        Methods:              []PluginAdminMethod{{Name: "X Shipping"}},
        PreferredPluginCodes: []string{"x_shipping"},
        FallbackPluginCodes:  []string{"x_shipping", "x", "X Shipping"},
        IsPluginMethod: func(item *shippingpb.ShippingRequest, pluginCodes []string) bool {
            return pluginMethodMatchesByNameOrCode(item, "X Shipping", pluginCodes)
        },
    }
    return EnsurePluginAdminShippingMethods(ctx, grpcAddr, storeID, accessToken, cfg)
}
```

## Checklist قبل اعتماد أي Plugin

1. عند install/oauth: نفّذ دالة `Ensure<Plugin>AdminShippingMethods`.
2. في `GetShippingRate`: رجّع السعر + ETA + الخدمة + rate_id.
3. لا تضيف services كثيرة داخل admin settings؛ اتركها method عامة واحدة.
4. تأكد من fallback plugin codes في حال `Plugins.GetAllPlugins` يرجع permission denied.
5. اختبر على store مختلفة (غير store 1) للتأكد من العزل الصحيح للبيانات.
