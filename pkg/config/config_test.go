package config

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestLoad_Defaults(t *testing.T) {
	// Reset flags
	pflag.CommandLine = pflag.NewFlagSet(os.Args[0], pflag.ExitOnError)
	os.Args = []string{"cmd"}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.MetricsBindAddress != ":8090" {
		t.Errorf("Expected default metrics bind address :8090, got %s", cfg.MetricsBindAddress)
	}
	if cfg.AWSRateLimitQPS != 10 {
		t.Errorf("Expected default QPS 10, got %f", cfg.AWSRateLimitQPS)
	}
}

func TestLoad_EnvVarSubnets(t *testing.T) {
	// Reset flags
	pflag.CommandLine = pflag.NewFlagSet(os.Args[0], pflag.ExitOnError)
	os.Args = []string{"cmd"}

	err := os.Setenv("ENI_TAGGER_SUBNET_IDS", "subnet-123,subnet-456")
	require.NoError(t, err)
	defer func() {
		err := os.Unsetenv("ENI_TAGGER_SUBNET_IDS")
		require.NoError(t, err)
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(cfg.SubnetIDs) != 2 {
		t.Errorf("Expected 2 subnets, got %d", len(cfg.SubnetIDs))
	}
	if cfg.SubnetIDs[0] != "subnet-123" {
		t.Errorf("Expected subnet-123, got %s", cfg.SubnetIDs[0])
	}
}

func TestLoad_InvalidSubnet(t *testing.T) {
	// Reset flags
	pflag.CommandLine = pflag.NewFlagSet(os.Args[0], pflag.ExitOnError)
	os.Args = []string{"cmd"}

	err := os.Setenv("ENI_TAGGER_SUBNET_IDS", "invalid-id")
	require.NoError(t, err)
	defer func() {
		err := os.Unsetenv("ENI_TAGGER_SUBNET_IDS")
		require.NoError(t, err)
	}()

	_, err = Load()
	if err == nil {
		t.Error("Expected error for invalid subnet ID, got nil")
	}
}

func TestLoad_InvalidTagNamespace(t *testing.T) {
	// Reset flags
	pflag.CommandLine = pflag.NewFlagSet(os.Args[0], pflag.ExitOnError)
	os.Args = []string{"cmd", "--tag-namespace", "invalid"}

	// Capture stderr
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	cfg, err := Load()
	require.NoError(t, err)
	err = w.Close()
	require.NoError(t, err)
	os.Stderr = oldStderr

	// Read captured output
	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(r)
	require.NoError(t, err)
	warningOutput := buf.String()

	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.TagNamespace != "invalid" {
		t.Errorf("Expected TagNamespace 'invalid', got '%s'", cfg.TagNamespace)
	}

	if !strings.Contains(warningOutput, "Warning: invalid tag-namespace value 'invalid'") {
		t.Errorf("Expected warning message, got: %s", warningOutput)
	}
}

func TestLoad_EnvFallbacks(t *testing.T) {
	// Reset flags
	pflag.CommandLine = pflag.NewFlagSet(os.Args[0], pflag.ExitOnError)
	os.Args = []string{"cmd"}

	err := os.Setenv("ENI_TAGGER_DRY_RUN", "true")
	require.NoError(t, err)
	err = os.Setenv("ENI_TAGGER_METRICS_BIND_ADDRESS", ":9010")
	require.NoError(t, err)
	err = os.Setenv("ENI_TAGGER_ALLOW_SHARED_ENI_TAGGING", "true")
	require.NoError(t, err)
	err = os.Setenv("ENI_TAGGER_AWS_RATE_LIMIT_QPS", "20")
	require.NoError(t, err)
	defer func() {
		err := os.Unsetenv("ENI_TAGGER_DRY_RUN")
		require.NoError(t, err)
		err = os.Unsetenv("ENI_TAGGER_METRICS_BIND_ADDRESS")
		require.NoError(t, err)
		err = os.Unsetenv("ENI_TAGGER_ALLOW_SHARED_ENI_TAGGING")
		require.NoError(t, err)
		err = os.Unsetenv("ENI_TAGGER_AWS_RATE_LIMIT_QPS")
		require.NoError(t, err)
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !cfg.DryRun {
		t.Errorf("Expected DryRun=true from env, got false")
	}
	if cfg.MetricsBindAddress != ":9010" {
		t.Errorf("Expected MetricsBindAddress ':9010' from env, got %s", cfg.MetricsBindAddress)
	}
	if !cfg.AllowSharedENITagging {
		t.Errorf("Expected AllowSharedENITagging=true from env, got false")
	}
	if cfg.AWSRateLimitQPS != 20.0 {
		t.Errorf("Expected AWSRateLimitQPS=20.0 from env, got %f", cfg.AWSRateLimitQPS)
	}
}

func TestLoad_CLI_Precedence_OverEnv(t *testing.T) {
	// Reset flags
	pflag.CommandLine = pflag.NewFlagSet(os.Args[0], pflag.ExitOnError)
	// Pass CLI to enable dry-run (true)
	os.Args = []string{"cmd", "--dry-run"}

	err := os.Setenv("ENI_TAGGER_DRY_RUN", "false")
	require.NoError(t, err)
	defer func() {
		err := os.Unsetenv("ENI_TAGGER_DRY_RUN")
		require.NoError(t, err)
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	// CLI should take precedence: cli set dry-run true
	if !cfg.DryRun {
		t.Errorf("Expected DryRun=true from CLI precedence, got false")
	}
}
