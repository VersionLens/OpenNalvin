#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "$SCRIPT_DIR/.." && pwd)
cd "$REPO_ROOT"

provider_a="default"
provider_b="openai"
iterations=8
warmups=1
workspace=""
nalvin_bin=""
prompt='Reply with exactly TTFT_BENCH_OK and nothing else.'

usage() {
	cat <<'EOF'
Benchmark agent-run TTFT across two providers by alternating timed runs.

Usage:
  ./scripts/benchmark-agent-run-ttft.sh [options]

Options:
  --provider-a NAME    First provider to compare. Default: default
  --provider-b NAME    Second provider to compare. Default: openai
  --iterations N       Measured runs per provider. Default: 8
  --warmups N          Warm-up runs per provider. Default: 1
  --workspace NAME     Optional workspace override for each run
  --prompt TEXT        Prompt to use for every run
  --nalvin-bin PATH    nalvin binary to run. Default: go run .
  -h, --help           Show this help

Example:
  ./scripts/benchmark-agent-run-ttft.sh --iterations 4 --warmups 1
EOF
}

while [[ $# -gt 0 ]]; do
	case "$1" in
		--provider-a)
			provider_a=${2:-}
			shift 2
			;;
		--provider-b)
			provider_b=${2:-}
			shift 2
			;;
		--iterations)
			iterations=${2:-}
			shift 2
			;;
		--warmups)
			warmups=${2:-}
			shift 2
			;;
		--workspace)
			workspace=${2:-}
			shift 2
			;;
		--prompt)
			prompt=${2:-}
			shift 2
			;;
		--nalvin-bin)
			nalvin_bin=${2:-}
			shift 2
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			echo "unknown argument: $1" >&2
			usage >&2
			exit 1
			;;
	esac
done

if ! [[ "$iterations" =~ ^[0-9]+$ ]] || ! [[ "$warmups" =~ ^[0-9]+$ ]]; then
	echo "--iterations and --warmups must be non-negative integers" >&2
	exit 1
fi

if [[ -n "$nalvin_bin" ]]; then
	nalvin_cmd=("$nalvin_bin")
else
	nalvin_cmd=(go run .)
fi

results_file=$(mktemp)
trap 'rm -f "$results_file"' EXIT

strip_ansi() {
	python3 -c 'import re, sys; print(re.sub(r"\x1b\[[0-9;]*m", "", sys.stdin.read()), end="")'
}

run_once() {
	local provider=$1
	local phase=$2
	local iter=$3
	local -a cmd=("${nalvin_cmd[@]}")

	if [[ -n "$workspace" ]]; then
		cmd+=(--workspace "$workspace")
	fi
	cmd+=(agent run --provider "$provider" --timing -p "$prompt")

	local out
	out=$("${cmd[@]}" 2>&1 | strip_ansi)

	local server_ttft
	server_ttft=$(printf '%s\n' "$out" | sed -n 's/^\[timing\] server TTFT: \([0-9][0-9]*\)ms$/\1/p' | tail -n1)
	local server_content_ttft
	server_content_ttft=$(printf '%s\n' "$out" | sed -n 's/^\[timing\] server content TTFT: \([0-9][0-9]*\)ms$/\1/p' | tail -n1)
	local local_ttft
	local_ttft=$(printf '%s\n' "$out" | sed -n 's/^\[timing\] local end-to-end TTFT: \([0-9][0-9]*\)ms$/\1/p' | tail -n1)
	local local_content_ttft
	local_content_ttft=$(printf '%s\n' "$out" | sed -n 's/^\[timing\] local content TTFT: \([0-9][0-9]*\)ms$/\1/p' | tail -n1)
	local first_kind
	first_kind=$(printf '%s\n' "$out" | sed -n 's/^\[timing\] first meaningful stream event=\([^ ]*\) at .*$/\1/p' | tail -n1)
	local run_id
	run_id=$(printf '%s\n' "$out" | sed -n 's/^Run ID: \(.*\)$/\1/p' | tail -n1)

	if [[ -z "$server_ttft" || -z "$server_content_ttft" || -z "$local_ttft" || -z "$local_content_ttft" || -z "$run_id" ]]; then
		echo "failed to parse timing output for provider=$provider phase=$phase iter=$iter" >&2
		printf '%s\n' "$out" >&2
		exit 1
	fi

	printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
		"$provider" "$phase" "$iter" "$first_kind" "$server_ttft" "$server_content_ttft" "$local_ttft" "$local_content_ttft" "$run_id" >>"$results_file"

	printf 'RESULT provider=%s phase=%s iter=%s first_kind=%s server_ttft_ms=%s server_content_ttft_ms=%s local_ttft_ms=%s local_content_ttft_ms=%s run_id=%s\n' \
		"$provider" "$phase" "$iter" "$first_kind" "$server_ttft" "$server_content_ttft" "$local_ttft" "$local_content_ttft" "$run_id"
}

