package database

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"lexmodo-plugin/config"

	_ "github.com/go-sql-driver/mysql"
)

type Store struct {
	DB *sql.DB
}

func NewStore(cfg config.Config) (*Store, error) {
	db, err := sql.Open("mysql", cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("db connection failed: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("db ping failed: %w", err)
	}

	store := &Store{DB: db}
	if err := store.ensureTables(); err != nil {
		return nil, err
	}

	log.Println("Connected to MySQL")
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.DB == nil {
		return nil
	}
	return s.DB.Close()
}

func (s *Store) ensureTables() error {
	if err := s.ensureChosenRatesTable(); err != nil {
		return err
	}
	if err := s.ensureTrackingNumbersTable(); err != nil {
		return err
	}
	if err := s.ensureShippingSettingsTable(); err != nil {
		return err
	}
	if err := s.ensureCurrencyRatesTable(); err != nil {
		return err
	}
	if err := s.ensureLabelRecordsTable(); err != nil {
		return err
	}
	if err := s.ensureClientPostOfficesTable(); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureChosenRatesTable() error {
	_, err := s.DB.Exec(`
		CREATE TABLE IF NOT EXISTS chosen_shipping_rates (
			invoice_id VARCHAR(255) PRIMARY KEY,
			rate_id VARCHAR(255) NOT NULL,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		)
	`)
	return err
}

func (s *Store) ensureTrackingNumbersTable() error {
	_, err := s.DB.Exec(`
		CREATE TABLE IF NOT EXISTS tracking_numbers (
			invoice_id VARCHAR(255) PRIMARY KEY,
			tracking_number VARCHAR(255) NOT NULL,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		)
	`)
	return err
}

func (s *Store) ensureShippingSettingsTable() error {
	_, err := s.DB.Exec(`
		CREATE TABLE IF NOT EXISTS shipping_settings (
			client_id BIGINT PRIMARY KEY,
			account_number VARCHAR(255) NOT NULL,
			enabled_services TEXT NOT NULL,
			default_postal_code VARCHAR(10) NOT NULL DEFAULT '',
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}
	return s.ensureShippingSettingsColumns()
}

func (s *Store) ensureCurrencyRatesTable() error {
	_, err := s.DB.Exec(`
		CREATE TABLE IF NOT EXISTS currency_rates (
			id BIGINT PRIMARY KEY AUTO_INCREMENT,
			client_id BIGINT NOT NULL,
			currency_code VARCHAR(8) NOT NULL,
			rate_to_cad DOUBLE NOT NULL,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			UNIQUE KEY uniq_currency_rate (client_id, currency_code)
		)
	`)
	return err
}

func (s *Store) ensureLabelRecordsTable() error {
	_, err := s.DB.Exec(`
		CREATE TABLE IF NOT EXISTS label_records (
			id VARCHAR(64) PRIMARY KEY,
			shipment_id VARCHAR(64) NOT NULL,
			tracking_number VARCHAR(64) NOT NULL,
			invoice_uuid VARCHAR(255) NOT NULL DEFAULT '',
			rate_id VARCHAR(255) NOT NULL DEFAULT '',
			carrier VARCHAR(64) NOT NULL DEFAULT '',
			service_code VARCHAR(64) NOT NULL,
			service_name VARCHAR(255) NOT NULL DEFAULT '',
			shipping_charges_cents BIGINT NOT NULL DEFAULT 0,
			delivery_date VARCHAR(32) NOT NULL DEFAULT '',
			delivery_days INT NOT NULL DEFAULT 0,
			refund_link TEXT,
			weight DOUBLE NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}
	return s.ensureLabelRecordColumns()
}

func (s *Store) ensureLabelRecordColumns() error {
	var dbName string
	if err := s.DB.QueryRow(`SELECT DATABASE()`).Scan(&dbName); err != nil {
		return err
	}
	if strings.TrimSpace(dbName) == "" {
		return nil
	}

	rows, err := s.DB.Query(`
		SELECT column_name
		FROM information_schema.columns
		WHERE table_schema = ? AND table_name = 'label_records'
	`, dbName)
	if err != nil {
		return err
	}
	defer rows.Close()

	existing := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		existing[strings.ToLower(name)] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}

	columns := []struct {
		name string
		def  string
	}{
		{name: "invoice_uuid", def: "invoice_uuid VARCHAR(255) NOT NULL DEFAULT ''"},
		{name: "rate_id", def: "rate_id VARCHAR(255) NOT NULL DEFAULT ''"},
		{name: "carrier", def: "carrier VARCHAR(64) NOT NULL DEFAULT ''"},
		{name: "service_name", def: "service_name VARCHAR(255) NOT NULL DEFAULT ''"},
		{name: "shipping_charges_cents", def: "shipping_charges_cents BIGINT NOT NULL DEFAULT 0"},
		{name: "delivery_date", def: "delivery_date VARCHAR(32) NOT NULL DEFAULT ''"},
		{name: "delivery_days", def: "delivery_days INT NOT NULL DEFAULT 0"},
		{name: "refund_link", def: "refund_link TEXT"},
	}

	for _, col := range columns {
		if existing[col.name] {
			continue
		}
		if _, err := s.DB.Exec("ALTER TABLE label_records ADD COLUMN " + col.def); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ensureClientPostOfficesTable() error {
	_, err := s.DB.Exec(`
		CREATE TABLE IF NOT EXISTS client_post_offices (
			id BIGINT PRIMARY KEY AUTO_INCREMENT,
			client_id BIGINT NOT NULL,
			search_postal_code VARCHAR(10) NOT NULL,
			office_id VARCHAR(20) NOT NULL,
			office_name VARCHAR(100) NOT NULL,
			office_location VARCHAR(100),
			office_address VARCHAR(100),
			city VARCHAR(50),
			province VARCHAR(2),
			office_postal_code VARCHAR(10),
			latitude DECIMAL(10,7),
			longitude DECIMAL(10,7),
			distance_km DECIMAL(7,3),
			bilingual BOOLEAN DEFAULT FALSE,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			UNIQUE KEY uniq_client_postal_office (client_id, search_postal_code, office_id),
			KEY idx_client_postal (client_id, search_postal_code)
		)
	`)
	return err
}

func (s *Store) ensureShippingSettingsColumns() error {
	var dbName string
	if err := s.DB.QueryRow(`SELECT DATABASE()`).Scan(&dbName); err != nil {
		return err
	}
	if strings.TrimSpace(dbName) == "" {
		return nil
	}

	rows, err := s.DB.Query(`
		SELECT column_name
		FROM information_schema.columns
		WHERE table_schema = ? AND table_name = 'shipping_settings'
	`, dbName)
	if err != nil {
		return err
	}
	defer rows.Close()

	existing := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		existing[strings.ToLower(name)] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}

	columns := []struct {
		name string
		def  string
	}{
		{name: "default_postal_code", def: "default_postal_code VARCHAR(10) NOT NULL DEFAULT ''"},
	}
	for _, col := range columns {
		if existing[col.name] {
			continue
		}
		if _, err := s.DB.Exec("ALTER TABLE shipping_settings ADD COLUMN " + col.def); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SaveChosenRateID(invoiceID string, rateID string) error {
	_, err := s.DB.Exec(`
		INSERT INTO chosen_shipping_rates (invoice_id, rate_id)
		VALUES (?, ?)
		ON DUPLICATE KEY UPDATE rate_id = VALUES(rate_id)
	`, invoiceID, rateID)
	return err
}

func (s *Store) SaveTrackingNumber(invoiceID string, trackingNumber string) error {
	_, err := s.DB.Exec(`
		INSERT INTO tracking_numbers (invoice_id, tracking_number)
		VALUES (?, ?)
		ON DUPLICATE KEY UPDATE tracking_number = VALUES(tracking_number)
	`, invoiceID, trackingNumber)
	return err
}

func (s *Store) LoadChosenRateID(invoiceID string) (string, error) {
	var rateID string
	err := s.DB.QueryRow(`
		SELECT rate_id
		FROM chosen_shipping_rates
		WHERE invoice_id = ?
	`, invoiceID).Scan(&rateID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return rateID, err
}

func (s *Store) LoadTrackingNumber(invoiceID string) (string, error) {
	var trackingNumber string
	err := s.DB.QueryRow(`
		SELECT tracking_number
		FROM tracking_numbers
		WHERE invoice_id = ?
	`, invoiceID).Scan(&trackingNumber)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return trackingNumber, err
}

func (s *Store) LoadLatestTrackingNumber() (string, error) {
	var trackingNumber string
	err := s.DB.QueryRow(`
		SELECT tracking_number
		FROM tracking_numbers
		ORDER BY updated_at DESC
		LIMIT 1
	`).Scan(&trackingNumber)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return trackingNumber, err
}

type LabelRecord struct {
	ID                   string
	ShipmentID           string
	TrackingNumber       string
	InvoiceUUID          string
	RateID               string
	Carrier              string
	ServiceCode          string
	ServiceName          string
	ShippingChargesCents int64
	DeliveryDate         string
	DeliveryDays         int
	RefundLink           string
	Weight               float64
	CreatedAt            time.Time
}

func (s *Store) SaveLabelRecord(record LabelRecord) error {
	_, err := s.DB.Exec(`
		INSERT INTO label_records (
			id,
			shipment_id,
			tracking_number,
			invoice_uuid,
			rate_id,
			carrier,
			service_code,
			service_name,
			shipping_charges_cents,
			delivery_date,
			delivery_days,
			refund_link,
			weight
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, record.ID, record.ShipmentID, record.TrackingNumber, record.InvoiceUUID, record.RateID, record.Carrier, record.ServiceCode, record.ServiceName, record.ShippingChargesCents, record.DeliveryDate, record.DeliveryDays, record.RefundLink, record.Weight)
	return err
}

func (s *Store) LoadLabelRecords(fromDate string, toDate string, limit int) ([]LabelRecord, error) {
	records, _, err := s.LoadLabelRecordsPage(fromDate, toDate, limit, 0)
	return records, err
}

func (s *Store) LoadLabelRecordsPage(fromDate string, toDate string, limit int, offset int) ([]LabelRecord, bool, error) {
	if limit <= 0 {
		limit = 10
	}
	if offset < 0 {
		offset = 0
	}
	query := `
		SELECT id, shipment_id, tracking_number, invoice_uuid, rate_id, carrier, service_code, service_name, shipping_charges_cents, delivery_date, delivery_days, refund_link, weight, created_at
		FROM label_records
	`
	args := []any{}
	clauses := []string{"(carrier = ? OR carrier = '')"}
	args = append(args, "Canada Post")
	if strings.TrimSpace(fromDate) != "" {
		clauses = append(clauses, "created_at >= ?")
		args = append(args, fromDate+" 00:00:00")
	}
	if strings.TrimSpace(toDate) != "" {
		clauses = append(clauses, "created_at <= ?")
		args = append(args, toDate+" 23:59:59")
	}
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	args = append(args, limit+1, offset)

	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	records := []LabelRecord{}
	for rows.Next() {
		var rec LabelRecord
		var refundLink sql.NullString
		if err := rows.Scan(
			&rec.ID,
			&rec.ShipmentID,
			&rec.TrackingNumber,
			&rec.InvoiceUUID,
			&rec.RateID,
			&rec.Carrier,
			&rec.ServiceCode,
			&rec.ServiceName,
			&rec.ShippingChargesCents,
			&rec.DeliveryDate,
			&rec.DeliveryDays,
			&refundLink,
			&rec.Weight,
			&rec.CreatedAt,
		); err != nil {
			return nil, false, err
		}
		if refundLink.Valid {
			rec.RefundLink = refundLink.String
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasNext := false
	if len(records) > limit {
		hasNext = true
		records = records[:limit]
	}
	return records, hasNext, nil
}

func (s *Store) LoadRefundLinkByLabelID(labelID string) (string, error) {
	labelID = strings.TrimSpace(labelID)
	if labelID == "" {
		return "", nil
	}
	var link string
	err := s.DB.QueryRow(`
		SELECT refund_link
		FROM label_records
		WHERE id = ?
		LIMIT 1
	`, labelID).Scan(&link)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return strings.TrimSpace(link), err
}

func (s *Store) LoadRefundLinkByShipmentID(shipmentID string) (string, error) {
	shipmentID = strings.TrimSpace(shipmentID)
	if shipmentID == "" {
		return "", nil
	}
	var link string
	err := s.DB.QueryRow(`
		SELECT refund_link
		FROM label_records
		WHERE shipment_id = ?
		ORDER BY created_at DESC
		LIMIT 1
	`, shipmentID).Scan(&link)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return strings.TrimSpace(link), err
}

func (s *Store) LoadRefundLinkByInvoiceUUID(invoiceUUID string) (string, error) {
	invoiceUUID = strings.TrimSpace(invoiceUUID)
	if invoiceUUID == "" {
		return "", nil
	}
	var link string
	err := s.DB.QueryRow(`
		SELECT refund_link
		FROM label_records
		WHERE invoice_uuid = ?
		ORDER BY created_at DESC
		LIMIT 1
	`, invoiceUUID).Scan(&link)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return strings.TrimSpace(link), err
}

func (s *Store) LoadLabelRecordByLabelID(labelID string) (LabelRecord, error) {
	labelID = strings.TrimSpace(labelID)
	if labelID == "" {
		return LabelRecord{}, nil
	}
	var rec LabelRecord
	var refundLink sql.NullString
	err := s.DB.QueryRow(`
		SELECT id, shipment_id, tracking_number, invoice_uuid, rate_id, carrier, service_code, service_name, shipping_charges_cents, delivery_date, delivery_days, refund_link, weight, created_at
		FROM label_records
		WHERE id = ?
		LIMIT 1
	`, labelID).Scan(
		&rec.ID,
		&rec.ShipmentID,
		&rec.TrackingNumber,
		&rec.InvoiceUUID,
		&rec.RateID,
		&rec.Carrier,
		&rec.ServiceCode,
		&rec.ServiceName,
		&rec.ShippingChargesCents,
		&rec.DeliveryDate,
		&rec.DeliveryDays,
		&refundLink,
		&rec.Weight,
		&rec.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return LabelRecord{}, nil
	}
	if refundLink.Valid {
		rec.RefundLink = refundLink.String
	}
	return rec, err
}

type ShippingSettings struct {
	AccountNumber     string
	EnabledServices   map[string]bool
	DefaultPostalCode string
}

type CurrencyRate struct {
	CurrencyCode string
	RateToCad    float64
	UpdatedAt    time.Time
}

func (s *Store) SaveShippingSettings(clientID int64, accountNumber string, enabledServices []string) error {
	services := strings.Join(enabledServices, ",")
	_, err := s.DB.Exec(`
		INSERT INTO shipping_settings (client_id, account_number, enabled_services)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE account_number = VALUES(account_number), enabled_services = VALUES(enabled_services)
	`, clientID, accountNumber, services)
	return err
}

func (s *Store) LoadShippingSettings(clientID int64) (ShippingSettings, error) {
	var settings ShippingSettings
	var services string
	err := s.DB.QueryRow(`
		SELECT account_number, enabled_services, default_postal_code
		FROM shipping_settings
		WHERE client_id = ?
	`, clientID).Scan(&settings.AccountNumber, &services, &settings.DefaultPostalCode)
	if err == sql.ErrNoRows {
		return ShippingSettings{}, nil
	}
	if err != nil {
		return ShippingSettings{}, err
	}
	settings.EnabledServices = parseEnabledServices(services)
	return settings, nil
}

func (s *Store) SaveDefaultPostalCode(clientID int64, postalCode string) error {
	postalCode = strings.ToUpper(strings.TrimSpace(postalCode))
	_, err := s.DB.Exec(`
		INSERT INTO shipping_settings (client_id, account_number, enabled_services, default_postal_code)
		VALUES (?, '', '', ?)
		ON DUPLICATE KEY UPDATE default_postal_code = VALUES(default_postal_code)
	`, clientID, postalCode)
	return err
}

func (s *Store) SaveCurrencyRate(clientID int64, currencyCode string, rateToCad float64) error {
	code := strings.ToUpper(strings.TrimSpace(currencyCode))
	if code == "" {
		return fmt.Errorf("currency code is required")
	}
	if rateToCad <= 0 {
		return fmt.Errorf("rate_to_cad must be greater than zero")
	}
	_, err := s.DB.Exec(`
		INSERT INTO currency_rates (client_id, currency_code, rate_to_cad)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE rate_to_cad = VALUES(rate_to_cad)
	`, clientID, code, rateToCad)
	return err
}

func (s *Store) LoadCurrencyRate(clientID int64, currencyCode string) (float64, bool, error) {
	code := strings.ToUpper(strings.TrimSpace(currencyCode))
	if code == "" {
		return 0, false, nil
	}
	var rate float64
	err := s.DB.QueryRow(`
		SELECT rate_to_cad
		FROM currency_rates
		WHERE client_id = ? AND currency_code = ?
	`, clientID, code).Scan(&rate)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return rate, true, nil
}

func (s *Store) LoadCurrencyRates(clientID int64) ([]CurrencyRate, error) {
	rows, err := s.DB.Query(`
		SELECT currency_code, rate_to_cad, updated_at
		FROM currency_rates
		WHERE client_id = ?
		ORDER BY currency_code
	`, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rates []CurrencyRate
	for rows.Next() {
		var rate CurrencyRate
		if err := rows.Scan(&rate.CurrencyCode, &rate.RateToCad, &rate.UpdatedAt); err != nil {
			return nil, err
		}
		rates = append(rates, rate)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return rates, nil
}

func parseEnabledServices(value string) map[string]bool {
	enabled := make(map[string]bool)
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		enabled[item] = true
	}
	return enabled
}
