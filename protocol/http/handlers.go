package httpapi

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	"lexmodo-plugin/database"
	"lexmodo-plugin/service"

	"golang.org/x/oauth2"
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
			oneTime, err := a.createSessionToken(int64(storeID), 2*time.Minute)
			if err != nil {
				http.Error(w, "Failed to create session token", http.StatusInternalServerError)
				return
			}
			http.Redirect(w, r, "/settings?client_id="+strconv.FormatInt(int64(storeID), 10)+"&session_token="+url.QueryEscape(oneTime), http.StatusSeeOther)
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

		oneTime, err := a.createSessionToken(int64(storeID), 2*time.Minute)
		if err != nil {
			http.Error(w, "Failed to create session token", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/settings?client_id="+strconv.FormatInt(int64(storeID), 10)+"&session_token="+url.QueryEscape(oneTime), http.StatusSeeOther)
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
	labelID = strings.TrimSuffix(labelID, ".pdf")
	labelID = strings.TrimSpace(labelID)
	if labelID == "" || strings.Contains(labelID, "/") || strings.Contains(labelID, "\\") || strings.Contains(labelID, "..") {
		http.NotFound(w, r)
		return
	}

	storagePath := strings.TrimSpace(a.Config.LabelStoragePath)
	if storagePath == "" {
		storagePath = "files/labels"
	}

	filePath := filepath.Join(storagePath, labelID+".pdf")
	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "failed to read label", http.StatusInternalServerError)
		return
	}
	if info.IsDir() {
		http.NotFound(w, r)
		return
	}

	file, err := os.Open(filePath)
	if err != nil {
		http.Error(w, "failed to open label", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=\"%s.pdf\"", labelID))
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, file); err != nil {
		log.Println("failed to write label response:", err)
	}
}

type serviceOption struct {
	ID    string
	Label string
}

type currencyOption struct {
	Code  string
	Label string
}

var serviceOptions = []serviceOption{
	// Domestic (Canada)
	{ID: "DOM.RP", Label: "Regular Parcel (Domestic)"},
	{ID: "DOM.EP", Label: "Expedited Parcel (Domestic)"},
	{ID: "DOM.XP", Label: "Xpresspost (Domestic)"},
	{ID: "DOM.XP.CERT", Label: "Xpresspost Certified (Domestic)"},
	{ID: "DOM.PC", Label: "Priority (Domestic)"},
	{ID: "DOM.LIB", Label: "Library Materials (Domestic)"},

	// USA
	{ID: "USA.EP", Label: "Expedited Parcel USA"},
	{ID: "USA.SP.AIR", Label: "Small Packet USA Air"},
	{ID: "USA.TP", Label: "Tracked Packet – USA"},
	{ID: "USA.TP.LVM", Label: "Tracked Packet – USA (LVM)"},
	{ID: "USA.XP", Label: "Xpresspost USA"},

	// International
	{ID: "INT.XP", Label: "Xpresspost International"},
	{ID: "INT.IP.AIR", Label: "International Parcel Air"},
	{ID: "INT.IP.SURF", Label: "International Parcel Surface"},
	{ID: "INT.SP.AIR", Label: "Small Packet International Air"},
	{ID: "INT.SP.SURF", Label: "Small Packet International Surface"},
	{ID: "INT.TP", Label: "Tracked Packet – International"},
}

