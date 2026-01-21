package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	Port              string
	GRPCAddr          string
	AppClientID       string
	AppSecret         string
	RedirectURI       string
	AuthorizeURL      string
	TokenURL          string
	DBUser            string
	DBPass            string
	DBHost            string
	DBName            string
	FedexRatesAPIURL  string
	FedexShipmentsURL string
	FedexCancelURL    string
	PublicBaseURL     string
	OrdersGRPCAddr    string
}

func LoadConfig() Config {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("json")
	v.AddConfigPath(".")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	setDefaults(v)
	_ = v.ReadInConfig()

	return Config{
		Port:              v.GetString("server.port"),
		GRPCAddr:          v.GetString("server.grpc_addr"),
		AppClientID:       v.GetString("oauth.app_client_id"),
		AppSecret:         v.GetString("oauth.app_secret"),
		RedirectURI:       v.GetString("oauth.redirect_uri"),
		AuthorizeURL:      v.GetString("oauth.authorize_url"),
		TokenURL:          v.GetString("oauth.token_url"),
		DBUser:            v.GetString("database.user"),
		DBPass:            v.GetString("database.pass"),
		DBHost:            v.GetString("database.host"),
		DBName:            v.GetString("database.name"),
		FedexRatesAPIURL:  v.GetString("fedex.rates_api_url"),
		FedexShipmentsURL: v.GetString("fedex.shipments_api_url"),
		FedexCancelURL:    v.GetString("fedex.shipments_cancel_api_url"),
		PublicBaseURL:     v.GetString("server.public_base_url"),
		OrdersGRPCAddr:    v.GetString("orders.grpc_addr"),
	}
}

func (c Config) DSN() string {
	return fmt.Sprintf(
		"%s:%s@tcp(%s)/%s?parseTime=true&loc=UTC",
		c.DBUser,
		c.DBPass,
		c.DBHost,
		c.DBName,
	)
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("server.port", "50050")
	v.SetDefault("server.grpc_addr", "0.0.0.0:50051")
	v.SetDefault("server.public_base_url", "")
	v.SetDefault("fedex.rates_api_url", "http://localhost:8000/api/fedex/rates")
	v.SetDefault("fedex.shipments_api_url", "http://localhost:8000/api/fedex/shipments")
	v.SetDefault("fedex.shipments_cancel_api_url", "http://localhost:8000/api/fedex/shipments/cancel")
	v.SetDefault("orders.grpc_addr", "192.168.1.99:7000")
}
