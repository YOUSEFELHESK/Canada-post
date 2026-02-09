package service

import (
	"context"
	"strconv"
	"strings"
)

func clientIDFromContextInt(ctx context.Context) int64 {
	value := strings.TrimSpace(clientIDFromContext(ctx))
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}