var currencyOptions = []currencyOption{
	{Code: "USD", Label: "USD"},
	{Code: "AFN", Label: "AFN"},
	{Code: "ALL", Label: "ALL"},
	{Code: "AMD", Label: "AMD"},
	{Code: "ANG", Label: "ANG"},
	{Code: "AOA", Label: "AOA"},
	{Code: "ARS", Label: "ARS"},
	{Code: "AUD", Label: "AUD"},
	{Code: "AWG", Label: "AWG"},
	{Code: "AZN", Label: "AZN"},
	{Code: "BAM", Label: "BAM"},
	{Code: "BBD", Label: "BBD"},
	{Code: "BDT", Label: "BDT"},
	{Code: "BGN", Label: "BGN"},
	{Code: "BHD", Label: "BHD"},
	{Code: "BIF", Label: "BIF"},
	{Code: "BMD", Label: "BMD"},
	{Code: "BND", Label: "BND"},
	{Code: "BOB", Label: "BOB"},
	{Code: "BRL", Label: "BRL"},
	{Code: "BSD", Label: "BSD"},
	{Code: "BTC", Label: "BTC"},
	{Code: "BTN", Label: "BTN"},
	{Code: "BWP", Label: "BWP"},
	{Code: "BYN", Label: "BYN"},
	{Code: "BZD", Label: "BZD"},
	{Code: "CAD", Label: "CAD"},
	{Code: "CDF", Label: "CDF"},
	{Code: "CHF", Label: "CHF"},
	{Code: "CLF", Label: "CLF"},
	{Code: "CLP", Label: "CLP"},
	{Code: "CNH", Label: "CNH"},
	{Code: "CNY", Label: "CNY"},
	{Code: "COP", Label: "COP"},
	{Code: "CRC", Label: "CRC"},
	{Code: "CUC", Label: "CUC"},
	{Code: "CUP", Label: "CUP"},
	{Code: "CVE", Label: "CVE"},
	{Code: "CZK", Label: "CZK"},
	{Code: "DJF", Label: "DJF"},
	{Code: "DKK", Label: "DKK"},
	{Code: "DOP", Label: "DOP"},
	{Code: "DZD", Label: "DZD"},
	{Code: "EGP", Label: "EGP"},
	{Code: "ERN", Label: "ERN"},
	{Code: "ETB", Label: "ETB"},
	{Code: "EUR", Label: "EUR"},
	{Code: "FJD", Label: "FJD"},
	{Code: "FKP", Label: "FKP"},
	{Code: "GBP", Label: "GBP"},
	{Code: "GEL", Label: "GEL"},
	{Code: "GGP", Label: "GGP"},
	{Code: "GHS", Label: "GHS"},
	{Code: "GIP", Label: "GIP"},
	{Code: "GMD", Label: "GMD"},
	{Code: "GNF", Label: "GNF"},
	{Code: "GTQ", Label: "GTQ"},
	{Code: "GYD", Label: "GYD"},
	{Code: "HKD", Label: "HKD"},
	{Code: "HNL", Label: "HNL"},
	{Code: "HRK", Label: "HRK"},
	{Code: "HTG", Label: "HTG"},
	{Code: "HUF", Label: "HUF"},
	{Code: "IDR", Label: "IDR"},
	{Code: "ILS", Label: "ILS"},
	{Code: "IMP", Label: "IMP"},
	{Code: "INR", Label: "INR"},
	{Code: "IQD", Label: "IQD"},
	{Code: "IRR", Label: "IRR"},
	{Code: "ISK", Label: "ISK"},
	{Code: "JEP", Label: "JEP"},
	{Code: "JMD", Label: "JMD"},
	{Code: "JOD", Label: "JOD"},
	{Code: "JPY", Label: "JPY"},
	{Code: "KES", Label: "KES"},
	{Code: "KGS", Label: "KGS"},
	{Code: "KHR", Label: "KHR"},
	{Code: "KMF", Label: "KMF"},
	{Code: "KPW", Label: "KPW"},
	{Code: "KRW", Label: "KRW"},
	{Code: "KWD", Label: "KWD"},
	{Code: "KYD", Label: "KYD"},
	{Code: "KZT", Label: "KZT"},
	{Code: "LAK", Label: "LAK"},
	{Code: "LBP", Label: "LBP"},
	{Code: "LKR", Label: "LKR"},
	{Code: "LRD", Label: "LRD"},
	{Code: "LSL", Label: "LSL"},
	{Code: "LYD", Label: "LYD"},
	{Code: "MAD", Label: "MAD"},
	{Code: "MDL", Label: "MDL"},
	{Code: "MGA", Label: "MGA"},
	{Code: "MKD", Label: "MKD"},
	{Code: "MMK", Label: "MMK"},
	{Code: "MNT", Label: "MNT"},
	{Code: "MOP", Label: "MOP"},
	{Code: "MRO", Label: "MRO"},
	{Code: "MRU", Label: "MRU"},
	{Code: "MUR", Label: "MUR"},
	{Code: "MVR", Label: "MVR"},
	{Code: "MWK", Label: "MWK"},
	{Code: "MXN", Label: "MXN"},
	{Code: "MYR", Label: "MYR"},
	{Code: "MZN", Label: "MZN"},
	{Code: "NAD", Label: "NAD"},
	{Code: "NGN", Label: "NGN"},
	{Code: "NIO", Label: "NIO"},
	{Code: "NOK", Label: "NOK"},
	{Code: "NPR", Label: "NPR"},
	{Code: "NZD", Label: "NZD"},
	{Code: "OMR", Label: "OMR"},
	{Code: "PAB", Label: "PAB"},
	{Code: "PEN", Label: "PEN"},
	{Code: "PGK", Label: "PGK"},
	{Code: "PHP", Label: "PHP"},
	{Code: "PKR", Label: "PKR"},
	{Code: "PLN", Label: "PLN"},
	{Code: "PYG", Label: "PYG"},
	{Code: "QAR", Label: "QAR"},
	{Code: "RON", Label: "RON"},
	{Code: "RSD", Label: "RSD"},
	{Code: "RUB", Label: "RUB"},
	{Code: "RWF", Label: "RWF"},
	{Code: "SAR", Label: "SAR"},
	{Code: "SBD", Label: "SBD"},
	{Code: "SCR", Label: "SCR"},
	{Code: "SDG", Label: "SDG"},
	{Code: "SEK", Label: "SEK"},
	{Code: "SGD", Label: "SGD"},
	{Code: "SHP", Label: "SHP"},
	{Code: "SLL", Label: "SLL"},
	{Code: "SOS", Label: "SOS"},
	{Code: "SRD", Label: "SRD"},
	{Code: "SSP", Label: "SSP"},
	{Code: "STD", Label: "STD"},
	{Code: "STN", Label: "STN"},
	{Code: "SVC", Label: "SVC"},
	{Code: "SYP", Label: "SYP"},
	{Code: "SZL", Label: "SZL"},
	{Code: "THB", Label: "THB"},
	{Code: "TJS", Label: "TJS"},
	{Code: "TMT", Label: "TMT"},
	{Code: "TND", Label: "TND"},
	{Code: "TOP", Label: "TOP"},
	{Code: "TRY", Label: "TRY"},
	{Code: "TTD", Label: "TTD"},
	{Code: "TWD", Label: "TWD"},
	{Code: "TZS", Label: "TZS"},
	{Code: "UAH", Label: "UAH"},
	{Code: "UGX", Label: "UGX"},
	{Code: "AED", Label: "AED"},
	{Code: "UYU", Label: "UYU"},
	{Code: "UZS", Label: "UZS"},
	{Code: "VEF", Label: "VEF"},
	{Code: "VND", Label: "VND"},
	{Code: "VUV", Label: "VUV"},
	{Code: "WST", Label: "WST"},
	{Code: "XAF", Label: "XAF"},
	{Code: "XAG", Label: "XAG"},
	{Code: "XAU", Label: "XAU"},
	{Code: "XCD", Label: "XCD"},
	{Code: "XDR", Label: "XDR"},
	{Code: "XOF", Label: "XOF"},
	{Code: "XPD", Label: "XPD"},
	{Code: "XPF", Label: "XPF"},
	{Code: "XPT", Label: "XPT"},
	{Code: "YER", Label: "YER"},
	{Code: "ZAR", Label: "ZAR"},
	{Code: "ZMW", Label: "ZMW"},
	{Code: "ZWL", Label: "ZWL"},
}

