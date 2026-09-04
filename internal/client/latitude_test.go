package client

import (
	"strings"
	"testing"

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
