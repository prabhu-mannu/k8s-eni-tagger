package config

import (
	"bytes"
	"flag"
	"os"
	"strings"
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	// Reset flags
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
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
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	os.Args = []string{"cmd"}

	os.Setenv("ENI_TAGGER_SUBNET_IDS", "subnet-123,subnet-456")
	defer os.Unsetenv("ENI_TAGGER_SUBNET_IDS")

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
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	os.Args = []string{"cmd"}

	os.Setenv("ENI_TAGGER_SUBNET_IDS", "invalid-id")
	defer os.Unsetenv("ENI_TAGGER_SUBNET_IDS")

	_, err := Load()
	if err == nil {
		t.Error("Expected error for invalid subnet ID, got nil")
	}
}

func TestLoad_InvalidTagNamespace(t *testing.T) {
	// Reset flags
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	os.Args = []string{"cmd", "--tag-namespace", "invalid"}

	// Capture stderr
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	cfg, err := Load()
	w.Close()
	os.Stderr = oldStderr

	// Read captured output
	buf := new(bytes.Buffer)
	buf.ReadFrom(r)
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
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	os.Args = []string{"cmd"}

	os.Setenv("ENI_TAGGER_DRY_RUN", "true")
	os.Setenv("ENI_TAGGER_METRICS_BIND_ADDRESS", ":9010")
	os.Setenv("ENI_TAGGER_ALLOW_SHARED_ENI_TAGGING", "true")
	os.Setenv("ENI_TAGGER_AWS_RATE_LIMIT_QPS", "20")
	defer os.Unsetenv("ENI_TAGGER_DRY_RUN")
	defer os.Unsetenv("ENI_TAGGER_METRICS_BIND_ADDRESS")
	defer os.Unsetenv("ENI_TAGGER_ALLOW_SHARED_ENI_TAGGING")
	defer os.Unsetenv("ENI_TAGGER_AWS_RATE_LIMIT_QPS")

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
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	// Pass CLI to enable dry-run (true)
	os.Args = []string{"cmd", "--dry-run"}

	os.Setenv("ENI_TAGGER_DRY_RUN", "false")
	defer os.Unsetenv("ENI_TAGGER_DRY_RUN")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	// CLI should take precedence: cli set dry-run true
	if !cfg.DryRun {
		t.Errorf("Expected DryRun=true from CLI precedence, got false")
	}
}
