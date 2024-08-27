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
}

func (c *Config) Valid() error {
	if !(c.MinConns <= c.MaxConns) {
		return fmt.Errorf("MinConns must <= MaxConns, incorrect config: %s", c)
	}
	if len(c.AppName) == 0 || len(c.AppName) > AppNameLengthMax {
		return fmt.Errorf("Invalid AppName: %s", c)
	}
	showedNames := make(map[ReplicaName]bool)
	for i, replica := range c.ReadReplicas {
		if len(replica.Name) == 0 {
			return fmt.Errorf("Invalid ReadReplicas[%d].Name: %s", i, c)
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
			return fmt.Errorf("Duplicated ReadReplicas[%d].Name: %s", i, c)
		}
		showedNames[replica.Name] = true
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
