package wpgx

import (
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type ConfigTestSuite struct {
	suite.Suite
}

func TestConfigTestSuite(t *testing.T) {
	suite.Run(t, new(ConfigTestSuite))
}

func (suite *ConfigTestSuite) SetupTest() {
}

func (suite *ConfigTestSuite) TestDefaultConfigParse() {
	suite.T().Setenv("POSTGRES_APPNAME", "test")
	config := ConfigFromEnv()
	suite.Equal(0, len(config.ReadReplicas))
}

func (suite *ConfigTestSuite) TestDefaultConfigParse2Replica() {
	suite.T().Setenv("POSTGRES_APPNAME", "test")
	suite.T().Setenv("POSTGRES_REPLICAPREFIXES", "HOSTED,X1")
	suite.T().Setenv("HOSTED_NAME", "hostedDB")
	suite.T().Setenv("X1_NAME", "X1DB")
	config := ConfigFromEnv()
	suite.Equal(2, len(config.ReadReplicas))
}

func (suite *ConfigTestSuite) TestStringDoesNotMutateConfig() {
	config := &Config{
		Password: "primary-secret",
		AppName:  "test",
		ReadReplicas: []ReadReplicaConfig{
			{Name: "r1", Password: "replica-secret"},
		},
	}
	s := config.String()
	suite.NotContains(s, "primary-secret")
	suite.NotContains(s, "replica-secret")
	suite.Equal("primary-secret", config.Password)
	suite.Equal("replica-secret", config.ReadReplicas[0].Password)
}

func (suite *ConfigTestSuite) TestValidRejectsReplicaMinConnsAboveMaxConns() {
	config := &Config{
		MaxConns: 10,
		AppName:  "test",
		ReadReplicas: []ReadReplicaConfig{
			{Name: "r1", MinConns: 5, MaxConns: 10},
			{Name: "r2", MinConns: 11, MaxConns: 10},
		},
	}
	suite.ErrorContains(config.Valid(), "ReadReplicas[1].MinConns")
	config.ReadReplicas[1].MinConns = 10
	suite.NoError(config.Valid())
}

func (suite *ConfigTestSuite) TestAcquireDefaultsFromEnv() {
	suite.T().Setenv("POSTGRES_APPNAME", "test")
	suite.T().Setenv("POSTGRES_REPLICAPREFIXES", "R1")
	suite.T().Setenv("R1_NAME", "r1")
	suite.T().Setenv("R1_ACQUIRETIMEOUT", "750ms")
	suite.T().Setenv("R1_MAXACQUIREWAITERS", "8")
	config := ConfigFromEnv()
	suite.Equal(2*time.Second, config.AcquireTimeout)
	suite.Equal(int32(0), config.MaxAcquireWaiters)
	suite.Equal(750*time.Millisecond, config.ReadReplicas[0].AcquireTimeout)
	suite.Equal(int32(8), config.ReadReplicas[0].MaxAcquireWaiters)
}

func (suite *ConfigTestSuite) TestValidRejectsNegativeAcquireLimits() {
	config := &Config{MaxConns: 1, AppName: "test", AcquireTimeout: -time.Second}
	suite.ErrorContains(config.Valid(), "AcquireTimeout")
	config.AcquireTimeout = 0
	config.MaxAcquireWaiters = -1
	suite.ErrorContains(config.Valid(), "MaxAcquireWaiters")
	config.MaxAcquireWaiters = 0
	config.ReadReplicas = []ReadReplicaConfig{{Name: "r1", MaxConns: 1, AcquireTimeout: -1}}
	suite.ErrorContains(config.Valid(), "ReadReplicas[0].AcquireTimeout")
	config.ReadReplicas[0].AcquireTimeout = 0
	config.ReadReplicas[0].MaxAcquireWaiters = -1
	suite.ErrorContains(config.Valid(), "ReadReplicas[0].MaxAcquireWaiters")
	config.ReadReplicas[0].MaxAcquireWaiters = 0
	suite.NoError(config.Valid())
}
