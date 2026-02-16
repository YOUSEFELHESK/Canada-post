package httpapi

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"lexmodo-plugin/service"
)

const uninstallPluginCode = "shipstation"

// HandleUninstall uninstalls the plugin remotely, then removes local plugin data.
func (a *App) HandleUninstall(w http.ResponseWriter, r *http.Request) {
	log.Printf("uninstall HTTP request received: method=%s path=%s query=%s", r.Method, r.URL.Path, r.URL.RawQuery)
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	sessionToken := strings.TrimSpace(r.FormValue("session_token"))
	if sessionToken == "" {
		sessionToken = strings.TrimSpace(r.URL.Query().Get("session_token"))
	}
	if sessionToken == "" {
		http.Error(w, "session_token required", http.StatusBadRequest)
		return
	}

	clientID, err := resolveUninstallClientID(r, sessionToken)
	if err != nil || clientID <= 0 {
		http.Error(w, "invalid client_id", http.StatusBadRequest)
		return
	}

	if !a.consumeSessionToken(clientID, sessionToken) && !isValidJWTSessionForClient(clientID, sessionToken) {
		http.Error(w, "invalid or expired session token", http.StatusUnauthorized)
		return
	}

	accessToken := strings.TrimSpace(a.Store.GetAccessToken(int(clientID)))
	targetGRPCAddr := strings.TrimSpace(a.Config.OrdersGRPCAddr)
	if targetGRPCAddr == "" {
		targetGRPCAddr = strings.TrimSpace(a.Config.GRPCAddr)
	}
	if targetGRPCAddr == "" {
		http.Error(w, "grpc address not configured", http.StatusInternalServerError)
		return
	}

	log.Printf("uninstall request: store=%d grpc=%s plugin_code=%s", clientID, targetGRPCAddr, uninstallPluginCode)
	var remoteUninstallErrs []string
	for _, tokenCandidate := range []struct {
		name  string
		token string
	}{
		{name: "access_token", token: accessToken},
		{name: "session_token", token: sessionToken},
	} {
		token := strings.TrimSpace(tokenCandidate.token)
		if token == "" {
			continue
		}
		if err := service.UninstallPlugin(r.Context(), targetGRPCAddr, clientID, token, uninstallPluginCode); err == nil {
			log.Printf("remote uninstall succeeded for store %d using %s", clientID, tokenCandidate.name)
			remoteUninstallErrs = nil
			break
		} else {
			remoteUninstallErrs = append(remoteUninstallErrs, fmt.Sprintf("%s: %v", tokenCandidate.name, err))
		}
	}

	if len(remoteUninstallErrs) > 0 {
		// Keep local cleanup path functional even if backend auth rejects remote uninstall.
		log.Printf("remote uninstall failed for store %d: %s", clientID, strings.Join(remoteUninstallErrs, " | "))
	}

	if err := a.Store.CleanupPluginData(clientID); err != nil {
		log.Printf("cleanup failed for store %d: %v", clientID, err)
		http.Error(w, fmt.Sprintf("cleanup failed: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("plugin uninstalled completely for store %d", clientID)
	a.writeUninstallSuccessPage(w)
}

func resolveUninstallClientID(r *http.Request, sessionToken string) (int64, error) {
	clientIDValue := strings.TrimSpace(r.URL.Query().Get("client_id"))
	if clientIDValue == "" {
		clientIDValue = strings.TrimSpace(r.FormValue("client_id"))
	}
	if clientIDValue != "" {
		clientID, err := strconv.ParseInt(clientIDValue, 10, 64)
		if err != nil || clientID <= 0 {
			return 0, fmt.Errorf("invalid client_id")
		}
		return clientID, nil
	}

	claims, err := service.Verify(strings.TrimSpace(sessionToken))
	if err != nil || claims.StoreID <= 0 {
		return 0, fmt.Errorf("missing client_id and invalid session token")
	}
	return int64(claims.StoreID), nil
}

func isValidJWTSessionForClient(clientID int64, token string) bool {
	claims, err := service.Verify(strings.TrimSpace(token))
	if err != nil {
		return false
	}
	return int64(claims.StoreID) == clientID
}

func (a *App) writeUninstallSuccessPage(w http.ResponseWriter) {
	redirectURL := a.adminPluginsURL()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>Plugin Uninstalled</title>
  <style>
    body { font-family: "Space Grotesk", "Segoe UI", sans-serif; margin: 0; background: #f3f5f8; color: #14171f; }
    .wrap { max-width: 560px; margin: 60px auto; padding: 0 20px; }
    .card { background: #fff; border: 1px solid #e5e7eb; border-radius: 12px; padding: 24px; box-shadow: 0 12px 30px rgba(15,23,42,.08); }
    h1 { margin: 0 0 8px; font-size: 22px; }
    p { margin: 0; color: #6b7280; }
  </style>
</head>
<body>
  <div class="wrap">
    <div class="card">
      <h1>Plugin Uninstalled</h1>
      <p>Canada Post plugin was removed successfully. Redirecting to plugins page...</p>
    </div>
  </div>
  <script>
    setTimeout(function() {
      window.top.location.href = %q;
    }, 2000);
  </script>
</body>
</html>`, redirectURL)
}

func (a *App) adminPluginsURL() string {
	const fallback = "https://devadmin.lexmodo.com/plugins"

	authURL := strings.TrimSpace(a.Config.AuthorizeURL)
	if authURL == "" {
		return fallback
	}

	parsed, err := url.Parse(authURL)
	if err != nil || strings.TrimSpace(parsed.Host) == "" {
		return fallback
	}
	if parsed.Scheme == "" {
		parsed.Scheme = "https"
	}
	parsed.Path = "/plugins"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}
