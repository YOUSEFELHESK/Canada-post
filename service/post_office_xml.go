package service

import "encoding/xml"

type PostOfficeListXML struct {
	XMLName     xml.Name        `xml:"http://www.canadapost.ca/ws/postoffice post-office-list"`
	PostOffices []PostOfficeXML `xml:"http://www.canadapost.ca/ws/postoffice post-office"`
}

type PostOfficeXML struct {
	OfficeID        string     `xml:"http://www.canadapost.ca/ws/postoffice office-id"`
	Name            string     `xml:"http://www.canadapost.ca/ws/postoffice name"`
	Location        string     `xml:"http://www.canadapost.ca/ws/postoffice location"`
	Distance        float64    `xml:"http://www.canadapost.ca/ws/postoffice distance"`
	BilingualDesign bool       `xml:"http://www.canadapost.ca/ws/postoffice bilingual-designation"`
	Address         AddressXML `xml:"http://www.canadapost.ca/ws/postoffice address"`
}

type AddressXML struct {
	OfficeAddress string  `xml:"http://www.canadapost.ca/ws/postoffice office-address"`
	City          string  `xml:"http://www.canadapost.ca/ws/postoffice city"`
	Province      string  `xml:"http://www.canadapost.ca/ws/postoffice province"`
	PostalCode    string  `xml:"http://www.canadapost.ca/ws/postoffice postal-code"`
	Latitude      float64 `xml:"http://www.canadapost.ca/ws/postoffice latitude"`
	Longitude     float64 `xml:"http://www.canadapost.ca/ws/postoffice longitude"`
}
