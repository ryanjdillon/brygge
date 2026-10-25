#!/usr/bin/env bash
# Render the alert rules for a placeholder deployment and run promtool's
# syntax check and unit tests on them. Needs promtool on PATH (the dev shell
# provides it). Used by `just alerts-check` and the flake's alerts check.
set -euo pipefail

script_dir="$(cd "$(dirname "$0")" && pwd)"
alerts_dir="${BRYGGE_ALERTS_DIR:-$script_dir/../docs/developer/otel/alerts}"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

render() {
  BRYGGE_ALERTS_SOURCE="$alerts_dir/brygge-alerts.rules.yaml" \
    bash "$script_dir/render-brygge-alerts.sh" "$@" \
    example.invalid example https://example.invalid/runbook
}

render --groups >"$work/brygge-alerts.rendered.yaml"
cp "$alerts_dir/brygge-alerts.test.yaml" "$work/"

# The CR wrapper must render too, even though promtool can't read it.
render >/dev/null

promtool check rules "$work/brygge-alerts.rendered.yaml"
promtool test rules "$work/brygge-alerts.test.yaml"
