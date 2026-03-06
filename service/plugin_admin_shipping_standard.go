package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	pluginspb "bitbucket.org/lexmodo/proto/plugins"
	shippingpb "bitbucket.org/lexmodo/proto/shipping"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// PluginAdminMethod is one visible shipping method entry in admin/shipping settings.
type PluginAdminMethod struct {
	Name string
}

// PluginAdminSyncConfig defines how a plugin should be synced to admin shipping methods.
type PluginAdminSyncConfig struct {
	PluginDisplayName string
	Methods           []PluginAdminMethod

	// Preferred codes are tried first when creating methods.
	PreferredPluginCodes []string
	// Fallback codes are used if discovery is unavailable.
	FallbackPluginCodes []string

	// Optional matcher for legacy/plugin-owned methods.
	// If nil, a default matcher by plugin display name or code is used.
	IsPluginMethod func(item *shippingpb.ShippingRequest, pluginCodes []string) bool
}

// EnsurePluginAdminShippingMethods syncs plugin methods for a store.
// It is safe to call multiple times.
func EnsurePluginAdminShippingMethods(
	ctx context.Context,
	grpcAddr string,
	storeID int64,
	accessToken string,
	cfg PluginAdminSyncConfig,
) (int, error) {
	if strings.TrimSpace(grpcAddr) == "" {
		return 0, errors.New("shipping grpc address is required")
	}
	if storeID <= 0 {
		return 0, errors.New("store id is required")
	}
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return 0, errors.New("access token is required")
	}
	if len(cfg.Methods) == 0 {
		return 0, errors.New("at least one method is required")
	}

	conn, err := grpc.DialContext(ctx, grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return 0, fmt.Errorf("failed to connect to shipping grpc: %w", err)
	}
	defer conn.Close()

	client := shippingpb.NewShippingsClient(conn)
	attempts := buildPluginSyncAuthAttempts(accessToken)
	if len(attempts) == 0 {
		return 0, errors.New("no valid authorization attempts for shipping grpc")
	}

	var failures []string
	for _, attempt := range attempts {
		created, attemptErr := ensurePluginMethodsWithAttempt(ctx, conn, client, storeID, attempt, cfg)
		if attemptErr == nil {
			return created, nil
		}
		failures = append(failures, fmt.Sprintf("%s: %v", attempt.label, attemptErr))
	}

	return 0, fmt.Errorf("failed to sync admin shipping methods: %s", strings.Join(failures, " | "))
}

type pluginSyncAuthAttempt struct {
	label         string
	authorization string
	forceAuth     bool
}

func buildPluginSyncAuthAttempts(token string) []pluginSyncAuthAttempt {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}

	raw := token
	if strings.HasPrefix(strings.ToLower(raw), "bearer ") {
		raw = strings.TrimSpace(raw[7:])
	}
	if raw == "" {
		return nil
	}
	bearer := "Bearer " + raw

	candidates := []pluginSyncAuthAttempt{
		{label: "bearer+x-force-auth", authorization: bearer, forceAuth: true},
		{label: "bearer", authorization: bearer, forceAuth: false},
	}

	seen := make(map[string]bool)
	filtered := make([]pluginSyncAuthAttempt, 0, len(candidates))
	for _, candidate := range candidates {
		key := candidate.authorization + "|" + fmt.Sprintf("%t", candidate.forceAuth)
		if seen[key] {
			continue
		}
		seen[key] = true
		filtered = append(filtered, candidate)
	}
	return filtered
}

