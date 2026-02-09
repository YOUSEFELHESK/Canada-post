package service

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
)

type PostOfficeService struct {
	cpClient *CanadaPostClient
	db       *sql.DB
}

func NewPostOfficeService(cpClient *CanadaPostClient, db *sql.DB) *PostOfficeService {
	return &PostOfficeService{
		cpClient: cpClient,
		db:       db,
	}
}

func (s *PostOfficeService) GetUsedPostalCodes(clientID int64) ([]string, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("post office store not configured")
	}
	if clientID <= 0 {
		return nil, fmt.Errorf("client id required")
	}
	rows, err := s.db.Query(`
		SELECT DISTINCT search_postal_code
		FROM client_post_offices
		WHERE client_id = ?
		ORDER BY search_postal_code
	`, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	postalCodes := []string{}
	for rows.Next() {
		var postalCode string
		if err := rows.Scan(&postalCode); err != nil {
			return nil, err
		}
		postalCode = strings.TrimSpace(postalCode)
		if postalCode == "" {
			continue
		}
		postalCodes = append(postalCodes, postalCode)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return postalCodes, nil
}

func (s *PostOfficeService) GetUsedPostalCodesPage(clientID int64, limit, offset int) ([]string, bool, error) {
	if s == nil || s.db == nil {
		return nil, false, fmt.Errorf("post office store not configured")
	}
	if clientID <= 0 {
		return nil, false, fmt.Errorf("client id required")
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.Query(`
		SELECT search_postal_code, MAX(created_at) AS last_created
		FROM client_post_offices
		WHERE client_id = ?
		GROUP BY search_postal_code
		ORDER BY last_created DESC
		LIMIT ? OFFSET ?
	`, clientID, limit+1, offset)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	postalCodes := []string{}
	for rows.Next() {
		var postalCode string
		var _lastCreated sql.NullTime
		if err := rows.Scan(&postalCode, &_lastCreated); err != nil {
			return nil, false, err
		}
		postalCode = strings.TrimSpace(postalCode)
		if postalCode == "" {
			continue
		}
		postalCodes = append(postalCodes, postalCode)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasNext := false
	if len(postalCodes) > limit {
		hasNext = true
		postalCodes = postalCodes[:limit]
	}
	return postalCodes, hasNext, nil
}

func (s *PostOfficeService) GetOrFetchPostOffices(ctx context.Context, clientID int64, postalCode string) ([]PostOffice, bool, error) {
	if s == nil || s.db == nil {
		return nil, false, fmt.Errorf("post office store not configured")
	}
	if clientID <= 0 {
		return nil, false, fmt.Errorf("client id required")
	}
	postalCode = normalizeCanadianPostalCode(postalCode)
	if postalCode == "" {
		return nil, false, fmt.Errorf("postal code required")
	}

	offices, err := s.getFromDatabase(clientID, postalCode)
	if err != nil {
		return nil, false, err
	}
	if len(offices) > 0 {
		return offices, true, nil
	}

	if s.cpClient == nil {
		return nil, false, fmt.Errorf("canada post client not configured")
	}
	offices, err = s.cpClient.FindPostOffices(ctx, postalCode)
	if err != nil {
		return nil, false, fmt.Errorf("failed to fetch from Canada Post: %w", err)
	}
	if len(offices) == 0 {
		return offices, false, nil
	}

	if err := s.storeInDatabase(clientID, postalCode, offices); err != nil {
		log.Printf("warning: failed to store post offices: %v", err)
	}
	return offices, false, nil
}

func (s *PostOfficeService) GetAllStoredOffices(clientID int64) ([]PostOffice, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("post office store not configured")
	}
	if clientID <= 0 {
		return nil, fmt.Errorf("client id required")
	}
	rows, err := s.db.Query(`
		SELECT office_id, office_name, office_location, office_address,
		       city, province, office_postal_code, latitude, longitude,
		       distance_km, bilingual
		FROM client_post_offices
		WHERE client_id = ?
		ORDER BY distance_km ASC
	`, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	offices := []PostOffice{}
	for rows.Next() {
		var office PostOffice
		if err := rows.Scan(
			&office.OfficeID,
			&office.Name,
			&office.Location,
			&office.OfficeAddress,
			&office.City,
			&office.Province,
			&office.PostalCode,
			&office.Latitude,
			&office.Longitude,
			&office.Distance,
			&office.BilingualDesign,
		); err != nil {
			return nil, err
		}
		offices = append(offices, office)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return offices, nil
}

func (s *PostOfficeService) GetStoredOfficesByPostalCode(clientID int64, postalCode string) ([]PostOffice, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("post office store not configured")
	}
	if clientID <= 0 {
		return nil, fmt.Errorf("client id required")
	}
	postalCode = normalizeCanadianPostalCode(postalCode)
	if postalCode == "" {
		return nil, fmt.Errorf("postal code required")
	}
	return s.getFromDatabase(clientID, postalCode)
}

func (s *PostOfficeService) getFromDatabase(clientID int64, postalCode string) ([]PostOffice, error) {
	rows, err := s.db.Query(`
		SELECT office_id, office_name, office_location, office_address,
		       city, province, office_postal_code, latitude, longitude,
		       distance_km, bilingual
		FROM client_post_offices
		WHERE client_id = ? AND search_postal_code = ?
		ORDER BY distance_km ASC
	`, clientID, postalCode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	offices := []PostOffice{}
	for rows.Next() {
		var office PostOffice
		if err := rows.Scan(
			&office.OfficeID,
			&office.Name,
			&office.Location,
			&office.OfficeAddress,
			&office.City,
			&office.Province,
			&office.PostalCode,
			&office.Latitude,
			&office.Longitude,
			&office.Distance,
			&office.BilingualDesign,
		); err != nil {
			return nil, err
		}
		offices = append(offices, office)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return offices, nil
}

func (s *PostOfficeService) storeInDatabase(clientID int64, searchPostalCode string, offices []PostOffice) error {
	if len(offices) == 0 {
		return nil
	}
	if s == nil || s.db == nil {
		return fmt.Errorf("post office store not configured")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO client_post_offices
		(client_id, search_postal_code, office_id, office_name, office_location,
		 office_address, city, province, office_postal_code, latitude, longitude,
		 distance_km, bilingual)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE updated_at = CURRENT_TIMESTAMP
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, office := range offices {
		if strings.TrimSpace(office.OfficeID) == "" {
			continue
		}
		if _, err := stmt.Exec(
			clientID,
			searchPostalCode,
			strings.TrimSpace(office.OfficeID),
			strings.TrimSpace(office.Name),
			strings.TrimSpace(office.Location),
			strings.TrimSpace(office.OfficeAddress),
			strings.TrimSpace(office.City),
			strings.TrimSpace(office.Province),
			strings.TrimSpace(office.PostalCode),
			office.Latitude,
			office.Longitude,
			office.Distance,
			office.BilingualDesign,
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *PostOfficeService) FindOfficeIDByDisplayText(clientID int64, displayText string) (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("post office store not configured")
	}
	if clientID <= 0 {
		return "", fmt.Errorf("client id required")
	}
	base := basePostOfficeDisplay(displayText)
	if base == "" {
		return "", fmt.Errorf("office selection required")
	}
	var officeID string
	err := s.db.QueryRow(`
		SELECT office_id
		FROM client_post_offices
		WHERE client_id = ?
		AND CONCAT(office_location, ' - ', office_address, ' (', city, ')') = ?
		LIMIT 1
	`, clientID, base).Scan(&officeID)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("office not found")
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(officeID), nil
}

func (s *PostOfficeService) DeletePostalCode(clientID int64, postalCode string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("post office store not configured")
	}
	if clientID <= 0 {
		return fmt.Errorf("client id required")
	}
	postalCode = normalizeCanadianPostalCode(postalCode)
	if postalCode == "" {
		return fmt.Errorf("postal code required")
	}
	_, err := s.db.Exec(`
		DELETE FROM client_post_offices
		WHERE client_id = ? AND search_postal_code = ?
	`, clientID, postalCode)
	return err
}
