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
	DefaultEnvPrefix = "postgres"
	AppNameLengthMax = 32
)

type ReadReplicaConfig struct {
	Name            ReplicaName   `required:"true"`
	Username        string        `default:"postgres"`
	Password        string        `default:"my-secret"`
	Host            string        `default:"localhost"`
	Port            int           `default:"5432"`
	DBName          string        `default:"wpgx_test_db"`
	MaxConns        int32         `default:"100"`
	MinConns        int32         `default:"0"`
	MaxConnLifetime time.Duration `default:"6h"`
	MaxConnIdleTime time.Duration `default:"1m"`
	// BeforeAcquire is a function that is called before acquiring a connection.
	BeforeAcquire func(context.Context, *pgx.Conn) bool `ignored:"true"`
	IsProxy       bool                                  `default:"false"`
	Broken        bool                                  `default:"false"`
}

// RedisConfig is the configuration for Redis connection.
type RedisConfig struct {
	Host     string `default:"localhost"`
	Port     int    `default:"6379"`
	Password string `default:""`
	DB       int    `default:"0"`
	// MaxRetries is the maximum number of retries for failed commands.
	MaxRetries int `default:"3"`
	// PoolSize is the maximum number of connections in the pool.
	PoolSize int `default:"10"`
	// MinIdleConns is the minimum number of idle connections in the pool.
	MinIdleConns int `default:"5"`
	// MaxConnAge is the maximum age of a connection.
	MaxConnAge time.Duration `default:"30m"`
	// PoolTimeout is the timeout for getting a connection from the pool.
	PoolTimeout time.Duration `default:"4s"`
	// IdleTimeout is the timeout for idle connections.
	IdleTimeout time.Duration `default:"5m"`
	// IdleCheckFrequency is the frequency of idle checks.
	IdleCheckFrequency time.Duration `default:"1m"`
}

// Config is the configuration for the WPgx.
// Note: for backward compatibility, connection settings of the primary instance are kept in the root Config,
// creating a bit code duplication with ReadReplicaConfig.
type Config struct {
	Username        string        `default:"postgres"`
	Password        string        `default:"my-secret"`
	Host            string        `default:"localhost"`
	Port            int           `default:"5432"`
	DBName          string        `default:"wpgx_test_db"`
	MaxConns        int32         `default:"100"`
	MinConns        int32         `default:"0"`
	MaxConnLifetime time.Duration `default:"6h"`
	MaxConnIdleTime time.Duration `default:"1m"`
	// BeforeAcquire is a function that is called before acquiring a connection.
	BeforeAcquire func(context.Context, *pgx.Conn) bool `ignored:"true"`
	IsProxy       bool                                  `default:"false"`

	EnablePrometheus bool   `default:"true"`
	EnableTracing    bool   `default:"true"`
	AppName          string `required:"true"`

	// ReplicaConfigPrefixes is a list of replica configuration prefixes. They will
	// be used to create ReadReplicas by using envconfig to parse them.
	ReplicaPrefixes []string `default:""`
	// ReadReplicas is a list of read replicas, parsed from ReplicaNames.
	ReadReplicas []ReadReplicaConfig `ignored:"true"`

	// Redis configuration
	Redis RedisConfig `default:""`
}

func (c *Config) Valid() error {
	if c.MinConns > c.MaxConns {
		return fmt.Errorf("MinConns must <= MaxConns, incorrect config: %s", c)
	}
	if len(c.AppName) == 0 || len(c.AppName) > AppNameLengthMax {
		return fmt.Errorf("invalid AppName: %s", c)
	}
	showedNames := make(map[ReplicaName]bool)
	for i, replica := range c.ReadReplicas {
		if len(replica.Name) == 0 {
			return fmt.Errorf("invalid ReadReplicas[%d].Name: %s", i, c)
		}
		if len(replica.Name) > AppNameLengthMax {
			return fmt.Errorf("ReadReplicas[%d].Name is too long: %s", i, c)
		}
		if replica.Name == ReservedReplicaNamePrimary {
			return fmt.Errorf("ReadReplicas[%d].Name cannot be %s", i, ReservedReplicaNamePrimary)
		}
		if string(replica.Name) == c.AppName {
			return fmt.Errorf("ReadReplicas[%d].Name must be different from AppName: %s", i, c)
		}
		if _, ok := showedNames[replica.Name]; ok {
			return fmt.Errorf("duplicated ReadReplicas[%d].Name: %s", i, c)
		}
		showedNames[replica.Name] = true
	}

	// Validate Redis configuration only if Redis is configured
	if c.Redis.Host != "" || c.Redis.Port != 0 {
		if c.Redis.PoolSize <= 0 {
			return fmt.Errorf("Redis PoolSize must be > 0, got: %d", c.Redis.PoolSize)
		}
		if c.Redis.MinIdleConns < 0 {
			return fmt.Errorf("Redis MinIdleConns must be >= 0, got: %d", c.Redis.MinIdleConns)
		}
		if c.Redis.MinIdleConns > c.Redis.PoolSize {
			return fmt.Errorf("Redis MinIdleConns must <= PoolSize, got: %d > %d", c.Redis.MinIdleConns, c.Redis.PoolSize)
		}
		if c.Redis.MaxRetries < 0 {
			return fmt.Errorf("Redis MaxRetries must be >= 0, got: %d", c.Redis.MaxRetries)
		}
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
	for i := range copy.ReadReplicas {
		copy.ReadReplicas[i].Password = "*hidden*"
	}
	if len(copy.Redis.Password) > 0 {
		copy.Redis.Password = "*hidden*"
	}
	return fmt.Sprintf("%+v", copy)
}

func ConfigFromEnv() *Config {
	return ConfigFromEnvPrefix(DefaultEnvPrefix)
}

func ConfigFromEnvPrefix(prefix string) *Config {
	config := &Config{}
	envconfig.MustProcess(prefix, config)
	for _, prefix := range config.ReplicaPrefixes {
		replicaConfig := ReadReplicaConfig{}
		envconfig.MustProcess(prefix, &replicaConfig)
		config.ReadReplicas = append(config.ReadReplicas, replicaConfig)
	}
	if err := config.Valid(); err != nil {
		log.Fatal().Msgf("%s", err)
	}
	return config
}
