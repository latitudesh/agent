package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// runAsAgentEnv makes the test binary run main() instead of the tests, so a
// test can start the real agent as a child process and signal it.
const runAsAgentEnv = "LSH_AGENT_TEST_RUN_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(runAsAgentEnv) == "1" {
		os.Args[0] = "lsh-agent"
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// agentCommand returns the agent as a child process with a config pointing at
// apiURL, the firewall collector off and no ufw needed.
func agentCommand(t *testing.T, apiURL string, args ...string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	cfgYAML := "latitude:\n  api_endpoint: " + apiURL + "\n  project_id: proj_test\n  firewall_id: fw_test\nfirewall:\n  enabled: false\n"
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	// Arguments go to the child as they are: main() parses os.Args itself.
	cmd := exec.Command(os.Args[0], append([]string{"-config", cfgPath}, args...)...)
	cmd.Env = append(withoutAgentEnv(os.Environ()), runAsAgentEnv+"=1")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	return cmd, &out
}

// agentEnvVars are the variables config.LoadConfig reads. They override the
// config file, so the child must not inherit them from whoever runs the tests
// (FIREWALL_ENABLED=true would make it look for ufw). A test sets its own
// after this, and the last entry wins.
var agentEnvVars = []string{
	"LATITUDESH_AUTH_TOKEN", "PROJECT_ID", "FIREWALL_ID", "PUBLIC_IP",
	"AGENT_INTERVAL", "LOG_LEVEL", "UFW_BINARY", "FIREWALL_ENABLED",
}

func withoutAgentEnv(env []string) []string {
	var kept []string
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if !slices.Contains(agentEnvVars, name) {
			kept = append(kept, kv)
		}
	}
	return kept
}

// TestAgent_StopsOnSIGTERMDuringStalledRequest starts the agent against an API
// that accepts requests and never answers, and checks that SIGTERM stops it
// right away instead of after the 30s request timeout.
func TestAgent_StopsOnSIGTERMDuringStalledRequest(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the agent as a child process")
	}

	requested := make(chan struct{}, 1)
	released := make(chan struct{})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case requested <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-released:
		}
	}))
	defer api.Close()
	defer close(released)

	cmd, out := agentCommand(t, api.URL)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	select {
	case <-requested:
	case err := <-exited:
		t.Fatalf("agent exited before calling the API: %v\n%s", err, out)
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("agent never called the API\n%s", out)
	}

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("agent exited with %v\n%s", err, out)
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-exited
		t.Fatalf("agent still running 5s after SIGTERM: it waits for the stalled request\n%s", out)
	}

	logs := out.String()
	if !strings.Contains(logs, "received shutdown signal") {
		t.Errorf("no shutdown log line:\n%s", logs)
	}
	if strings.Contains(logs, "health check failed") || strings.Contains(logs, "cycle failed") {
		t.Errorf("the shutdown was logged as a failure:\n%s", logs)
	}
}

func TestAgent_CheckConfigRejectsBadInterval(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the agent as a child process")
	}
	cmd, out := agentCommand(t, "http://127.0.0.1:1", "-check-config")
	cmd.Env = append(cmd.Env, "AGENT_INTERVAL=30")

	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("-check-config with AGENT_INTERVAL=30: err = %v, want exit status 1\n%s", err, out)
	}
	if !strings.Contains(out.String(), `invalid interval "30"`) {
		t.Errorf("output does not name the bad interval:\n%s", out)
	}
}

func TestRunLoop_CancelInterruptsCycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	var reported atomic.Int32

	done := make(chan struct{})
	go func() {
		defer close(done)
		runLoop(ctx, time.Hour, func(ctx context.Context) error {
			close(started)
			<-ctx.Done() // a request that only ends when it is cancelled
			return ctx.Err()
		}, func(error) { reported.Add(1) })
	}()

	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runLoop did not return after ctx was cancelled")
	}
	if n := reported.Load(); n != 0 {
		t.Errorf("the cancelled cycle was reported as %d failure(s)", n)
	}
}

func TestRunLoop_RunsAtOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ran := make(chan struct{})
	go runLoop(ctx, time.Hour, func(context.Context) error {
		close(ran)
		cancel()
		return nil
	}, func(error) {})

	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("the first cycle waited for the ticker instead of running right away")
	}
}

func TestRunLoop_RunsOnEveryTickAndReportsFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var cycles, reported atomic.Int32
	runLoop(ctx, 10*time.Millisecond, func(context.Context) error {
		if cycles.Add(1) == 3 {
			cancel()
			return nil
		}
		return errors.New("api down")
	}, func(error) { reported.Add(1) })

	if cycles.Load() != 3 {
		t.Errorf("cycles = %d, want 3 (no cycle after cancel)", cycles.Load())
	}
	if reported.Load() != 2 {
		t.Errorf("reported = %d, want the 2 failed cycles", reported.Load())
	}
}
