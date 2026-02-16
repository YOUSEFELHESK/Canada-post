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

type adminShippingMethod struct {
	Name        string
	ServiceCode string
}

var canadaPostAdminShippingMethods = []adminShippingMethod{
	// Keep a single generic plugin entry in admin shipping settings.
	{Name: "Canada Post", ServiceCode: "CANADA_POST_PLUGIN"},
}

// EnsureCanadaPostAdminShippingMethods makes sure the store has all configured Canada Post admin shipping methods.
// It is safe to call multiple times.
func EnsureCanadaPostAdminShippingMethods(ctx context.Context, grpcAddr string, storeID int64, accessToken string) (int, error) {
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

	conn, err := grpc.DialContext(ctx, grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return 0, fmt.Errorf("failed to connect to shipping grpc: %w", err)
	}
	defer conn.Close()

	client := shippingpb.NewShippingsClient(conn)
	attempts := buildShippingAuthAttempts(accessToken)
	if len(attempts) == 0 {
		return 0, errors.New("no valid authorization attempts for shipping grpc")
	}

	var failures []string
	for _, attempt := range attempts {
		created, attemptErr := ensureWithAttempt(ctx, conn, client, storeID, attempt)
		if attemptErr == nil {
			return created, nil
		}
		failures = append(failures, fmt.Sprintf("%s: %v", attempt.label, attemptErr))
	}

	return 0, fmt.Errorf("failed to sync admin shipping methods: %s", strings.Join(failures, " | "))
}

type shippingAuthAttempt struct {
	label         string
	authorization string
	forceAuth     bool
}

func buildShippingAuthAttempts(token string) []shippingAuthAttempt {
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

	candidates := []shippingAuthAttempt{
		{label: "bearer+x-force-auth", authorization: bearer, forceAuth: true},
		{label: "bearer", authorization: bearer, forceAuth: false},
		{label: "raw+x-force-auth", authorization: raw, forceAuth: true},
		{label: "raw", authorization: raw, forceAuth: false},
	}

	seen := make(map[string]bool)
	filtered := make([]shippingAuthAttempt, 0, len(candidates))
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

func ensureWithAttempt(ctx context.Context, conn *grpc.ClientConn, client shippingpb.ShippingsClient, storeID int64, attempt shippingAuthAttempt) (int, error) {
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
		return 0, fmt.Errorf("GetAdminShippingMethods rejected (code=%s message=%q)", strings.TrimSpace(existingResp.GetCode()), strings.TrimSpace(existingResp.GetMessage()))
	}

	existingItems := existingResp.GetShippingInfo()
	existingNames := make(map[string]bool)
	for _, item := range existingItems {
		if item == nil || item.GetShippingName() == nil {
			continue
		}
		name := normalizeShippingMethodName(item.GetShippingName().GetValue())
		if name == "" {
			continue
		}
		existingNames[name] = true
	}

	pluginCodes := resolvePluginCodes(callCtx, conn)
	if len(pluginCodes) == 0 {
		pluginCodes = []string{"shipstation", "canada_post", "canadapost", "Canada Post", "CANADA_POST_LIVE"}
	}

	if updated, err := ensureGenericCanadaPostName(callCtx, client, existingItems, pluginCodes); err != nil {
		return 0, err
	} else if updated {
		existingNames[normalizeShippingMethodName("Canada Post")] = true
	}

	created := 0
	for _, method := range canadaPostAdminShippingMethods {
		name := normalizeShippingMethodName(method.Name)
		if name == "" {
			continue
		}
		if existingNames[name] {
			continue
		}

		var createFailures []string
		createdForMethod := false
		newlyCreatedForMethod := false
		for _, pluginCode := range pluginCodes {
			pluginCode = strings.TrimSpace(pluginCode)
			if pluginCode == "" {
				continue
			}
			req := &shippingpb.ShippingRequest{
				ShippingName:                   wrapperspb.String(method.Name),
				ShippingCode:                   wrapperspb.String(pluginCode),
				ShippingStatus:                 wrapperspb.Bool(true),
				ShippingType:                   shippingpb.ShippingRequest_external_rate,
				ShippingMethodCalculationsType: shippingpb.ShippingRequest_none,
			}
			createResp, createErr := client.CreateAdminShippingMethods(callCtx, req)
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
				if isPluginNotAvailableMessage(message) {
					createFailures = append(createFailures, fmt.Sprintf("%s plugin unavailable", pluginCode))
					continue
				}
				if isAlreadyExistsShippingMethodMessage(message) {
					createdForMethod = true
					break
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
			createdForMethod = true
			newlyCreatedForMethod = true
			break
		}
		if !createdForMethod {
			return created, fmt.Errorf("CreateAdminShippingMethods(%s) failed: %s", method.ServiceCode, strings.Join(createFailures, " | "))
		}

		existingNames[name] = true
		if newlyCreatedForMethod {
			created++
		}
	}

	return created, nil
}

func ensureGenericCanadaPostName(
	ctx context.Context,
	client shippingpb.ShippingsClient,
	existingItems []*shippingpb.ShippingRequest,
	pluginCodes []string,
) (bool, error) {
	if client == nil || len(existingItems) == 0 {
		return false, nil
	}
	targetName := "Canada Post"
	targetNormalized := normalizeShippingMethodName(targetName)

	for _, item := range existingItems {
		if item == nil || item.GetShippingName() == nil {
			continue
		}
		if normalizeShippingMethodName(item.GetShippingName().GetValue()) == targetNormalized {
			return false, nil
		}
	}

	for _, item := range existingItems {
		if item == nil || item.GetShippingName() == nil {
			continue
		}
		if !isCanadaPostLikeMethod(item, pluginCodes) {
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

func isCanadaPostLikeMethod(item *shippingpb.ShippingRequest, pluginCodes []string) bool {
	if item == nil {
		return false
	}
	name := ""
	if item.GetShippingName() != nil {
		name = normalizeShippingMethodName(item.GetShippingName().GetValue())
	}
	if strings.HasPrefix(name, "canada post") {
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

func resolvePluginCodes(ctx context.Context, conn *grpc.ClientConn) []string {
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

	type candidate struct {
		code     string
		priority int
	}
	candidates := make([]candidate, 0)
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
		if strings.Contains(name, "canada") || strings.Contains(name, "post") {
			priority = 10
		}
		if strings.EqualFold(code, "shipstation") {
			priority = 20
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

func normalizeShippingMethodName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return strings.ToLower(value)
}

func isPluginNotAvailableMessage(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	if message == "" {
		return false
	}
	return strings.Contains(message, "plugin not available")
}

func isAlreadyExistsShippingMethodMessage(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	if message == "" {
		return false
	}
	return strings.Contains(message, "already exists") ||
		strings.Contains(message, "duplicate") ||
		strings.Contains(message, "already exist")
}
