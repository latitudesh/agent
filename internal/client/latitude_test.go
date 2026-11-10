package client

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func newTestClient(projectID, firewallID string) *LatitudeClient {
	logger := logrus.New()
	logger.SetOutput(nopWriter{})
	return NewLatitudeClient("", "https://api.latitude.sh/agent/ping", projectID, firewallID, "203.0.113.10", logger)
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestVerifyFirewallIdentity(t *testing.T) {
	const (
		myProject  = "proj_dexA0qZvzNlQV"
		myFirewall = "fw_3xMV0x2k5mQp7"
	)

	cases := []struct {
		name       string
		body       string
		wantErr    bool
		wantErrHas string
	}{
		{
			name: "matching firewall and project is accepted",
			body: `{"status":"success","server_id":"sv_1","project_id":"proj_dexA0qZvzNlQV",
			        "firewall":{"id":"fw_3xMV0x2k5mQp7","rules":[{"from":"1.1.1.1","protocol":"tcp","port":"22"}]}}`,
			wantErr: false,
		},
		{
			name: "stale cross-tenant firewall is refused",
			body: `{"status":"success","server_id":"sv_1","project_id":"proj_dexA0qZvzNlQV",
			        "firewall":{"id":"fw_7pWRawvE5rD6y","rules":[{"from":"100.64.0.0/10","protocol":"tcp","port":"8333"}]}}`,
			wantErr:    true,
			wantErrHas: "fw_7pWRawvE5rD6y",
		},
		{
			name: "mismatched project is refused",
			body: `{"status":"success","server_id":"sv_1","project_id":"proj_someoneelse",
			        "firewall":{"id":"fw_3xMV0x2k5mQp7","rules":[{"from":"1.1.1.1","protocol":"tcp","port":"22"}]}}`,
			wantErr:    true,
			wantErrHas: "proj_someoneelse",
		},
		{
			name:    "disabled firewall (empty object) is accepted",
			body:    `{"status":"success","server_id":"sv_1","project_id":"proj_dexA0qZvzNlQV","firewall":{}}`,
			wantErr: false,
		},
		{
			name:    "response without identity fields is refused (fail closed)",
			body:    `{"firewall":{"rules":[{"from":"1.1.1.1","protocol":"tcp","port":"22"}]}}`,
			wantErr: true,
		},
		{
			name:    "rules without a firewall id are refused (fail closed)",
			body:    `{"status":"success","project_id":"proj_dexA0qZvzNlQV","firewall":{"rules":[{"from":"1.1.1.1","protocol":"tcp","port":"22"}]}}`,
			wantErr: true,
		},
		{
			name:       "empty firewall for the wrong project is refused",
			body:       `{"status":"success","project_id":"proj_someoneelse","firewall":{}}`,
			wantErr:    true,
			wantErrHas: "proj_someoneelse",
		},
		{
			name:    "absent firewall field is refused (fail closed)",
			body:    `{"status":"success","server_id":"sv_1","project_id":"proj_dexA0qZvzNlQV"}`,
			wantErr: true,
		},
		{
			name:    "null firewall field is refused (fail closed)",
			body:    `{"status":"success","server_id":"sv_1","project_id":"proj_dexA0qZvzNlQV","firewall":null}`,
			wantErr: true,
		},
		{
			name:    "invalid JSON is rejected",
			body:    `{not json`,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lc := newTestClient(myProject, myFirewall)
			err := lc.VerifyFirewallIdentity(tc.body)
			if tc.wantErr && err == nil {
				t.Fatalf("expected an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if tc.wantErrHas != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErrHas)) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErrHas, err)
			}
		})
	}
}

func TestVerifyFirewallIdentity_WrongFirewallWithNoRules(t *testing.T) {
	lc := newTestClient("proj_me", "fw_me")
	body := `{"status":"success","project_id":"proj_me","firewall":{"id":"fw_other","rules":[]}}`
	if err := lc.VerifyFirewallIdentity(body); err == nil {
		t.Fatalf("expected refusal for wrong firewall id, got nil")
	}
}

// TestPingAndGetFirewallRules_StalledAPITimesOut guards the sync loop against
// an API call that never completes. The agent passes a context that is only
// cancelled on shutdown, so without a client timeout a server that accepts the
// connection but never answers blocks the cycle, and every cycle after it.
func TestPingAndGetFirewallRules_StalledAPITimesOut(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	logger := logrus.New()
	logger.SetOutput(nopWriter{})
	lc := NewLatitudeClient("", srv.URL, "proj_me", "fw_me", "203.0.113.10", logger)
	if lc.httpClient.Timeout != requestTimeout {
		t.Fatalf("client timeout = %v, want %v", lc.httpClient.Timeout, requestTimeout)
	}
	lc.httpClient.Timeout = 200 * time.Millisecond

	done := make(chan error, 1)
	go func() {
		_, err := lc.PingAndGetFirewallRules(context.Background())
		done <- err
	}()

	select {
	case err := <-done:
		var netErr net.Error
		if !errors.As(err, &netErr) || !netErr.Timeout() {
			t.Fatalf("expected a timeout error from a stalled API, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PingAndGetFirewallRules did not return: a stalled API blocks the sync loop")
	}
}

func TestPingAndGetFirewallRules_ConnectsOverIPv4Only(t *testing.T) {
	cases := []struct {
		network, addr string
		wantErr       bool
	}{
		{"tcp4", "127.0.0.1:0", false},
		{"tcp6", "[::1]:0", true},
	}
	for _, tc := range cases {
		t.Run(tc.network, func(t *testing.T) {
			ln, err := net.Listen(tc.network, tc.addr)
			if err != nil {
				t.Skipf("%s loopback unavailable: %v", tc.network, err)
			}
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"firewall":{}}`))
			}))
			_ = srv.Listener.Close()
			srv.Listener = ln
			srv.Start()
			defer srv.Close()

			logger := logrus.New()
			logger.SetOutput(nopWriter{})
			lc := NewLatitudeClient("", srv.URL, "proj_me", "fw_me", "203.0.113.10", logger)

			_, err = lc.PingAndGetFirewallRules(context.Background())
			if gotErr := err != nil; gotErr != tc.wantErr {
				t.Fatalf("PingAndGetFirewallRules over %s: err = %v, want error = %v", tc.network, err, tc.wantErr)
			}
		})
	}
}
