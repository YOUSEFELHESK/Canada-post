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
	if err := s.ensureLabelRecordsTable(); err != nil {
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
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
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
			service_code VARCHAR(64) NOT NULL,
			weight DOUBLE NOT NULL,
			label_pdf LONGBLOB,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`)
	return err
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
	ID             string
	ShipmentID     string
	TrackingNumber string
	ServiceCode    string
	Weight         float64
	LabelPDF       []byte
	CreatedAt      time.Time
}

func (s *Store) SaveLabelRecord(record LabelRecord) error {
	_, err := s.DB.Exec(`
		INSERT INTO label_records (
			id,
			shipment_id,
			tracking_number,
			service_code,
			weight,
			label_pdf
		) VALUES (?, ?, ?, ?, ?, ?)
	`, record.ID, record.ShipmentID, record.TrackingNumber, record.ServiceCode, record.Weight, record.LabelPDF)
	return err
}

func (s *Store) LoadLabelRecords(fromDate string, toDate string, limit int) ([]LabelRecord, error) {
	if limit <= 0 {
		limit = 10
	}
	query := `
		SELECT id, shipment_id, tracking_number, service_code, weight, created_at
		FROM label_records
	`
	args := []any{}
	clauses := []string{}
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
	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := []LabelRecord{}
	for rows.Next() {
		var rec LabelRecord
		if err := rows.Scan(
			&rec.ID,
			&rec.ShipmentID,
			&rec.TrackingNumber,
			&rec.ServiceCode,
			&rec.Weight,
			&rec.CreatedAt,
		); err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	return records, rows.Err()
}

func (s *Store) LoadLabelPDF(id string) ([]byte, error) {
	var data []byte
	err := s.DB.QueryRow(`
		SELECT label_pdf
		FROM label_records
		WHERE id = ?
	`, id).Scan(&data)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("label not found")
	}
	return data, err
}

type ShippingSettings struct {
	AccountNumber   string
	EnabledServices map[string]bool
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
		SELECT account_number, enabled_services
		FROM shipping_settings
		WHERE client_id = ?
	`, clientID).Scan(&settings.AccountNumber, &services)
	if err == sql.ErrNoRows {
		return ShippingSettings{}, nil
	}
	if err != nil {
		return ShippingSettings{}, err
	}
	settings.EnabledServices = parseEnabledServices(services)
	return settings, nil
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
