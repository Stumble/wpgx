package wpgx

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kelseyhightower/envconfig"
	"github.com/rs/zerolog/log"
)

const (
	HighQPSMaxOpenConns = 100
	DefaultEnvPrefix    = "postgres"
	AppNameLengthMax    = 32
)

type Config struct {
	Username         string        `default:"postgres"`
	Password         string        `default:"my-secret"`
	Host             string        `default:"localhost"`
	Port             int           `default:"5432"`
	DBName           string        `default:"wpgx_test_db"`
	MaxConns         int32         `default:"100"`
	MinConns         int32         `default:"0"`
	MaxConnLifetime  time.Duration `default:"6h"`
	MaxConnIdleTime  time.Duration `default:"1m"`
	EnablePrometheus bool          `default:"true"`
	EnableTracing    bool          `default:"true"`
	AppName          string        `required:"true"`

	BeforeAcquire func(context.Context, *pgx.Conn) bool `ignored:"true"`
}

func (c *Config) Valid() error {
	if !(c.MinConns <= c.MaxConns) {
		return fmt.Errorf("MinConns must <= MaxConns, incorrect config: %s", c)
	}
	if len(c.AppName) == 0 || len(c.AppName) > AppNameLengthMax {
		return fmt.Errorf("Invalid AppName: %s", c)
	}
	return nil
}

func (c *Config) String() string {
	if c == nil {
		return "nil"
	}
	copy := *c
	if len(copy.Password) > 0 {
		copy.Password = "*hidden*"
	}
	return fmt.Sprintf("%+v", copy)
}

func ConfigFromEnv() *Config {
	config := &Config{}
	envconfig.MustProcess(DefaultEnvPrefix, config)
	if err := config.Valid(); err != nil {
		log.Fatal().Msgf("%s", err)
	}
	return config
}

func ConfigFromEnvPrefix(prefix string) *Config {
	config := &Config{}
	envconfig.MustProcess(prefix, config)
	if err := config.Valid(); err != nil {
		log.Fatal().Msgf("%s", err)
	}
	return config
}
