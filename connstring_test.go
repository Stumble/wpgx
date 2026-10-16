package wpgx

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func TestConnStringEscapesComponents(t *testing.T) {
	for _, tc := range []struct {
		name     string
		username string
		password string
		host     string
		dbName   string
	}{
		{name: "plain", username: "postgres", password: "my-secret", host: "localhost", dbName: "wpgx_test_db"},
		{name: "no password", username: "postgres", host: "localhost", dbName: "wpgx_test_db"},
		{name: "at sign", username: "postgres", password: "p@ss", host: "localhost", dbName: "db"},
		{name: "uri delimiters", username: "us:er@x", password: "a/b?c#d:e", host: "localhost", dbName: "db"},
		{name: "percent and space", username: "postgres", password: "100% sure ", host: "localhost", dbName: "db"},
		{name: "database name", username: "postgres", password: "pw", host: "localhost", dbName: "my db/x?y#z%"},
		{name: "ipv6 host", username: "postgres", password: "pw", host: "::1", dbName: "db"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := pgxpool.ParseConfig(connString(&pgxConfig{
				Username: tc.username,
				Password: tc.password,
				Host:     tc.host,
				Port:     6543,
				DBName:   tc.dbName,
				SSLMode:  "disable",
			}))
			require.NoError(t, err)
			cc := cfg.ConnConfig
			require.Equal(t, tc.username, cc.User)
			require.Equal(t, tc.password, cc.Password)
			require.Equal(t, tc.host, cc.Host)
			require.Equal(t, uint16(6543), cc.Port)
			require.Equal(t, tc.dbName, cc.Database)
			require.Nil(t, cc.TLSConfig)
		})
	}
}
