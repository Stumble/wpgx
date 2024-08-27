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
