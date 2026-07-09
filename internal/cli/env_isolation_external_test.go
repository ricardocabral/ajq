package cli_test

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ajq-cli-test-config-*")
	if err != nil {
		panic(err)
	}
	_ = os.Unsetenv("AJQ_CONFIG")
	_ = os.Setenv("XDG_CONFIG_HOME", dir)
	_ = os.Unsetenv("AJQ_BACKEND")
	_ = os.Unsetenv("AJQ_MODEL")
	_ = os.Unsetenv("AJQ_BASE_URL")
	os.Exit(m.Run())
}
