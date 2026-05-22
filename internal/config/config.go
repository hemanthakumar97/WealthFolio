package config

import (
	"fmt"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	DatabaseURL string `env:"DATABASE_URL,required"`
	JWTSecret   string `env:"JWT_SECRET,required"`
	AppEnv      string `env:"APP_ENV" envDefault:"development"`
	TZ          string `env:"TZ" envDefault:"Asia/Kolkata"`
	Port        int    `env:"PORT" envDefault:"8000"`

	SnapshotHour   int `env:"SNAPSHOT_HOUR" envDefault:"23"`
	MarketMoodHour int `env:"MARKET_MOOD_HOUR" envDefault:"19"`

	GmailWatcherHour   int    `env:"GMAIL_WATCHER_HOUR" envDefault:"7"`
	GmailLookbackDays  int    `env:"GMAIL_LOOKBACK_DAYS" envDefault:"7"`
	ZerodhaPDFPassword string `env:"ZERODHA_PDF_PASSWORD"` // Password for Zerodha contract note PDFs

	UploadMaxMB     int    `env:"UPLOAD_MAX_MB" envDefault:"10"`
	ProfilePicMaxMB int    `env:"PROFILE_PIC_MAX_MB" envDefault:"5"`
	UploadDir       string `env:"UPLOAD_DIR" envDefault:"/data/uploads"`
}

func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	return cfg, nil
}

func (c *Config) IsProduction() bool {
	return c.AppEnv == "production"
}
