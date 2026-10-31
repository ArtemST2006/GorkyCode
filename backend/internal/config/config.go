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
	Reminder        Reminder      `yaml:"reminder"`
	Email           Email         `yaml:"email"`
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
	CheckBusy        bool          `yaml:"check_busy" env:"GOOGLE_CHECK_BUSY" env-default:"false"`
}

// Enabled reports whether Google Calendar credentials are configured.
func (g Google) Enabled() bool {
	return g.ClientID != "" && g.ClientSecret != ""
}

// Reminder holds the reminder dispatch settings.
type Reminder struct {
	// Offsets are the advance moments before a task at which a reminder is
	// fired, as a comma-separated list of durations such as "72h,24h,3h,10m".
	Offsets string `yaml:"offsets" env:"REMINDER_OFFSETS" env-default:"72h,24h,3h,10m"`
	// ScanInterval is how often due pending notifications are claimed and sent.
	ScanInterval time.Duration `yaml:"scan_interval" env:"REMINDER_SCAN_INTERVAL" env-default:"30s"`
	// SyncInterval is how often the upcoming moments are re-derived from the
	// tasks table; it cannot exceed the claim TTL.
	SyncInterval time.Duration `yaml:"sync_interval" env:"REMINDER_SYNC_INTERVAL" env-default:"10m"`
	// ClaimTTL is the expiry of one delivery attempt.
	ClaimTTL time.Duration `yaml:"claim_ttl" env:"REMINDER_CLAIM_TTL" env-default:"2m"`
	// Retention is how long delivered and unacknowledged records are kept.
	Retention time.Duration `yaml:"retention" env:"REMINDER_RETENTION" env-default:"720h"`
	// Horizon is the reconciliation lookahead window.
	Horizon time.Duration `yaml:"horizon" env:"REMINDER_HORIZON" env-default:"168h"`
	// ReconcileLimit bounds one reconciliation scan.
	ReconcileLimit int `yaml:"reconcile_limit" env:"REMINDER_RECONCILE_LIMIT" env-default:"500"`
	// BatchLimit bounds the number of notifications sent in one scan.
	BatchLimit int `yaml:"batch_limit" env:"REMINDER_BATCH_LIMIT" env-default:"100"`
	// MinAdvanceLead is the minimum distance between task creation and the
	// first advance reminder; advance pings firing sooner than this are
	// dropped to avoid noisy "reminder about a 24h event" five minutes after
	// the user asked for it.
	MinAdvanceLead time.Duration `yaml:"min_advance_lead" env:"REMINDER_MIN_ADVANCE_LEAD" env-default:"5h"`
}

// Email holds outgoing reminder email settings. The integration is off while
// Host or From is empty.
type Email struct {
	Host     string        `yaml:"host" env:"SMTP_HOST"`
	Port     int           `yaml:"port" env:"SMTP_PORT" env-default:"587"`
	Username string        `yaml:"username" env:"SMTP_USERNAME"`
	Password string        `yaml:"password" env:"SMTP_PASSWORD"`
	From     string        `yaml:"from" env:"EMAIL_FROM"`
	Timeout  time.Duration `yaml:"timeout" env:"EMAIL_TIMEOUT" env-default:"15s"`
	// Kinds lists the notification kinds mailed to the user address; by
	// default letters cover every user-facing kind.
	Kinds []string `yaml:"kinds" env:"EMAIL_KINDS" env-default:"reminder_due,task_due,meeting_start"`
}

// Enabled reports whether SMTP credentials are configured.
func (e Email) Enabled() bool {
	return e.Host != "" && e.From != ""
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
