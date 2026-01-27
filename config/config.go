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

	PublicBaseURL  string
	OrdersGRPCAddr string
	CanadaPost     CanadaPostConfig
}

type CanadaPostConfig struct {
	BaseURL        string
	CustomerNumber string
	Username       string
	Password       string
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

		PublicBaseURL:  v.GetString("server.public_base_url"),
		OrdersGRPCAddr: v.GetString("orders.grpc_addr"),
		CanadaPost: CanadaPostConfig{
			BaseURL:        v.GetString("canadapost.base_url"),
			CustomerNumber: v.GetString("canadapost.customer_number"),
			Username:       v.GetString("canadapost.username"),
			Password:       v.GetString("canadapost.password"),
		},
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

	// Canada Post
	v.SetDefault("canadapost.base_url", "https://ct.soa-gw.canadapost.ca")
	v.SetDefault("canadapost.customer_number", "")
	v.SetDefault("canadapost.username", "")
	v.SetDefault("canadapost.password", "")

	v.SetDefault("orders.grpc_addr", "192.168.1.99:7000")

	_ = v.BindEnv("canadapost.base_url", "CANADA_POST_BASE_URL", "CANADAPOST_BASE_URL")
	_ = v.BindEnv("canadapost.customer_number", "CANADA_POST_CUSTOMER_NUMBER", "CANADAPOST_CUSTOMER_NUMBER")
	_ = v.BindEnv("canadapost.username", "CANADA_POST_USERNAME", "CANADAPOST_USERNAME")
	_ = v.BindEnv("canadapost.password", "CANADA_POST_PASSWORD", "CANADAPOST_PASSWORD")
}
