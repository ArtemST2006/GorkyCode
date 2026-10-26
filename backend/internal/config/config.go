// Package config loads backend settings from YAML and environment variables.
package config

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/ilyakaznacheev/cleanenv"
)

// Config holds backend settings.
type Config struct {
	Env             string        `yaml:"env" env:"ENV" env-default:"local"`
	LogLevel        string        `yaml:"log_level" env:"LOG_LEVEL" env-default:"info"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout" env:"SHUTDOWN_TIMEOUT" env-default:"10s"`
	HTTPServer      HTTPServer    `yaml:"http-server"`
	Database        Database      `yaml:"database"`
	ArsModel        ArsModel      `yaml:"ars_model"`
	AIServiceURL    string        `yaml:"ai_service_url" env:"AI_SERVICE_URL" env-default:"http://localhost:8090"`
	Redis           Redis         `yaml:"redis"`
	Auth            Auth          `yaml:"auth"`
	Google          Google        `yaml:"google"`
}

// HTTPServer holds HTTP listener settings.
type HTTPServer struct {
	Address           string        `yaml:"address" env:"HTTP_ADDR" env-default:":8080"`
	ReadHeaderTimeout time.Duration `yaml:"read_header_timeout" env:"HTTP_READ_HEADER_TIMEOUT" env-default:"5s"`
	ReadTimeout       time.Duration `yaml:"read_timeout" env:"HTTP_READ_TIMEOUT" env-default:"10s"`
	WriteTimeout      time.Duration `yaml:"write_timeout" env:"HTTP_WRITE_TIMEOUT" env-default:"10s"`
	IdleTimeout       time.Duration `yaml:"idle_timeout" env:"HTTP_IDLE_TIMEOUT" env-default:"60s"`
}

// Database holds PostgreSQL pool settings.
type Database struct {
	URL             string        `yaml:"url" env:"DATABASE_URL" env-required:"true"`
	MaxConns        int32         `yaml:"max_conns" env:"DATABASE_MAX_CONNS" env-default:"10"`
	MinConns        int32         `yaml:"min_conns" env:"DATABASE_MIN_CONNS" env-default:"2"`
	MaxConnLifetime time.Duration `yaml:"max_conn_lifetime" env:"DATABASE_MAX_CONN_LIFETIME" env-default:"30m"`
	ConnectTimeout  time.Duration `yaml:"connect_timeout" env:"DATABASE_CONNECT_TIMEOUT" env-default:"5s"`
	QueryTimeout    time.Duration `yaml:"query_timeout" env:"DATABASE_QUERY_TIMEOUT" env-default:"3s"`
}

// ArsModel hold Ars model settings
type ArsModel struct {
	ModelPath    string `env:"MODEL_PATH" env-required:"true"`
	NumThreads   int    `yaml:"num_threads" env-default:"2"`
	DecodeMethod string `yaml:"decode_method" env-default:"greedy_search"`
	Provider     string `yaml:"provider" env-default:"cpu"`
}

// Redis holds Redis connection settings.
type Redis struct {
	URL string `yaml:"url" env:"REDIS_URL" env-required:"true"`
}

// Auth holds session settings.
type Auth struct {
	SessionTTL time.Duration `yaml:"session_ttl" env:"AUTH_SESSION_TTL" env-default:"24h"`
}

// Google holds Google Calendar OAuth settings. The integration is off while
// ClientID or ClientSecret is empty.
type Google struct {
	ClientID         string        `yaml:"client_id" env:"GOOGLE_CLIENT_ID"`
	ClientSecret     string        `yaml:"client_secret" env:"GOOGLE_CLIENT_SECRET"`
	RedirectURL      string        `yaml:"redirect_url" env:"GOOGLE_REDIRECT_URL"`
	FrontendRedirect string        `yaml:"frontend_redirect" env:"GOOGLE_FRONTEND_REDIRECT"`
	EventDuration    time.Duration `yaml:"event_duration" env:"GOOGLE_EVENT_DURATION" env-default:"1h"`
}

// Enabled reports whether Google Calendar credentials are configured.
func (g Google) Enabled() bool {
	return g.ClientID != "" && g.ClientSecret != ""
}

// Load reads CONFIG_PATH when set, then applies environment variables.
func Load() (*Config, error) {
	var cfg Config
	if path := os.Getenv("CONFIG_PATH"); path != "" {
		if err := cleanenv.ReadConfig(path, &cfg); err != nil {
			return nil, fmt.Errorf("read config %q: %w", path, err)
		}
	} else if err := cleanenv.ReadEnv(&cfg); err != nil {
		return nil, fmt.Errorf("read config from environment: %w", err)
	}

	return &cfg, nil
}

// MustLoad loads config or terminates the process, as in AvitoLab.
func MustLoad() *Config {
	cfg, err := Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	return cfg
}
