package wpgx

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func countGoroutines(substr string) int {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return strings.Count(string(buf[:n]), substr)
		}
		buf = make([]byte, 2*len(buf))
	}
}

func TestNewPoolClosesCreatedPoolsOnError(t *testing.T) {
	const healthCheck = "pgxpool.(*Pool).backgroundHealthCheck"
	before := countGoroutines(healthCheck)
	// pgxpool connects lazily, so nothing needs to listen on these ports.
	_, err := NewPool(context.Background(), &Config{
		Username: "postgres", Host: "127.0.0.1", Port: 1, DBName: "db", MaxConns: 1,
		SSLMode: "disable", AppName: "leak_test",
		ReadReplicas: []ReadReplicaConfig{
			{Name: "ok", Username: "postgres", Host: "127.0.0.1", Port: 1, DBName: "db", MaxConns: 1, SSLMode: "disable"},
			{Name: "bad", Username: "postgres", Host: "127.0.0.1", Port: 1, DBName: "db", MaxConns: 1, SSLMode: "bogus"},
		},
	})
	require.Error(t, err)
	require.Eventually(t, func() bool { return countGoroutines(healthCheck) == before },
		5*time.Second, 10*time.Millisecond, "pools created before the failing replica must be closed")
}
