package config

import (
	"flag"
	"os"
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