type settingsPageData struct {
	ClientID        int64
	AccountNumber   string
	Services        []serviceOption
	Enabled         map[string]bool
	CurrencyRates   []database.CurrencyRate
	Currencies      []currencyOption
	SessionToken    string
	Message         string
	CurrencyMessage string
	FromDate        string
	ToDate          string
	Labels          []database.LabelRecord
	ActiveTab       string
	Page            int
	PageSize        int
	HasNext         bool
	HasPrev         bool
}

func (a *App) settingsHandler(w http.ResponseWriter, r *http.Request) {
	clientID := parseClientID(r.URL.Query().Get("client_id"))
	if r.Method == http.MethodGet {
		if dest := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Dest"))); dest != "" && dest != "iframe" {
			http.Error(w, "settings must be opened inside the Lexmodo Plugin", http.StatusForbidden)
			return
		}
		if !a.allowEmbeddedFromAdmin(w, r) {
			return
		}
	}
	if clientID == 0 {
		http.Error(w, "client_id is required", http.StatusBadRequest)
		return
	}
	sessionToken := ""
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		sessionToken = strings.TrimSpace(r.FormValue("session_token"))
	} else {
		sessionToken = strings.TrimSpace(r.URL.Query().Get("session_token"))
	}
	if sessionToken == "" {
		a.redirectToOAuth(w, r, clientID)
		return
	}
	if r.Method == http.MethodPost {
		if !a.consumeSessionToken(clientID, sessionToken) {
			a.redirectToOAuth(w, r, clientID)
			return
		}
	} else {
		if !a.validateSessionToken(clientID, sessionToken) {
			a.redirectToOAuth(w, r, clientID)
			return
		}
	}
	nextToken := sessionToken
	if r.Method == http.MethodPost {
		accountNumber := strings.TrimSpace(r.FormValue("account_number"))
		formType := strings.TrimSpace(r.FormValue("form_type"))
		if formType == "currency" {
			code := strings.TrimSpace(r.FormValue("currency_code"))
			rateValue := strings.TrimSpace(r.FormValue("rate_to_cad"))
			log.Printf("currency form received: client_id=%d currency=%q rate_to_cad=%q", clientID, code, rateValue)
			if code == "" {
				http.Error(w, "currency code is required", http.StatusBadRequest)
				return
			}
			if rateValue == "" {
				http.Error(w, "rate_to_cad is required", http.StatusBadRequest)
				return
			}
			rateToCad, err := strconv.ParseFloat(rateValue, 64)
			if err != nil || rateToCad <= 0 {
				http.Error(w, "rate_to_cad must be a positive number", http.StatusBadRequest)
				return
			}
			if err := a.Store.SaveCurrencyRate(clientID, code, rateToCad); err != nil {
				log.Println("failed to save currency rate:", err)
				http.Error(w, "failed to save currency rate", http.StatusInternalServerError)
				return
			}
			log.Printf("currency rate saved: client_id=%d currency=%s rate_to_cad=%.6f", clientID, strings.ToUpper(code), rateToCad)
		} else {
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
		}
		var err error
		nextToken, err = a.createSessionToken(clientID, 2*time.Minute)
		if err != nil {
			http.Error(w, "failed to create session token", http.StatusInternalServerError)
			return
		}
		savedParam := "saved=1"
		if formType == "currency" {
			savedParam = "saved_currency=1"
		}
		http.Redirect(w, r, "/settings?client_id="+strconv.FormatInt(clientID, 10)+"&session_token="+url.QueryEscape(nextToken)+"&"+savedParam, http.StatusSeeOther)
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
	currencyRates, err := a.Store.LoadCurrencyRates(clientID)
	if err != nil {
		log.Println("failed to load currency rates:", err)
		http.Error(w, "failed to load currency rates", http.StatusInternalServerError)
		return
	}
	if len(currencyRates) == 0 {
		log.Printf("currency rates loaded: client_id=%d count=0", clientID)
	} else {
		for _, rate := range currencyRates {
			log.Printf("currency rates loaded: client_id=%d currency=%s rate_to_cad=%.6f updated_at=%s", clientID, rate.CurrencyCode, rate.RateToCad, rate.UpdatedAt.Format(time.RFC3339))
		}
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
	page := parsePage(r.URL.Query().Get("page"))
	pageSize := parsePageSize(r.URL.Query().Get("page_size"))
	offset := (page - 1) * pageSize
	labels, hasNext, err := a.Store.LoadLabelRecordsPage(fromDate, toDate, pageSize, offset)
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
		CurrencyRates: currencyRates,
		Currencies:    currencyOptions,
		SessionToken:  nextToken,
		FromDate:      fromDate,
		ToDate:        toDate,
		Labels:        labels,
		ActiveTab:     activeTab,
		Page:          page,
		PageSize:      pageSize,
		HasNext:       hasNext,
		HasPrev:       page > 1,
	}
	if r.URL.Query().Get("saved") == "1" {
		data.Message = "Settings saved."
	}
	if r.URL.Query().Get("saved_currency") == "1" {
		data.CurrencyMessage = "Currency rate saved."
	}

	renderSettingsPage(w, data)
}

