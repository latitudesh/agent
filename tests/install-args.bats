#!/usr/bin/env bats
# install.sh: argument parsing and the root check. These run the real script,
# which stops before touching the host (missing args, unknown flag, not root).

load lib/agent-test

setup() {
  setup_scratch
  if [ "$(id -u)" = 0 ]; then
    skip "running as root: install.sh would really install"
  fi
}

@test "no arguments: usage, exit 1" {
  run bash "$INSTALL_SH"
  [ "$status" -eq 1 ]
  [[ "$output" == *"Both Firewall ID and Project ID are required"* ]]
  [[ "$output" == *"Usage:"* ]]
}

@test "only -firewall: still requires -project" {
  run bash "$INSTALL_SH" -firewall fw_1
  [ "$status" -eq 1 ]
  [[ "$output" == *"required"* ]]
}

@test "only -project: still requires -firewall" {
  run bash "$INSTALL_SH" -project proj_1
  [ "$status" -eq 1 ]
  [[ "$output" == *"required"* ]]
}

@test "an unknown flag prints usage" {
  run bash "$INSTALL_SH" -firewall fw_1 -project proj_1 --bogus x
  [ "$status" -eq 1 ]
  [[ "$output" == *"Usage:"* ]]
}

@test "valid arguments as non-root: refuses before changing anything" {
  run bash "$INSTALL_SH" -firewall fw_1 -project proj_1 -version v1.2.3
  [ "$status" -eq 1 ]
  [[ "$output" == *"Please run as root"* ]]
}

@test "-version accepts a leading v (v1.2.3 -> 1.2.3)" {
  assert_contains "$INSTALL_SH" 'AGENT_VERSION="${2#v}"'
}
