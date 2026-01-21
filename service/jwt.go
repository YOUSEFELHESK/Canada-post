package service

import (
	"fmt" // For printing output
	"os"  // For reading environment variables

	// For working with timestamps

	"github.com/golang-jwt/jwt/v5" // JWT library
)

// CustomClaims defines the custom JWT claims for the Lexmodo plugin
type CustomClaims struct {
	Iss     string `json:"iss"`
	StoreID int    `json:"store_id"`
	jwt.RegisteredClaims
}

func Verify(tokenStr string) (*CustomClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &CustomClaims{}, func(t *jwt.Token) (interface{}, error) {
		return []byte(os.Getenv("APP_SECRET")), nil

	})
	if err != nil || !token.Valid {

		return nil, err
	}
	claims, ok := token.Claims.(*CustomClaims)

	if !ok {
		return nil, fmt.Errorf("invalid token claims")
	}
	return claims, nil
}
