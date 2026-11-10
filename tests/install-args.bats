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

@test "-version pins the package the same way on both families (v prefix dropped)" {
  assert_contains "$INSTALL_SH" 'AGENT_VERSION="${2#v}"'
  assert_contains "$INSTALL_SH" '"lsh-agent${AGENT_VERSION:+=$AGENT_VERSION}"'
  assert_contains "$INSTALL_SH" 'install -y "lsh-agent${AGENT_VERSION:+-$AGENT_VERSION}"'
}

# --- argument validation (the values end up in /etc/lsh-agent/env) ----------

@test "IDs with shell metacharacters or a newline are rejected before anything runs" {
  local bad
  for bad in 'p1;touch /tmp/x' 'p$(id)' 'p`id`' $'p1\nFIREWALL_ID=fw_x' 'p 1' "p'1"; do
    run bash "$INSTALL_SH" -firewall fw_1 -project "$bad"
    [ "$status" -eq 1 ]
    [[ "$output" == *"may only contain letters, digits"* ]]
    run bash "$INSTALL_SH" -firewall "$bad" -project proj_1
    [ "$status" -eq 1 ]
    [[ "$output" == *"may only contain letters, digits"* ]]
  done
}

@test "an invalid -public_ip is rejected" {
  local bad
  for bad in '203.0.113.5;id' '256.1.1.1' '1.2.3' 'example.com' $'203.0.113.5\nX=1' '$(id)'; do
    run bash "$INSTALL_SH" -firewall fw_1 -project proj_1 -public_ip "$bad"
    [ "$status" -eq 1 ]
    [[ "$output" == *"-public_ip must be an IPv4 or IPv6 address"* ]]
  done
}

@test "valid IDs and IPs pass validation (and stop at the root check)" {
  local ip
  for ip in 203.0.113.5 2001:db8::1 ::ffff:192.0.2.1; do
    run bash "$INSTALL_SH" -firewall fw_a.b-1 -project proj_X_9 -public_ip "$ip"
    [ "$status" -eq 1 ]
    [[ "$output" == *"Please run as root"* ]]
  done
}

@test "write_env_file writes plain KEY=value lines, mode 0644" {
  eval "$(extract_fn "$INSTALL_SH" write_env_file)"
  mkdir -p "$ROOT/etc/lsh-agent"
  # shellcheck disable=SC2034  # read by the extracted write_env_file
  FIREWALL_ID=fw_1 PROJECT_ID=proj_1 PUBLIC_IP=203.0.113.5
  umask 077
  write_env_file "$ROOT/etc/lsh-agent/env"
  [ "$(cat "$ROOT/etc/lsh-agent/env")" = $'FIREWALL_ID=fw_1\nPROJECT_ID=proj_1\nPUBLIC_IP=203.0.113.5' ]
  [ "$(stat -c %a "$ROOT/etc/lsh-agent/env")" = 644 ]
  [ ! -e "$ROOT/etc/lsh-agent/env.tmp" ]
}

@test "the env file is written only through write_env_file" {
  assert_lacks_code_re "$INSTALL_SH" 'echo .*>>? */etc/lsh-agent/env'
  assert_contains "$INSTALL_SH" 'write_env_file /etc/lsh-agent/env'
}

@test "valid_ip checks the structure of IPv6 addresses" {
  eval "$(extract_fn "$INSTALL_SH" valid_ipv4 valid_ip)"
  local ip
  for ip in 203.0.113.5 0.0.0.0 :: ::1 1:: 2001:db8::1 1:2:3:4:5:6:7:8 fe80::1:2 \
            ::ffff:192.0.2.1 64:ff9b::198.51.100.7 1:2:3:4:5:6:192.0.2.1 2001:DB8:0:0:0:0:0:1; do
    valid_ip "$ip" || { echo "rejected valid $ip"; return 1; }
  done
  for ip in : ::: 2001:::1 1::2::3 :1:2:3:4:5:6:7:8 1:2:3:4:5:6:7:8: 1:2:3:4:5:6:7 \
            1:2:3:4:5:6:7:8:9 12345::1 g::1 1:2:3:4:5:6:7:192.0.2.1 ::ffff:256.0.0.1 \
            ::ffff:1.2.3 1.2.3.4:: 2001:db8::1.2.3.4.5 256.1.1.1 1.2.3 .; do
    if valid_ip "$ip"; then echo "accepted invalid $ip"; return 1; fi
  done
}
