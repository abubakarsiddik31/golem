#!/usr/bin/env bash
# Live smoke matrix: runs each adapter's opt-in integration tests when its
# credentials are present and skips it when they are not, then prints the
# matrix and exits nonzero if any run test failed. All-skip is success.
#
# Credentials come from the same GOLEM_* variables the integration tests
# themselves gate on (see providers/*/integration_test.go):
#
#   openai     GOLEM_OPENAI_API_KEY
#   azure      GOLEM_AZURE_OPENAI_API_KEY
#   anthropic  GOLEM_ANTHROPIC_API_KEY
#   gemini     GOLEM_GEMINI_API_KEY
#   bedrock    GOLEM_BEDROCK_ACCESS_KEY_ID, GOLEM_BEDROCK_SECRET_ACCESS_KEY,
#              GOLEM_BEDROCK_REGION, GOLEM_BEDROCK_MODEL
#
# Usage: scripts/smoke.sh [adapter ...]   # default: every adapter
set -u

cd "$(dirname "$0")/.."

# adapter|package|env-var whitelist, one per line
matrix='
openai|providers/openai|GOLEM_OPENAI_API_KEY
azure|providers/azure|GOLEM_AZURE_OPENAI_API_KEY
anthropic|providers/anthropic|GOLEM_ANTHROPIC_API_KEY
gemini|providers/gemini|GOLEM_GEMINI_API_KEY
bedrock|providers/bedrock|GOLEM_BEDROCK_ACCESS_KEY_ID GOLEM_BEDROCK_SECRET_ACCESS_KEY GOLEM_BEDROCK_REGION GOLEM_BEDROCK_MODEL
'

filter="${*:-}"
status=0
printf '%-10s %-8s %s\n' ADAPTER RESULT NOTES
printf '%-10s %-8s %s\n' ---------- -------- ------------------------
while IFS='|' read -r adapter package envvars; do
	[ -n "$adapter" ] || continue
	if [ -n "$filter" ]; then
		case " $filter " in
		*" $adapter "*) ;;
		*) continue ;;
		esac
	fi
	run=1
	missing=
	for var in $envvars; do
		eval "value=\${$var-}"
		if [ -z "$value" ]; then
			run=0
			missing="$missing $var"
		fi
	done
	if [ "$run" -eq 0 ]; then
		printf '%-10s %-8s missing:%s\n' "$adapter" SKIP "$missing"
		missing=
		continue
	fi
	echo ">>> go test -count=1 -run TestLive ./$package" >&2
	if go test -count=1 -run 'TestLive' "./$package"; then
		printf '%-10s %-8s\n' "$adapter" PASS
	else
		printf '%-10s %-8s\n' "$adapter" FAIL
		status=1
	fi
done <<<"$matrix"
exit $status