func (a *App) allowEmbeddedFromAdmin(w http.ResponseWriter, r *http.Request) bool {
	ref := strings.TrimSpace(r.Header.Get("Referer"))
	log.Printf("🔒 Iframe check: referer=%s host=%s", ref, r.Host)
	if ref == "" {
		log.Println("✅ Empty referer, allowing")
		return true
	}

	refURL, err := url.Parse(ref)
	if err != nil {
		log.Printf("❌ Invalid referer URL: %v", err)
		http.Error(w, "settings must be opened inside the Lexmodo Plugin", http.StatusForbidden)
		return false
	}

	if reqHost := strings.TrimSpace(r.Host); reqHost != "" && strings.EqualFold(refURL.Host, reqHost) {
		log.Printf("✅ Same-host access: %s", refURL.Host)
		return true
	}

	allowedHost := ""
	if authURL := strings.TrimSpace(a.Config.AuthorizeURL); authURL != "" {
		if u, err := url.Parse(authURL); err == nil {
			allowedHost = u.Host
		}
	}

	if allowedHost != "" && strings.EqualFold(refURL.Host, allowedHost) {
		log.Printf("✅ Allowed domain: %s", refURL.Host)
		return true
	}

	log.Printf("❌ Blocked: referer=%s not in allowed list", refURL.Host)
	http.Error(w, "settings must be opened inside the Lexmodo Plugin", http.StatusForbidden)
	return false
}

