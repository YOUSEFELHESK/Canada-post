package httpapi

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"text/template"
	"time"

	"golang.org/x/oauth2"
	"lexmodo-plugin/database"
	"lexmodo-plugin/service"
)

func (a *App) home(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "Lexmodo Plugin Home")
}

func (a *App) callkey(w http.ResponseWriter, r *http.Request) {
	pubKeyPath := "/home/lexmodo/.ssh/id_rsa.pub"
	pubKey, err := os.ReadFile(pubKeyPath)
	if err != nil {
		http.Error(w, "Could not read public key", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write(pubKey)
}

func (a *App) callback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	sessionToken := r.URL.Query().Get("session_token")

	fmt.Println("code:", code)
	fmt.Println("state:", state)
	fmt.Println("SESSION TOKEN:", sessionToken)

	if sessionToken != "" && code == "" {
		claims, err := service.Verify(sessionToken)
		if err != nil {
			log.Println("❌ JWT verification error:", err)
			http.Error(w, "Invalid session token", http.StatusUnauthorized)
			return
		}

		log.Printf("✅ JWT Issuer: %s | StoreID: %d\n", claims.Iss, claims.StoreID)
		storeID := claims.StoreID

		access := a.Store.GetAccessToken(storeID)
		refresh := a.Store.GetRefreshToken(storeID)
		fmt.Println("AccessToken:", access)
		fmt.Println("RefreshToken:", refresh)

		if strings.TrimSpace(access) != "" {
			settings, err := a.Store.LoadShippingSettings(int64(storeID))
			if err != nil {
				http.Error(w, "Failed to load settings: "+err.Error(), http.StatusInternalServerError)
				return
			}
			if settings.EnabledServices == nil {
				settings.EnabledServices = make(map[string]bool)
			}
			renderSettingsPage(w, settingsPageData{
				ClientID:      int64(storeID),
				AccountNumber: settings.AccountNumber,
				Services:      serviceOptions,
				Enabled:       settings.EnabledServices,
			})
			return
		}

		newState := service.GenerateState()
		a.Store.SaveState(storeID, newState)
		fmt.Println("Generated new state:", newState)

		authURL := a.OAuth.AuthCodeURL(
			newState,
			oauth2.AccessTypeOffline,
			oauth2.SetAuthURLParam("plugin_code", "shipstation"),
			oauth2.SetAuthURLParam("scope", "shippings_write orders_write"))

		log.Println("AuthURL:", authURL)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>Authorize</title>
  <style>
    body { font-family: "Source Sans 3", "Segoe UI", "Helvetica Neue", Arial, sans-serif; margin: 0; background: #f3f5f8; color: #1d1f23; }
    .wrap { max-width: 560px; margin: 60px auto; padding: 0 20px; }
    .card { background: #fff; border: 1px solid #e5e7eb; border-radius: 10px; padding: 24px; box-shadow: 0 12px 30px rgba(15,23,42,.08); }
    h1 { margin: 0 0 8px; font-size: 18px; }
    p { margin: 0 0 16px; color: #6b7280; }
    a.button { display: inline-block; background: #2563eb; color: #fff; text-decoration: none; padding: 10px 16px; border-radius: 6px; font-weight: 600; }
  </style>
</head>
<body>
  <div class="wrap">
    <div class="card">
      <h1>Continue Authorization</h1>
      <p>We need to open the Lexmodo authorization page to continue.</p>
      <a class="button" href="%s" target="_top" rel="noopener">Continue</a>
    </div>
  </div>
  <script>window.top.location.href = "%s";</script>
</body>
</html>`, authURL, authURL)
		return
	}

	if code != "" && state != "" {
		storeID := a.Store.GetClientIDFromState(state)
		if storeID == 0 {
			http.Error(w, "Invalid or expired state", http.StatusBadRequest)
			return
		}

		tokenResp, err := service.ExchangeCodeForToken(a.OAuth, code)
		if err != nil {
			http.Error(w, "Token exchange failed: "+err.Error(), http.StatusUnauthorized)
			return
		}

		fmt.Printf("✅ Token exchange result: %+v\n", tokenResp)

		a.Store.DeleteState(storeID)

		ok := a.Store.InsertToken(storeID, tokenResp.AccessToken, tokenResp.RefreshToken, tokenResp.Expiry)
		if !ok {
			http.Error(w, "Failed to save tokens", http.StatusInternalServerError)
			return
		}

		fmt.Println("✅ AccessToken:", tokenResp.AccessToken)
		fmt.Println("✅ RefreshToken:", service.GetStringValue(tokenResp.RefreshToken))
		fmt.Println("⏰ Expires In:", int(time.Until(tokenResp.Expiry).Seconds()))

		if storeID == 0 {
			settings, err := a.Store.LoadShippingSettings(1)
			if err != nil {
				http.Error(w, "Failed to load settings: "+err.Error(), http.StatusInternalServerError)
				return
			}
			if settings.EnabledServices == nil {
				settings.EnabledServices = make(map[string]bool)
			}
			renderSettingsPage(w, settingsPageData{
				ClientID:      1,
				AccountNumber: settings.AccountNumber,
				Services:      serviceOptions,
				Enabled:       settings.EnabledServices,
			})
			return
		}
		http.Redirect(w, r, "/settings?client_id="+strconv.FormatInt(int64(storeID), 10), http.StatusSeeOther)
		return
	}

	http.Error(w, "Invalid request", http.StatusBadRequest)
}

func (a *App) labelHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	labelID := strings.TrimPrefix(r.URL.Path, "/labels/")
	labelID = strings.TrimSpace(labelID)
	labelID = strings.Trim(labelID, "/")
	if labelID == "" || strings.Contains(labelID, "/") {
		http.NotFound(w, r)
		return
	}

	labelID = path.Base(labelID)
	data, err := a.Store.LoadLabelPDF(labelID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", "inline; filename=\"label.pdf\"")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(data); err != nil {
		log.Println("failed to write label response:", err)
	}
}

type serviceOption struct {
	ID    string
	Label string
}

var serviceOptions = []serviceOption{
	{ID: "DOM.RP", Label: "Regular Parcel (Domestic)"},
	{ID: "DOM.EP", Label: "Expedited Parcel (Domestic)"},
	{ID: "DOM.XP", Label: "Xpresspost (Domestic)"},
	{ID: "DOM.PC", Label: "Priority (Domestic)"},
	{ID: "USA.EP", Label: "Expedited Parcel (USA)"},
	{ID: "USA.XP", Label: "Xpresspost (USA)"},
	{ID: "INT.XP", Label: "Xpresspost (International)"},
}

type settingsPageData struct {
	ClientID      int64
	AccountNumber string
	Services      []serviceOption
	Enabled       map[string]bool
	Message       string
	FromDate      string
	ToDate        string
	Labels        []database.LabelRecord
	ActiveTab     string
}

func (a *App) settingsHandler(w http.ResponseWriter, r *http.Request) {
	clientID := parseClientID(r.URL.Query().Get("client_id"))
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		accountNumber := strings.TrimSpace(r.FormValue("account_number"))
		if accountNumber == "" {
			http.Error(w, "account number is required", http.StatusBadRequest)
			return
		}
		enabled := r.Form["services"]
		if err := a.Store.SaveShippingSettings(clientID, accountNumber, enabled); err != nil {
			log.Println("failed to save settings:", err)
			http.Error(w, "failed to save settings", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/settings?client_id="+strconv.FormatInt(clientID, 10)+"&saved=1", http.StatusSeeOther)
		return
	}

	settings, err := a.Store.LoadShippingSettings(clientID)
	if err != nil {
		log.Println("failed to load settings:", err)
		http.Error(w, "failed to load settings", http.StatusInternalServerError)
		return
	}
	if settings.EnabledServices == nil {
		settings.EnabledServices = make(map[string]bool)
	}

	fromDate := strings.TrimSpace(r.URL.Query().Get("from"))
	toDate := strings.TrimSpace(r.URL.Query().Get("to"))
	activeTab := strings.TrimSpace(r.URL.Query().Get("tab"))
	if activeTab == "" && (fromDate != "" || toDate != "") {
		activeTab = "labels"
	}
	if activeTab == "" {
		activeTab = "settings"
	}
	limit := 10
	if strings.TrimSpace(fromDate) != "" || strings.TrimSpace(toDate) != "" {
		limit = 200
	}
	labels, err := a.Store.LoadLabelRecords(fromDate, toDate, limit)
	if err != nil {
		log.Println("failed to load label records:", err)
		http.Error(w, "failed to load labels", http.StatusInternalServerError)
		return
	}

	data := settingsPageData{
		ClientID:      clientID,
		AccountNumber: settings.AccountNumber,
		Services:      serviceOptions,
		Enabled:       settings.EnabledServices,
		FromDate:      fromDate,
		ToDate:        toDate,
		Labels:        labels,
		ActiveTab:     activeTab,
	}
	if r.URL.Query().Get("saved") == "1" {
		data.Message = "Settings saved."
	}

	renderSettingsPage(w, data)
}

func renderSettingsPage(w http.ResponseWriter, data settingsPageData) {
	tmpl := template.Must(template.New("settings").Parse(settingsHTML))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		log.Println("failed to render settings:", err)
	}
}

func parseClientID(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 1
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 1
	}
	return id
}

const settingsHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>Canada Post Settings</title>
  <style>
    :root {
      --bg: #f3f5f8;
      --card: #ffffff;
      --text: #1d1f23;
      --muted: #6b7280;
      --border: #e5e7eb;
      --accent: #2563eb;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font-family: "Source Sans 3", "Segoe UI", "Helvetica Neue", Arial, sans-serif;
      background: var(--bg);
      color: var(--text);
    }
    .wrap {
      max-width: 760px;
      margin: 40px auto;
      padding: 0 20px;
    }
    .card {
      background: var(--card);
      border: 1px solid var(--border);
      border-radius: 10px;
      box-shadow: 0 12px 30px rgba(15, 23, 42, 0.08);
      padding: 28px;
    }
    h1 {
      font-size: 20px;
      margin: 0 0 18px;
    }
    label {
      font-size: 13px;
      font-weight: 600;
      display: block;
      margin-bottom: 6px;
    }
    input[type="text"], input[type="date"] {
      width: 100%;
      border: 1px solid var(--border);
      border-radius: 6px;
      padding: 10px 12px;
      font-size: 14px;
      margin-bottom: 16px;
    }
    .tabs {
      display: flex;
      gap: 12px;
      margin-bottom: 16px;
    }
    .tab {
      background: transparent;
      border: 0;
      padding: 10px 14px;
      border-bottom: 2px solid transparent;
      font-weight: 600;
      cursor: pointer;
    }
    .tab.active {
      border-color: var(--accent);
      color: var(--accent);
    }
    .panel { display: none; }
    .panel.active { display: block; }
    .list {
      display: flex;
      flex-direction: column;
      gap: 12px;
    }
    .row {
      display: flex;
      align-items: center;
      gap: 10px;
      font-size: 14px;
      font-weight: 600;
    }
    .actions {
      margin-top: 20px;
      display: flex;
      align-items: center;
      gap: 12px;
    }
    button {
      border: 0;
      background: var(--accent);
      color: white;
      padding: 10px 16px;
      border-radius: 6px;
      font-weight: 600;
      cursor: pointer;
    }
    .hint {
      font-size: 12px;
      color: var(--muted);
    }
    .message {
      margin-top: 12px;
      color: #0f766e;
      font-size: 13px;
    }
  </style>
</head>
<body>
  <div class="wrap">
    <div class="tabs" role="tablist">
      <button class="tab {{if eq .ActiveTab "settings"}}active{{end}}" data-target="settings-panel" type="button">Canada Post Account Settings</button>
      <button class="tab {{if eq .ActiveTab "labels"}}active{{end}}" data-target="labels-panel" type="button">Created Labels</button>
    </div>

    <div class="card panel {{if eq .ActiveTab "settings"}}active{{end}}" id="settings-panel">
      <h1>Canada Post Account Settings</h1>
      <form method="post" action="/settings?client_id={{.ClientID}}">
        <label for="account_number">Canada Post Customer Number</label>
        <input id="account_number" name="account_number" type="text" value="{{.AccountNumber}}" placeholder="Enter customer number" required>
        <div class="list">
          {{range .Services}}
          <label class="row">
            <input type="checkbox" name="services" value="{{.ID}}" {{if index $.Enabled .ID}}checked{{end}}>
            <span>{{.Label}}</span>
          </label>
          {{end}}
        </div>
        <div class="actions">
          <button type="submit">Save Settings</button>
          <span class="hint">Client ID: {{.ClientID}}</span>
        </div>
        {{if .Message}}<div class="message">{{.Message}}</div>{{end}}
      </form>
    </div>

    <div class="card panel {{if eq .ActiveTab "labels"}}active{{end}}" id="labels-panel" style="margin-top:20px;">
      <h1>Created Labels</h1>
      <form method="get" action="/settings" class="actions" style="gap:8px;">
        <input type="hidden" name="client_id" value="{{.ClientID}}">
        <input type="hidden" name="tab" value="labels">
        <label style="margin:0;font-weight:600;">From</label>
        <input type="date" name="from" value="{{.FromDate}}">
        <label style="margin:0;font-weight:600;">To</label>
        <input type="date" name="to" value="{{.ToDate}}">
        <button type="submit">Filter</button>
      </form>
      <div style="overflow:auto; margin-top:12px;">
        <table style="width:100%; border-collapse: collapse; font-size:14px;">
          <thead>
            <tr>
              <th style="text-align:left; border-bottom:1px solid #e5e7eb; padding:8px;">Shipment ID</th>
              <th style="text-align:left; border-bottom:1px solid #e5e7eb; padding:8px;">Service Code</th>
              <th style="text-align:left; border-bottom:1px solid #e5e7eb; padding:8px;">Weight (lbs)</th>
              <th style="text-align:left; border-bottom:1px solid #e5e7eb; padding:8px;">Tracking #</th>
              <th style="text-align:left; border-bottom:1px solid #e5e7eb; padding:8px;">Created At</th>
              <th style="text-align:left; border-bottom:1px solid #e5e7eb; padding:8px;">Label</th>
            </tr>
          </thead>
          <tbody>
            {{if .Labels}}
              {{range .Labels}}
              <tr>
                <td style="padding:8px; border-bottom:1px solid #f1f5f9;">{{.ShipmentID}}</td>
                <td style="padding:8px; border-bottom:1px solid #f1f5f9;">{{.ServiceCode}}</td>
                <td style="padding:8px; border-bottom:1px solid #f1f5f9;">{{printf "%.2f" .Weight}}</td>
                <td style="padding:8px; border-bottom:1px solid #f1f5f9;">{{.TrackingNumber}}</td>
                <td style="padding:8px; border-bottom:1px solid #f1f5f9;">
                  {{.CreatedAt}}
                </td>
                <td style="padding:8px; border-bottom:1px solid #f1f5f9;">
                  <a href="/labels/{{.ID}}" target="_blank" rel="noopener">PDF</a>
                </td>
              </tr>
              {{end}}
            {{else}}
              <tr>
                <td colspan="6" style="padding:10px; color:#6b7280;">No labels found.</td>
              </tr>
            {{end}}
          </tbody>
        </table>
      </div>
    </div>
  </div>
  <script>
    const tabs = document.querySelectorAll('.tab');
    const panels = document.querySelectorAll('.panel');
    tabs.forEach((tab) => {
      tab.addEventListener('click', () => {
        tabs.forEach((t) => t.classList.remove('active'));
        panels.forEach((p) => p.classList.remove('active'));
        tab.classList.add('active');
        const target = document.getElementById(tab.dataset.target);
        if (target) target.classList.add('active');
      });
    });
  </script>
</body>
</html>`
