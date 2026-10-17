package wpgx

import (
	"testing"

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