func ensurePluginMethodsWithAttempt(
	ctx context.Context,
	conn *grpc.ClientConn,
	client shippingpb.ShippingsClient,
	storeID int64,
	attempt pluginSyncAuthAttempt,
	cfg PluginAdminSyncConfig,
) (int, error) {
	md := metadata.New(map[string]string{
		"authorization": attempt.authorization,
		"x-client-id":   fmt.Sprintf("%d", storeID),
	})
	if attempt.forceAuth {
		md.Set("x-force-auth", "true")
	}
	callCtx := metadata.NewOutgoingContext(ctx, md)

	existingResp, err := client.GetAdminShippingMethods(callCtx, &emptypb.Empty{})
	if err != nil {
		return 0, fmt.Errorf("GetAdminShippingMethods grpc error: %w", err)
	}
	if existingResp == nil {
		return 0, errors.New("GetAdminShippingMethods empty response")
	}
	if existingResp.GetFailure() {
		return 0, fmt.Errorf("GetAdminShippingMethods rejected (code=%s message=%q)",
			strings.TrimSpace(existingResp.GetCode()), strings.TrimSpace(existingResp.GetMessage()))
	}

	existingItems := existingResp.GetShippingInfo()
	existingNames := make(map[string]bool, len(existingItems))
	for _, item := range existingItems {
		if item == nil || item.GetShippingName() == nil {
			continue
		}
		name := normalizePluginMethodName(item.GetShippingName().GetValue())
		if name != "" {
			existingNames[name] = true
		}
	}

	discovered := discoverPluginCodes(callCtx, conn, cfg.PluginDisplayName)
	pluginCodes := mergePluginCodes(cfg.PreferredPluginCodes, discovered, cfg.FallbackPluginCodes)
	if len(pluginCodes) == 0 {
		return 0, errors.New("no plugin codes available for sync")
	}

	pluginMatcher := cfg.IsPluginMethod
	if pluginMatcher == nil {
		pluginMatcher = func(item *shippingpb.ShippingRequest, codes []string) bool {
			return pluginMethodMatchesByNameOrCode(item, cfg.PluginDisplayName, codes)
		}
	}

	targetName := cfg.Methods[0].Name
	if updated, err := ensureGenericPluginName(callCtx, client, existingItems, pluginCodes, targetName, pluginMatcher); err != nil {
		return 0, err
	} else if updated {
		existingNames[normalizePluginMethodName(targetName)] = true
	}

	created := 0
	for _, method := range cfg.Methods {
		name := normalizePluginMethodName(method.Name)
		if name == "" || existingNames[name] {
			continue
		}

		newlyCreated, err := createMethodWithBestPluginCode(callCtx, client, method.Name, pluginCodes)
		if err != nil {
			return created, err
		}

		existingNames[name] = true
		if newlyCreated {
			created++
		}
	}

	return created, nil
}

func createMethodWithBestPluginCode(
	ctx context.Context,
	client shippingpb.ShippingsClient,
	methodName string,
	pluginCodes []string,
) (bool, error) {
	var createFailures []string
	for _, pluginCode := range pluginCodes {
		pluginCode = strings.TrimSpace(pluginCode)
		if pluginCode == "" {
			continue
		}

		req := &shippingpb.ShippingRequest{
			ShippingName:                   wrapperspb.String(methodName),
			ShippingCode:                   wrapperspb.String(pluginCode),
			ShippingStatus:                 wrapperspb.Bool(true),
			ShippingType:                   shippingpb.ShippingRequest_external_rate,
			ShippingMethodCalculationsType: shippingpb.ShippingRequest_none,
		}
		createResp, createErr := client.CreateAdminShippingMethods(ctx, req)
		if createErr != nil {
			createFailures = append(createFailures, fmt.Sprintf("%s grpc error: %v", pluginCode, createErr))
			continue
		}
		if createResp == nil {
			createFailures = append(createFailures, fmt.Sprintf("%s empty response", pluginCode))
			continue
		}
		if createResp.GetFailure() || !createResp.GetSuccess() {
			message := strings.TrimSpace(createResp.GetMessage())
			if isPluginUnavailableMessage(message) {
				createFailures = append(createFailures, fmt.Sprintf("%s plugin unavailable", pluginCode))
				continue
			}
			if isAlreadyExistsMessage(message) {
				return false, nil
			}
			createFailures = append(
				createFailures,
				fmt.Sprintf(
					`%s rejected (success=%t failure=%t code=%s message=%q)`,
					pluginCode,
					createResp.GetSuccess(),
					createResp.GetFailure(),
					strings.TrimSpace(createResp.GetCode()),
					message,
				),
			)
			continue
		}
		return true, nil
	}

	return false, fmt.Errorf("CreateAdminShippingMethods(%s) failed: %s", methodName, strings.Join(createFailures, " | "))
}

