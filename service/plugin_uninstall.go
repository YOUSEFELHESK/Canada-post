package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	pluginspb "bitbucket.org/lexmodo/proto/plugins"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// UninstallPlugin calls Lexmodo Plugins.UnInstallPlugin for the given store/plugin.
func UninstallPlugin(ctx context.Context, grpcAddr string, storeID int64, accessToken string, pluginCode string) error {
	if strings.TrimSpace(grpcAddr) == "" {
		return errors.New("plugins grpc address is required")
	}
	if storeID <= 0 {
		return errors.New("store id is required")
	}
	if strings.TrimSpace(accessToken) == "" {
		return errors.New("access token is required")
	}
	pluginCode = strings.TrimSpace(pluginCode)
	if pluginCode == "" {
		return errors.New("plugin code is required")
	}

	conn, err := grpc.DialContext(ctx, grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("failed to connect to plugins grpc: %w", err)
	}
	defer conn.Close()

	request := &pluginspb.PluginsRequest{
		PluginCode: wrapperspb.String(pluginCode),
	}
	client := pluginspb.NewPluginsClient(conn)
	attempts := buildUninstallAuthAttempts(accessToken)
	if len(attempts) == 0 {
		return errors.New("no valid authorization attempts for uninstall grpc")
	}

	var failures []string
	for _, attempt := range attempts {
		md := metadata.New(map[string]string{
			"authorization": attempt.authorization,
			"x-client-id":   fmt.Sprintf("%d", storeID),
		})
		if attempt.forceAuth {
			md.Set("x-force-auth", "true")
		}
		callCtx := metadata.NewOutgoingContext(ctx, md)

		resp, err := client.UnInstallPlugin(callCtx, request)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s grpc error: %v", attempt.label, err))
			continue
		}
		if resp == nil {
			failures = append(failures, fmt.Sprintf("%s empty response", attempt.label))
			continue
		}
		if resp.GetFailure() || !resp.GetSuccess() {
			failures = append(
				failures,
				fmt.Sprintf(
					`%s rejected (success=%t failure=%t code=%s message=%q)`,
					attempt.label,
					resp.GetSuccess(),
					resp.GetFailure(),
					strings.TrimSpace(resp.GetCode()),
					strings.TrimSpace(resp.GetMessage()),
				),
			)
			continue
		}
		return nil
	}

	return fmt.Errorf("gRPC UnInstallPlugin failed: %s", strings.Join(failures, " | "))
}

type uninstallAuthAttempt struct {
	label         string
	authorization string
	forceAuth     bool
}

func buildUninstallAuthAttempts(token string) []uninstallAuthAttempt {
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

	candidates := []uninstallAuthAttempt{
		{label: "bearer+x-force-auth", authorization: bearer, forceAuth: true},
		{label: "bearer", authorization: bearer, forceAuth: false},
		{label: "raw+x-force-auth", authorization: raw, forceAuth: true},
		{label: "raw", authorization: raw, forceAuth: false},
	}

	seen := make(map[string]bool)
	filtered := make([]uninstallAuthAttempt, 0, len(candidates))
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
