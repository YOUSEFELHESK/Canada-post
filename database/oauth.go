package database

import (
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"time"
)

func (s *Store) GetClientIDFromState(state string) int {
	hashed := hashState(state)
	var storeID int
	err := s.DB.QueryRow(`SELECT store_id FROM oauth_state WHERE state = ?`, hashed).Scan(&storeID)
	if err != nil {
		log.Println("GetClientIDFromState error:", err)
		return 0
	}
	return storeID
}

func (s *Store) SaveState(storeID int, state string) error {
	hashed := hashState(state)
	_, err := s.DB.Exec(`
		INSERT INTO oauth_state (store_id, state)
		VALUES (?, ?)
		ON DUPLICATE KEY UPDATE state = VALUES(state)
	`, storeID, hashed)
	if err != nil {
		log.Println("SaveState error:", err)
	}
	return err
}

func (s *Store) DeleteState(storeID int) {
	_, err := s.DB.Exec(`DELETE FROM oauth_state WHERE store_id=?`, storeID)
	if err != nil {
		log.Println("DeleteState error:", err)
	}
}

func (s *Store) InsertToken(storeID int, access, refresh string, expiry time.Time) bool {
	_, err := s.DB.Exec(`
		INSERT INTO plugin_oauth (client_id, access_token, refresh_token, expiry_date)
		VALUES (?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			access_token = VALUES(access_token),
			refresh_token = VALUES(refresh_token),
			expiry_date = VALUES(expiry_date)
	`, storeID, access, refresh, expiry)
	if err != nil {
		log.Println("InsertToken error:", err)
		return false
	}
	return true
}

func (s *Store) GetAccessToken(storeID int) string {
	var token string
	var expiry sql.NullTime
	err := s.DB.QueryRow(`
		SELECT access_token, expiry_date
		FROM plugin_oauth
		WHERE client_id = ?`, storeID).Scan(&token, &expiry)

	if err != nil || token == "" || !expiry.Valid || time.Now().After(expiry.Time) {
		if err != nil {
			log.Println("GetAccessToken error:", err)
		}
		return ""
	}
	return token
}

func (s *Store) GetRefreshToken(storeID int) string {
	var token string
	err := s.DB.QueryRow(`SELECT refresh_token FROM plugin_oauth WHERE client_id = ?`, storeID).Scan(&token)
	if err != nil {
		log.Println("GetRefreshToken error:", err)
		return ""
	}
	return token
}

func hashState(state string) string {
	hash := md5.Sum([]byte(state))
	return hex.EncodeToString(hash[:])
}

// CleanupPluginData removes local plugin data for a store after uninstall.
func (s *Store) CleanupPluginData(storeID int64) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("store is not configured")
	}
	if storeID <= 0 {
		return fmt.Errorf("store id is required")
	}

	log.Printf("cleaning up plugin data for store %d", storeID)
	tx, err := s.DB.Begin()
	if err != nil {
		return fmt.Errorf("failed to start cleanup transaction: %w", err)
	}

	rollback := func() {
		_ = tx.Rollback()
	}

	deleteStep := func(stepName string, query string, args ...any) error {
		result, execErr := tx.Exec(query, args...)
		if execErr != nil {
			if isMissingTableError(execErr) {
				log.Printf("cleanup skipped (%s): table missing", stepName)
				return nil
			}
			return fmt.Errorf("%s: %w", stepName, execErr)
		}
		affected, _ := result.RowsAffected()
		log.Printf("cleanup step %s: %d rows deleted", stepName, affected)
		return nil
	}

	if err := deleteStep("delete plugin_oauth", "DELETE FROM plugin_oauth WHERE client_id = ?", storeID); err != nil {
		rollback()
		return err
	}
	if err := deleteStep("delete oauth_tokens", "DELETE FROM oauth_tokens WHERE store_id = ?", storeID); err != nil {
		rollback()
		return err
	}
	if err := deleteStep("delete oauth_state", "DELETE FROM oauth_state WHERE store_id = ?", storeID); err != nil {
		rollback()
		return err
	}
	if err := deleteStep("delete shipping_settings", "DELETE FROM shipping_settings WHERE client_id = ?", storeID); err != nil {
		rollback()
		return err
	}
	if err := deleteStep("delete currency_rates", "DELETE FROM currency_rates WHERE client_id = ?", storeID); err != nil {
		rollback()
		return err
	}
	if err := deleteStep("delete client_post_offices", "DELETE FROM client_post_offices WHERE client_id = ?", storeID); err != nil {
		rollback()
		return err
	}

	if err := tx.Commit(); err != nil {
		rollback()
		return fmt.Errorf("failed to commit cleanup transaction: %w", err)
	}

	log.Printf("cleanup completed for store %d", storeID)
	return nil
}

func isMissingTableError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "doesn't exist") ||
		strings.Contains(errStr, "no such table") ||
		strings.Contains(errStr, "unknown table")
}