echo "Benchmarking providers with agent run --timing"
echo "provider_a=$provider_a provider_b=$provider_b iterations=$iterations warmups=$warmups"
if [[ -n "$workspace" ]]; then
	echo "workspace=$workspace"
fi
printf 'nalvin_cmd='
printf '%q ' "${nalvin_cmd[@]}"
printf '\n'
echo

for ((i = 1; i <= warmups; i++)); do
	run_once "$provider_a" warmup "$i"
	run_once "$provider_b" warmup "$i"
done

for ((i = 1; i <= iterations; i++)); do
	run_once "$provider_a" measure "$i"
	run_once "$provider_b" measure "$i"
done

python3 - "$results_file" "$provider_a" "$provider_b" <<'PY'
import statistics
import sys
from collections import Counter, defaultdict

results_path, provider_a, provider_b = sys.argv[1:]
rows = []
with open(results_path, "r", encoding="utf-8") as fh:
    for line in fh:
        provider, phase, iteration, first_kind, server_ttft, server_content_ttft, local_ttft, local_content_ttft, run_id = line.rstrip("\n").split("\t")
        rows.append(
            {
                "provider": provider,
                "phase": phase,
                "iteration": int(iteration),
                "first_kind": first_kind or "(none)",
                "server_ttft": int(server_ttft),
                "server_content_ttft": int(server_content_ttft),
                "local_ttft": int(local_ttft),
                "local_content_ttft": int(local_content_ttft),
                "run_id": run_id,
            }
        )

measured = [row for row in rows if row["phase"] == "measure"]
by_provider = defaultdict(list)
for row in measured:
    by_provider[row["provider"]].append(row)

def fmt_ms(value: float) -> str:
    if float(value).is_integer():
        return f"{int(value)} ms"
    return f"{value:.1f} ms"

print()
print("Summary")
print(f"{'provider':<12} {'n':>2} {'median server TTFT':>20} {'median content TTFT':>22} {'best':>10} {'worst':>10}")
for provider in (provider_a, provider_b):
    data = by_provider.get(provider, [])
    if not data:
        print(f"{provider:<12} {'0':>2} {'n/a':>20} {'n/a':>22} {'n/a':>10} {'n/a':>10}")
        continue
    server_ttft = [row["server_ttft"] for row in data]
    server_content_ttft = [row["server_content_ttft"] for row in data]
    print(
        f"{provider:<12} "
        f"{len(data):>2} "
        f"{fmt_ms(statistics.median(server_ttft)):>20} "
        f"{fmt_ms(statistics.median(server_content_ttft)):>22} "
        f"{fmt_ms(min(server_ttft)):>10} "
        f"{fmt_ms(max(server_ttft)):>10}"
    )

print()
print("Measured server TTFT samples")
for provider in (provider_a, provider_b):
    data = by_provider.get(provider, [])
    samples = ", ".join(str(row["server_ttft"]) for row in data) if data else "n/a"
    print(f"{provider}: {samples}")

print()
print("First meaningful event counts")
for provider in (provider_a, provider_b):
    kinds = Counter(row["first_kind"] for row in by_provider.get(provider, []))
    if not kinds:
        print(f"{provider}: n/a")
        continue
    rendered = ", ".join(f"{kind}={count}" for kind, count in sorted(kinds.items()))
    print(f"{provider}: {rendered}")

data_a = by_provider.get(provider_a, [])
data_b = by_provider.get(provider_b, [])
if data_a and data_b:
    median_a = statistics.median(row["server_ttft"] for row in data_a)
    median_b = statistics.median(row["server_ttft"] for row in data_b)
    if median_a > 0 and median_b > 0:
        faster = provider_a if median_a < median_b else provider_b
        slower = provider_b if faster == provider_a else provider_a
        faster_median = min(median_a, median_b)
        slower_median = max(median_a, median_b)
        print()
        print(
            f"Median TTFT comparison: {faster} is about {slower_median / faster_median:.2f}x faster "
            f"than {slower} ({fmt_ms(faster_median)} vs {fmt_ms(slower_median)})."
        )
PY
