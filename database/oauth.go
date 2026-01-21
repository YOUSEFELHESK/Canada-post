package database

import (
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"log"
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
