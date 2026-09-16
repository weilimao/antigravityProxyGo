package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type ServerConfig struct {
	Host             string `yaml:"host"`
	Port             string `yaml:"port"`
	JWTSecret        string `yaml:"jwt_secret"`
	TokenExpireHours int    `yaml:"token_expire_hours"`
}

type DatabaseConfig struct {
	Type string `yaml:"type"` // "sqlite" or "mysql"
	DSN  string `yaml:"dsn"`  // DSN or sqlite file path
}

type RelayPaymentConfig struct {
	RelayURL          string `yaml:"relay_url"`           // 极客工坊切单地址，如 https://geektools.dev/api/v1/relay/create
	RelaySecret       string `yaml:"relay_secret"`        // 极客工坊 HMAC-SHA256 预共享通信密钥
	RelayCheckoutBase string `yaml:"relay_checkout_base"` // 自定义收银台前缀基准，如 https://geektools.dev
	NotifyURL         string `yaml:"notify_url"`          // Web 平台接收支付成功回调的公网/域名地址
	ReturnURL         string `yaml:"return_url"`          // 支付完成后用户浏览器跳转回的地址
	SiteURL           string `yaml:"site_url"`            // 本平台对外访问基准地址
}

type GoRelayGatewayConfig struct {
	GatewayURL    string `yaml:"gateway_url"`     // 现网已部署 Go Relay 网关地址，如 http://127.0.0.1:18444
	AdminKey      string `yaml:"admin_key"`       // 网关超级管理员 Key
	AdminPassword string `yaml:"admin_password"`  // 网关超级管理员密码
	SyncEnabled   bool   `yaml:"sync_enabled"`    // 是否开启自动向网关热同步配置
}

type RedisConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Host     string `yaml:"host"`
	Port     string `yaml:"port"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

type Config struct {
	Server   ServerConfig         `yaml:"server"`
	Database DatabaseConfig       `yaml:"database"`
	Redis    RedisConfig          `yaml:"redis"`
	Payment  RelayPaymentConfig   `yaml:"payment"`
	Gateway  GoRelayGatewayConfig `yaml:"gateway"`
}

var GlobalConfig *Config

func LoadConfig(path string) (*Config, error) {
	if path == "" {
		path = "config.yaml"
	}

	data, err := os.ReadFile(path)
	if err != nil {
		// 缺省默认配置
		cfg := &Config{
			Server: ServerConfig{
				Host:             "0.0.0.0",
				Port:             "8100",
				JWTSecret:        "antigravity-web-secret-change-me-2026",
				TokenExpireHours: 72,
			},
			Database: DatabaseConfig{
				Type: "sqlite",
				DSN:  filepath.Join("data", "antigravity_web.db"),
			},
			Redis: RedisConfig{
				Enabled:  false,
				Host:     "127.0.0.1",
				Port:     "6379",
				Password: "",
				DB:       1,
			},
			Payment: RelayPaymentConfig{
				RelayURL:          "http://127.0.0.1:8000/api/v1/relay/create",
				RelaySecret:       "geektools_relay_secret_key_888",
				RelayCheckoutBase: "http://127.0.0.1:8000",
				NotifyURL:         "http://127.0.0.1:8100/api/v1/pay/notify/relay",
				ReturnURL:         "http://127.0.0.1:6688/#/dashboard",
				SiteURL:           "http://127.0.0.1:8100",
			},
			Gateway: GoRelayGatewayConfig{
				GatewayURL:    "http://127.0.0.1:18444",
				AdminKey:      "sk-ant-admin",
				AdminPassword: "",
				SyncEnabled:   true,
			},
		}
		GlobalConfig = cfg
		return cfg, nil
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	GlobalConfig = &cfg
	return &cfg, nil
}
