#!/usr/bin/env bats
# Network retries: every apt/dnf/yum/curl/git fetch in the installer, the test
# harnesses and the workflows carries the retry policy tinkerbell-packer-images
# uses (apt Acquire::Retries=5 + 30s timeouts, dnf retries=10/timeout=30/
# minrate=1000, curl --retry 5 --retry-delay 3 --retry-connrefused). A
# transient mirror/CDN error, e.g. a distro mirror mid-sync, must not fail an
# install or a CI run.

load lib/agent-test

setup() {
  setup_scratch
}

# Code lines matching $2 in file $1 that lack $3. Comments and messages
# (echo/printf, e.g. "apt-get install failed") are not calls, so skipped.
lines_missing() {
  grep -nE -- "$2" "$1" | grep -vE '^[0-9]+:[[:space:]]*(#|echo |printf )' | grep -vF -- "$3" || true
}

# --- the policy itself ------------------------------------------------------

@test "install.sh defines the standard retry policy" {
  assert_contains "$INSTALL_SH" 'APT_RETRY_OPTS=(-o Acquire::Retries=5 -o Acquire::Retries::Delay=true -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30)'
  assert_contains "$INSTALL_SH" 'DNF_RETRY_OPTS=(--setopt=retries=10 --setopt=timeout=30 --setopt=minrate=1000)'
  assert_contains "$INSTALL_SH" 'CURL_RETRY_OPTS=(--retry 5 --retry-delay 3 --retry-connrefused)'
}

@test "install.sh never writes the retry policy into the host's apt/dnf config" {
  assert_lacks_code_re "$INSTALL_SH" 'apt\.conf\.d|dnf\.conf|yum\.conf'
}

# --- install.sh: every fetch uses it ----------------------------------------

@test "install.sh: every apt-get update/install passes APT_RETRY_OPTS" {
  run lines_missing "$INSTALL_SH" 'apt-get .*(update|install)' '"${APT_RETRY_OPTS[@]}"'
  [ -z "$output" ] || { echo "$output"; false; }
}

@test "install.sh: every dnf/yum install passes DNF_RETRY_OPTS" {
  run lines_missing "$INSTALL_SH" '"\$RPM_PM" .*install' '"${DNF_RETRY_OPTS[@]}"'
  [ -z "$output" ] || { echo "$output"; false; }
}

@test "install.sh: every curl fails on HTTP errors and passes CURL_RETRY_OPTS" {
  run lines_missing "$INSTALL_SH" '(^|[[:space:]])curl ' '"${CURL_RETRY_OPTS[@]}"'
  [ -z "$output" ] || { echo "$output"; false; }
  run lines_missing "$INSTALL_SH" '(^|[[:space:]])curl ' ' -f'
  [ -z "$output" ] || { echo "$output"; false; }
}

@test "install.sh: the source build retries git clone" {
  run grep -n -B3 -A6 'git clone' "$INSTALL_SH"
  [[ "$output" == *"for attempt in 1 2 3"* ]]
  [[ "$output" == *"&& break"* ]]
}

@test "install_package passes the apt policy on Debian (behaviour)" {
  fn="$(retry_policy)"$'\n'"$(extract_fn "$INSTALL_SH" install_package)"
  stub apt-get
  run bash -c 'PATH="$1"; OS_FAMILY=debian; eval "$2"; install_package ufw jq' _ "$(stub_path)" "$fn"
  [ "$status" -eq 0 ]
  called "apt-get -o Acquire::Retries=5 -o Acquire::Retries::Delay=true -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30 update"
  called "apt-get -o Acquire::Retries=5 -o Acquire::Retries::Delay=true -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30 install -y ufw jq"
}

@test "install_package passes the dnf policy on the RHEL family (behaviour)" {
  fn="$(retry_policy)"$'\n'"$(extract_fn "$INSTALL_SH" install_package)"
  stub yum
  run bash -c 'PATH="$1"; OS_FAMILY=rhel RPM_PM=yum; eval "$2"; install_package gcc make' _ "$(stub_path)" "$fn"
  [ "$status" -eq 0 ]
  called "yum --setopt=retries=10 --setopt=timeout=30 --setopt=minrate=1000 install -y gcc make"
}

# --- test harnesses and workflows -------------------------------------------

@test "test harnesses: every apt-get update/install retries (policy + outer retry)" {
  for f in "$REPO_ROOT"/scripts/test-*.sh; do
    run lines_missing "$f" 'apt-get .*(update|install)' 'Acquire::Retries=5'
    # test-apt-install.sh routes its calls through apt_net(); test-install-sh.sh
    # passes $net. Both carry the policy (asserted below).
    output="$(grep -vE 'apt_net|apt-get \$net ' <<< "$output" || true)"
    [ -z "$output" ] || { echo "$f: $output"; false; }
  done
  assert_contains "$REPO_ROOT/scripts/test-install-sh.sh" 'net="-o Acquire::Retries=5 -o Acquire::Retries::Delay=true -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30"'
  assert_contains "$REPO_ROOT/scripts/test-apt-install.sh" 'for attempt in 1 2 3; do'
  assert_contains "$REPO_ROOT/scripts/test-install-sh.sh" 'for attempt in 1 2 3; do'
}

@test "workflows: every apt-get retries and every curl uses the curl policy" {
  for f in "$REPO_ROOT"/.github/workflows/*.yml; do
    run lines_missing "$f" 'apt-get .*(update|install)' 'Acquire::Retries=5'
    output="$(grep -vF '"${net[@]}"' <<< "$output" || true)"
    [ -z "$output" ] || { echo "$f: $output"; false; }
    run lines_missing "$f" '(^|[[:space:]])curl ' '--retry 5 --retry-delay 3 --retry-connrefused'
    [ -z "$output" ] || { echo "$f: $output"; false; }
  done
}
