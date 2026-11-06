#!/usr/bin/env bats
# scripts/build-landing-page.sh: the package tables on packages.lsh.io.

bats_require_minimum_version 1.5.0

load lib/agent-test

setup() {
  setup_scratch
  APT="$BATS_TEST_TMPDIR/apt"
  RPM="$BATS_TEST_TMPDIR/rpm"
  mkdir -p "$APT/dists/stable/main/binary-amd64" "$RPM/packages"
  cat > "$APT/dists/stable/main/binary-amd64/Packages" << 'PKG'
Package: lsh-agent
Version: 1.4.0
Architecture: amd64
Filename: pool/main/l/lsh-agent/lsh-agent_1.4.0_amd64.deb
Size: 2936012
SHA256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
PKG
  printf 'x' > "$RPM/packages/lsh-agent-1.3.9-1.x86_64.rpm"
  printf 'x' > "$RPM/packages/lsh-agent-1.4.0-1.aarch64.rpm"
  printf 'x' > "$RPM/packages/lsh-agent-1.4.0-1.x86_64.rpm"
  TEMPLATE="$REPO_ROOT/packaging/site/index.html"
  OUT="$BATS_TEST_TMPDIR/index.html"
}

@test "lists the .deb and .rpm packages and leaves no placeholder" {
  run bash "$REPO_ROOT/scripts/build-landing-page.sh" "$APT" "$TEMPLATE" "$OUT" "$RPM"
  [ "$status" -eq 0 ]
  grep -q '<a href="/apt/pool/main/l/lsh-agent/lsh-agent_1.4.0_amd64.deb">' "$OUT"
  grep -q '<td>1.4.0</td><td>x86_64</td><td><a href="/rpm/packages/lsh-agent-1.4.0-1.x86_64.rpm">' "$OUT"
  grep -q '<td>1.4.0</td><td>aarch64</td>' "$OUT"
  run ! grep -q 'PACKAGES -->' "$OUT"
}

@test "rpm rows are newest version first" {
  bash "$REPO_ROOT/scripts/build-landing-page.sh" "$APT" "$TEMPLATE" "$OUT" "$RPM"
  first=$(grep -n 'lsh-agent-1.4.0-1.x86_64.rpm' "$OUT" | cut -d: -f1)
  older=$(grep -n 'lsh-agent-1.3.9-1.x86_64.rpm' "$OUT" | cut -d: -f1)
  [ "$first" -lt "$older" ]
}

@test "without an rpm repository the dnf setup and table are left out (releases before the first .rpm)" {
  run bash "$REPO_ROOT/scripts/build-landing-page.sh" "$APT" "$TEMPLATE" "$OUT"
  [ "$status" -eq 0 ]
  run ! grep -q 'PACKAGES -->' "$OUT"
  run ! grep -q '/rpm/lsh-agent.repo' "$OUT"
  run ! grep -q '/rpm/packages/' "$OUT"
  run ! grep -q 'Package (.rpm)' "$OUT"
  run ! grep -q 'RPM_' "$OUT"
  grep -q '/apt/lsh-agent.sources' "$OUT"
  grep -q 'Ubuntu 22.04+ and Debian 12+, on amd64 and arm64.</p>' "$OUT"
}

@test "with an rpm repository the dnf setup is shown and no marker is left" {
  bash "$REPO_ROOT/scripts/build-landing-page.sh" "$APT" "$TEMPLATE" "$OUT" "$RPM"
  grep -q 'https://packages.lsh.io/rpm/lsh-agent.repo' "$OUT"
  grep -q 'Package (.rpm)' "$OUT"
  grep -q 'Rocky Linux and Oracle Linux (dnf)' "$OUT"
  run ! grep -q 'RPM_' "$OUT"
}

@test "an rpm repository with no packages is an error" {
  rm "$RPM"/packages/*
  run bash "$REPO_ROOT/scripts/build-landing-page.sh" "$APT" "$TEMPLATE" "$OUT" "$RPM"
  [ "$status" -ne 0 ]
}
