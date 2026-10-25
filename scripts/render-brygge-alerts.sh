#!/usr/bin/env bash
# Render the Brygge alert rules for one deployment.
#
#   render-brygge-alerts.sh [--groups] <service_namespace> <club_slug> [runbook_base]
#
# Prints a PrometheusRule CR to stdout, or with --groups the plain rule groups
# (what promtool reads). See docs/developer/otel/alerting.md.
#
# Environment:
#   BRYGGE_ALERTS_LABELS     metadata.labels for the CR, comma-separated k=v
#                            (match the Prometheus ruleSelector, e.g.
#                            "release=kube-prometheus-stack"). Default: none.
#   BRYGGE_ALERTS_NAMESPACE  metadata.namespace for the CR. Default: omitted.
#   BRYGGE_ALERTS_SOURCE     rules file. Default: the one in docs/developer/otel/alerts.
set -euo pipefail

usage() {
  sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//' >&2
  exit 2
}

groups_only=false
if [ "${1:-}" = "--groups" ]; then
  groups_only=true
  shift
fi
[ $# -ge 2 ] && [ $# -le 3 ] || usage

namespace="$1"
slug="$2"
runbook_base="${3:-https://github.com/ryanjdillon/brygge/blob/main/docs/developer/otel/alerts-runbook.md}"

script_dir="$(cd "$(dirname "$0")" && pwd)"
source_file="${BRYGGE_ALERTS_SOURCE:-$script_dir/../docs/developer/otel/alerts/brygge-alerts.rules.yaml}"

# The values land unquoted in YAML and inside PromQL label matchers, so keep
# them to characters that are safe in both.
if ! [[ "$namespace" =~ ^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$ ]]; then
  echo "service_namespace must be a lowercase domain name: $namespace" >&2
  exit 2
fi
if ! [[ "$slug" =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ ]]; then
  echo "club_slug must be lowercase alphanumerics and dashes: $slug" >&2
  exit 2
fi
if ! [[ "$runbook_base" =~ ^[A-Za-z0-9._~:/?=@%+-]+$ ]]; then
  echo "runbook_base contains characters that need quoting: $runbook_base" >&2
  exit 2
fi

# '|' can't occur in any of the values (checked above), so it is a safe sed
# delimiter; '&' could only come from runbook_base and is excluded too.
rendered="$(sed \
  -e "s|__SERVICE_NAMESPACE__|$namespace|g" \
  -e "s|__CLUB_SLUG__|$slug|g" \
  -e "s|__RUNBOOK_BASE__|$runbook_base|g" \
  "$source_file")"

if leftover="$(grep -o '__[A-Z_]*__' <<<"$rendered" | sort -u)" && [ -n "$leftover" ]; then
  echo "unsubstituted placeholders: $leftover" >&2
  exit 1
fi

if $groups_only; then
  printf '%s\n' "$rendered"
  exit 0
fi

echo "apiVersion: monitoring.coreos.com/v1"
echo "kind: PrometheusRule"
echo "metadata:"
echo "  name: brygge-$slug"
if [ -n "${BRYGGE_ALERTS_NAMESPACE:-}" ]; then
  echo "  namespace: $BRYGGE_ALERTS_NAMESPACE"
fi
if [ -n "${BRYGGE_ALERTS_LABELS:-}" ]; then
  echo "  labels:"
  IFS=',' read -ra pairs <<<"$BRYGGE_ALERTS_LABELS"
  for pair in "${pairs[@]}"; do
    key="${pair%%=*}"
    value="${pair#*=}"
    if [ "$key" = "$pair" ] || ! [[ "$key" =~ ^[A-Za-z0-9_./-]+$ ]] || [[ "$value" == *[\"\\]* ]]; then
      echo "BRYGGE_ALERTS_LABELS entry is not k=v with a plain key and an unquoted value: $pair" >&2
      exit 2
    fi
    printf '    %s: "%s"\n' "$key" "$value"
  done
fi
echo "spec:"
# Drop the source's header comments and indent everything under spec:,
# leaving blank lines blank.
printf '%s\n' "$rendered" | sed -e '/^#/d' -e '/./s/^/  /'