func ensureGenericPluginName(
	ctx context.Context,
	client shippingpb.ShippingsClient,
	existingItems []*shippingpb.ShippingRequest,
	pluginCodes []string,
	targetName string,
	isPluginMethod func(item *shippingpb.ShippingRequest, pluginCodes []string) bool,
) (bool, error) {
	if client == nil || len(existingItems) == 0 || strings.TrimSpace(targetName) == "" {
		return false, nil
	}
	targetNormalized := normalizePluginMethodName(targetName)
	for _, item := range existingItems {
		if item == nil || item.GetShippingName() == nil {
			continue
		}
		if normalizePluginMethodName(item.GetShippingName().GetValue()) == targetNormalized {
			return false, nil
		}
	}

	for _, item := range existingItems {
		if item == nil || !isPluginMethod(item, pluginCodes) {
			continue
		}
		shippingID := ""
		if item.GetShippingId() != nil {
			shippingID = strings.TrimSpace(item.GetShippingId().GetValue())
		}
		if shippingID == "" {
			continue
		}

		req := &shippingpb.ShippingRequest{
			ShippingId:   wrapperspb.String(shippingID),
			ShippingName: wrapperspb.String(targetName),
		}
		resp, err := client.UpdateAdminShippingMethods(ctx, req)
		if err != nil {
			return false, fmt.Errorf("UpdateAdminShippingMethods(%s) grpc error: %w", shippingID, err)
		}
		if resp == nil {
			return false, fmt.Errorf("UpdateAdminShippingMethods(%s) empty response", shippingID)
		}
		if resp.GetFailure() || !resp.GetSuccess() {
			return false, fmt.Errorf(
				"UpdateAdminShippingMethods(%s) rejected (success=%t failure=%t code=%s message=%q)",
				shippingID,
				resp.GetSuccess(),
				resp.GetFailure(),
				strings.TrimSpace(resp.GetCode()),
				strings.TrimSpace(resp.GetMessage()),
			)
		}
		return true, nil
	}
	return false, nil
}

func discoverPluginCodes(ctx context.Context, conn *grpc.ClientConn, pluginDisplayName string) []string {
	if conn == nil {
		return nil
	}
	client := pluginspb.NewPluginsClient(conn)
	req := &pluginspb.PluginsRequest{
		PluginsrequestPluginType: pluginspb.PLUGINTYPE_SHIPPING,
	}
	resp, err := client.GetAllPlugins(ctx, req)
	if err != nil {
		log.Printf("plugins discovery failed: %v", err)
		return nil
	}
	if resp == nil || resp.GetFailure() {
		return nil
	}

	targetName := strings.ToLower(strings.TrimSpace(pluginDisplayName))
	type candidate struct {
		code     string
		priority int
	}
	candidates := make([]candidate, 0, len(resp.GetPlugins()))
	for _, plugin := range resp.GetPlugins() {
		if plugin == nil || !plugin.GetPluginInstalled() {
			continue
		}
		code := strings.TrimSpace(plugin.GetPluginCode())
		if code == "" {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(plugin.GetPluginName()))
		priority := 50
		if targetName != "" && strings.Contains(name, targetName) {
			priority = 10
		}
		candidates = append(candidates, candidate{code: code, priority: priority})
	}
	if len(candidates) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	out := make([]string, 0, len(candidates))
	for i := 0; i < len(candidates); i++ {
		best := i
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].priority < candidates[best].priority {
				best = j
			}
		}
		candidates[i], candidates[best] = candidates[best], candidates[i]
		code := strings.TrimSpace(candidates[i].code)
		key := strings.ToLower(code)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, code)
	}
	return out
}

func mergePluginCodes(groups ...[]string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0)
	for _, group := range groups {
		for _, code := range group {
			code = strings.TrimSpace(code)
			key := strings.ToLower(code)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, code)
		}
	}
	return out
}

// pluginMethodMatchesByNameOrCode is a reusable matcher for legacy/plugin-owned methods.
func pluginMethodMatchesByNameOrCode(
	item *shippingpb.ShippingRequest,
	pluginDisplayName string,
	pluginCodes []string,
) bool {
	if item == nil {
		return false
	}
	name := ""
	if item.GetShippingName() != nil {
		name = normalizePluginMethodName(item.GetShippingName().GetValue())
	}
	target := normalizePluginMethodName(pluginDisplayName)
	if target != "" && strings.Contains(name, target) {
		return true
	}

	code := ""
	if item.GetShippingCode() != nil {
		code = strings.ToLower(strings.TrimSpace(item.GetShippingCode().GetValue()))
	}
	if code == "" {
		return false
	}
	for _, pluginCode := range pluginCodes {
		if code == strings.ToLower(strings.TrimSpace(pluginCode)) {
			return true
		}
	}
	return false
}

func normalizePluginMethodName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return strings.ToLower(value)
}

func isPluginUnavailableMessage(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(message, "plugin not available")
}

func isAlreadyExistsMessage(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(message, "already exists") ||
		strings.Contains(message, "duplicate") ||
		strings.Contains(message, "already exist")
}
