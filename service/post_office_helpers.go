package service

import "fmt"

func (s *Server) resolveOfficeIDFromSelection(clientID int64, selection string) (string, error) {
	if s == nil || s.PostOffices == nil {
		return "", fmt.Errorf("post office service not configured")
	}
	if clientID <= 0 {
		return "", fmt.Errorf("client id required")
	}
	return s.PostOffices.FindOfficeIDByDisplayText(clientID, selection)
}
