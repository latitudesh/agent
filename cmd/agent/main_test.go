package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestMain lets a test run this test binary as the agent itself.
func TestMain(m *testing.M) {
	if os.Getenv("LSH_AGENT_RUN_MAIN") == "1" {
		os.Args = []string{os.Args[0], "-config", os.Getenv("LSH_AGENT_CONFIG")}
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestAgentStopsPromptlyOnSIGTERMDuringStalledRequest starts the agent against
// an API that accepts the request and never answers, sends SIGTERM while the
// request is in flight, and expects the agent to exit right away instead of
// waiting for the request timeout of every pending call.
func TestAgentStopsPromptlyOnSIGTERMDuringStalledRequest(t *testing.T) {
	requested := make(chan struct{}, 8)
	release := make(chan struct{})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer api.Close()
	defer close(release)

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	cfg := "agent:\n  interval: \"30s\"\nlatitude:\n  api_endpoint: \"" + api.URL + "/agent/ping\"\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(),
		"LSH_AGENT_RUN_MAIN=1",
		"LSH_AGENT_CONFIG="+cfgPath,
		"PROJECT_ID=proj_test",
		"FIREWALL_ID=fw_test",
		"FIREWALL_ENABLED=false",
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-requested:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the agent never called the API")
	}

	sent := time.Now()
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("agent exited with %v", err)
		}
		t.Logf("agent exited %s after SIGTERM", time.Since(sent).Round(time.Millisecond))
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("agent still running 5s after SIGTERM: it waits for the stalled API request to time out")
	}
}
