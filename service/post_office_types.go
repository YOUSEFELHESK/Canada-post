package service

import (
	"fmt"
	"strings"
)

type PostOffice struct {
	OfficeID        string
	Name            string
	Location        string
	OfficeAddress   string
	City            string
	Province        string
	PostalCode      string
	Latitude        float64
	Longitude       float64
	Distance        float64
	BilingualDesign bool
}

func normalizeCanadianPostalCode(postal string) string {
	postal = strings.ToUpper(strings.TrimSpace(postal))
	postal = strings.ReplaceAll(postal, " ", "")
	return postal
}

func formatPostOfficeDisplay(office PostOffice) string {
	location := strings.TrimSpace(office.Location)
	address := strings.TrimSpace(office.OfficeAddress)
	city := strings.TrimSpace(office.City)
	base := strings.TrimSpace(fmt.Sprintf("%s - %s (%s)", location, address, city))
	if office.Distance > 0 {
		return base + " [" + formatDistanceKm(office.Distance) + "km]"
	}
	return base
}

func basePostOfficeDisplay(selection string) string {
	selection = strings.TrimSpace(selection)
	if selection == "" {
		return ""
	}
	if idx := strings.LastIndex(selection, " ["); idx != -1 {
		selection = strings.TrimSpace(selection[:idx])
	}
	return selection
}

func formatDistanceKm(distance float64) string {
	if distance < 0 {
		distance = 0
	}
	return fmt.Sprintf("%.2f", distance)
}