func (a *App) redirectToOAuth(w http.ResponseWriter, _ *http.Request, clientID int64) {
	if clientID == 0 || a == nil || a.OAuth == nil || a.Store == nil {
		http.Error(w, "invalid session_token", http.StatusForbidden)
		return
	}
	newState := service.GenerateState()
	a.Store.SaveState(int(clientID), newState)

	authURL := a.OAuth.AuthCodeURL(
		newState,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("plugin_code", "shipstation"),
		oauth2.SetAuthURLParam("scope", "shippings_write orders_write"),
	)

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
      <p>Your session expired. We need to open the Lexmodo authorization page to continue.</p>
      <a class="button" href="%s" target="_top" rel="noopener">Continue</a>
    </div>
  </div>
  <script>window.top.location.href = "%s";</script>
</body>
</html>`, authURL, authURL)
}

func renderSettingsPage(w http.ResponseWriter, data settingsPageData) {
	tmpl := template.Must(template.New("settings").Funcs(template.FuncMap{
		"inc": func(value int) int {
			return value + 1
		},
		"dec": func(value int) int {
			if value <= 1 {
				return 1
			}
			return value - 1
		},
		"div100": func(value int64) float64 {
			return float64(value) / 100.0
		},
	}).Parse(settingsHTML))
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

func parsePage(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 1
	}
	page, err := strconv.Atoi(value)
	if err != nil || page < 1 {
		return 1
	}
	return page
}

func parsePageSize(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 15
	}
	size, err := strconv.Atoi(value)
	if err != nil || size < 5 || size > 200 {
		return 15
	}
	return size
}

const settingsHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>Canada Post Control Center</title>
  <style>
    :root {
      --bg: #eff3f7;
      --card: #ffffff;
      --text: #14171f;
      --muted: #6b7280;
      --border: #dde3ea;
      --accent: #1f4fd7;
      --accent-soft: rgba(31, 79, 215, 0.12);
      --shadow: 0 20px 50px rgba(30, 41, 59, 0.12);
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font-family: "Space Grotesk", "SF Pro Text", "Segoe UI", system-ui, sans-serif;
      background:
        radial-gradient(circle at 15% -10%, #d7e4ff 0%, rgba(239, 243, 247, 0) 55%),
        radial-gradient(circle at 95% 0%, #f6e7ff 0%, rgba(239, 243, 247, 0) 55%),
        var(--bg);
      color: var(--text);
      min-height: 100vh;
    }
    .wrap {
      max-width: 980px;
      margin: 36px auto 80px;
      padding: 0 24px;
      animation: fadeIn 0.4s ease-out;
    }
    .page-head {
      display: flex;
      align-items: center;
      justify-content: space-between;
      margin-bottom: 18px;
    }
    .page-title {
      font-size: 26px;
      margin: 0;
      letter-spacing: -0.02em;
    }
    .page-sub {
      margin: 6px 0 0;
      color: var(--muted);
      font-size: 14px;
    }
    .card {
      background: var(--card);
      border: 1px solid var(--border);
      border-radius: 16px;
      box-shadow: var(--shadow);
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
      border-radius: 10px;
      padding: 11px 12px;
      font-size: 14px;
      margin-bottom: 14px;
      background: #fbfcfe;
    }
    .tabs {
      display: inline-flex;
      gap: 8px;
      padding: 6px;
      background: rgba(255, 255, 255, 0.8);
      border: 1px solid var(--border);
      border-radius: 999px;
      box-shadow: 0 10px 20px rgba(15, 23, 42, 0.08);
      margin-bottom: 18px;
    }
    .tab {
      background: transparent;
      border: 0;
      padding: 10px 16px;
      border-radius: 999px;
      font-weight: 600;
      color: var(--muted);
      cursor: pointer;
      transition: all 0.2s ease;
    }
    .tab.active {
      background: var(--accent-soft);
      color: var(--accent);
    }
    .panel { display: none; }
    .panel.active { display: block; }
    .list {
      display: flex;
      flex-direction: column;
      gap: 12px;
      margin-top: 8px;
    }
    .row {
      display: flex;
      align-items: center;
      gap: 10px;
      font-size: 14px;
      font-weight: 600;
      padding: 10px 12px;
      background: #f7f9fc;
      border-radius: 10px;
      border: 1px solid #eef2f7;
    }
    .actions {
      margin-top: 20px;
      display: flex;
      align-items: center;
      gap: 12px;
      flex-wrap: wrap;
    }
    .filters {
      display: grid;
      grid-template-columns: 70px minmax(160px, 1fr) 40px minmax(160px, 1fr) auto;
      gap: 10px;
      align-items: center;
      margin-bottom: 12px;
    }
    .filters label {
      margin: 0;
    }
    .filters input[type="date"] {
      margin-bottom: 0;
    }
    @media (max-width: 720px) {
      .filters {
        grid-template-columns: 1fr;
        justify-items: stretch;
      }
    }
    button {
      border: 0;
      background: var(--accent);
      color: white;
      padding: 10px 16px;
      border-radius: 10px;
      font-weight: 600;
      cursor: pointer;
      box-shadow: 0 10px 20px rgba(31, 79, 215, 0.2);
    }
    .button-link {
      display: inline-flex;
      align-items: center;
      gap: 6px;
      padding: 8px 14px;
      border-radius: 10px;
      border: 1px solid var(--border);
      background: #fff;
      color: var(--text);
      text-decoration: none;
      font-weight: 600;
      font-size: 13px;
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
    table {
      width: 100%;
      border-collapse: collapse;
      font-size: 14px;
    }
    thead th {
      text-align: left;
      padding: 10px 8px;
      font-size: 12px;
      text-transform: uppercase;
      letter-spacing: 0.04em;
      color: var(--muted);
      background: #f5f7fb;
      border-bottom: 1px solid var(--border);
    }
    tbody td {
      padding: 10px 8px;
      border-bottom: 1px solid #f1f5f9;
    }
    tbody tr:nth-child(even) {
      background: #fbfcfe;
    }
    .table-wrap {
      overflow: auto;
      border-radius: 12px;
      border: 1px solid #eef2f7;
    }
    .empty {
      color: var(--muted);
      font-size: 14px;
      padding: 14px 8px;
    }
    @keyframes fadeIn {
      from { opacity: 0; transform: translateY(6px); }
      to { opacity: 1; transform: translateY(0); }
    }
  </style>
</head>
<body>
  <div class="wrap">
    <div class="page-head">
      <div>
        <h1 class="page-title">Canada Post Control Center</h1>
        <p class="page-sub">Manage account settings and track every label created.</p>
      </div>
    </div>
    <div class="tabs" role="tablist">
      <button class="tab {{if eq .ActiveTab "settings"}}active{{end}}" data-target="settings-panel" type="button">Canada Post Account Settings</button>
      <button class="tab {{if eq .ActiveTab "labels"}}active{{end}}" data-target="labels-panel" type="button">Created Labels</button>
    </div>

    <div class="card panel {{if eq .ActiveTab "settings"}}active{{end}}" id="settings-panel">
      <h1>Canada Post Account Settings</h1>
      <form method="post" action="/settings?client_id={{.ClientID}}">
        <input type="hidden" name="session_token" value="{{.SessionToken}}">
        <input type="hidden" name="form_type" value="settings">
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
      <div style="margin-top:26px; border-top:1px solid var(--border); padding-top:22px;">
        <h1>Currency Conversion Rates</h1>
        <p class="hint" style="margin:0 0 14px;">Set how much CAD equals 1 unit of the selected currency (e.g., 1 USD = 0.74 CAD).</p>
        <form method="post" action="/settings?client_id={{.ClientID}}">
          <input type="hidden" name="session_token" value="{{.SessionToken}}">
          <input type="hidden" name="form_type" value="currency">
          <label for="currency_code">Currency</label>
          <select id="currency_code" name="currency_code" style="width:100%; border:1px solid var(--border); border-radius:10px; padding:11px 12px; font-size:14px; margin-bottom:14px; background:#fbfcfe;">
            {{range .Currencies}}
              <option value="{{.Code}}">{{.Label}}</option>
            {{end}}
          </select>
          <label for="rate_to_cad">Rate to CAD</label>
          <input id="rate_to_cad" name="rate_to_cad" type="text" placeholder="0.74" required>
          <div class="actions">
            <button type="submit">Save Currency Rate</button>
          </div>
          {{if .CurrencyMessage}}<div class="message">{{.CurrencyMessage}}</div>{{end}}
        </form>
        <div style="margin-top:18px;">
          {{if .CurrencyRates}}
            <div class="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Currency</th>
                    <th>Rate to CAD</th>
                    <th>Updated</th>
                  </tr>
                </thead>
                <tbody>
                  {{range .CurrencyRates}}
                  <tr>
                    <td>{{.CurrencyCode}}</td>
                    <td>{{printf "%.4f" .RateToCad}}</td>
                    <td>{{.UpdatedAt}}</td>
                  </tr>
                  {{end}}
                </tbody>
              </table>
            </div>
          {{else}}
            <div class="empty">No currency rates configured.</div>
          {{end}}
        </div>
      </div>
    </div>

    <div class="card panel {{if eq .ActiveTab "labels"}}active{{end}}" id="labels-panel" style="margin-top:20px;">
      <h1>Created Labels</h1>
      <form method="get" action="/settings" class="filters">
        <input type="hidden" name="client_id" value="{{.ClientID}}">
        <input type="hidden" name="session_token" value="{{.SessionToken}}">
        <input type="hidden" name="tab" value="labels">
        <input type="hidden" name="page_size" value="{{.PageSize}}">
        <label style="margin:0;font-weight:600;">From</label>
        <input type="date" name="from" value="{{.FromDate}}">
        <label style="margin:0;font-weight:600;">To</label>
        <input type="date" name="to" value="{{.ToDate}}">
        <button type="submit">Filter</button>
      </form>
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Invoice UUID</th>
              <th>Shipment ID</th>
              <th>Service Name</th>
              <th>Weight (kg)</th>
              <th>Tracking #</th>
              <th>Shipping (CAD)</th>
              <th>Delivery Date</th>
              <th>ETA (days)</th>
              <th>Created At</th>
              <th>Label</th>
            </tr>
          </thead>
          <tbody>
            {{if .Labels}}
              {{range .Labels}}
              <tr>
                <td>
                  {{if .InvoiceUUID}}
                    <a href="https://devadmin.lexmodo.com/orders/{{.InvoiceUUID}}" target="_blank" rel="noopener">{{.InvoiceUUID}}</a>
                  {{else}}
                    -
                  {{end}}
                </td>
                <td>{{.ShipmentID}}</td>
                <td>{{.ServiceName}}</td>
                <td>{{printf "%.2f" .Weight}}</td>
                <td>{{.TrackingNumber}}</td>
                <td>{{printf "%.2f" (div100 .ShippingChargesCents)}}</td>
                <td>{{.DeliveryDate}}</td>
                <td>{{if gt .DeliveryDays 0}}{{.DeliveryDays}}{{else}}-{{end}}</td>
                <td>{{.CreatedAt}}</td>
                <td>
                  <a href="/labels/{{.ID}}.pdf" target="_blank" rel="noopener">PDF</a>
                </td>
              </tr>
              {{end}}
            {{else}}
              <tr>
                <td colspan="10" class="empty">No labels found.</td>
              </tr>
            {{end}}
          </tbody>
        </table>
      </div>
      <div class="actions" style="justify-content: space-between; margin-top: 14px;">
        <div class="hint">Page {{.Page}}</div>
        <div class="actions" style="margin-top: 0;">
          {{if .HasPrev}}
            <a class="button-link" href="/settings?client_id={{.ClientID}}&session_token={{.SessionToken}}&tab=labels&from={{.FromDate}}&to={{.ToDate}}&page={{dec .Page}}&page_size={{.PageSize}}">Previous</a>
          {{end}}
          {{if .HasNext}}
            <a class="button-link" href="/settings?client_id={{.ClientID}}&session_token={{.SessionToken}}&tab=labels&from={{.FromDate}}&to={{.ToDate}}&page={{inc .Page}}&page_size={{.PageSize}}">Next</a>
          {{end}}
        </div>
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
