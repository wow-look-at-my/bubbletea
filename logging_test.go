package tea

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"log"
	"os"
	"path/filepath"
	"testing"
)

func TestLogToFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.txt")
	prefix := "logprefix"
	f, err := LogToFile(path, prefix)
	assert.Nil(t, err)

	log.SetFlags(log.Lmsgprefix)
	log.Println("some test log")
	assert.NoError(t, f.Close())

	out, err := os.ReadFile(path)
	assert.Nil(t, err)

	require.Equal(t, prefix+" some test log\n", string(out))

}
