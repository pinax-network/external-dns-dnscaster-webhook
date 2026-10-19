#!/usr/bin/env bash
# Delete every DNScaster host of record TYPE whose properties match all the
# given KEY=VALUE tags, or every host of that TYPE in every zone when no tags
# are given. Matches are only listed unless --apply is passed.
#
# Usage: DNSCASTER_API_KEY=... hack/delete-records.sh [--apply] TYPE [KEY=VALUE...]
#   TYPE is a record type such as A, AAAA, CNAME or TXT, or a list like A,AAAA
#   e.g. hack/delete-records.sh TXT owner-id=riv-stage-firehose-a
#        hack/delete-records.sh CNAME
#
# Each list page and each delete is one call against the API's shared
# 2000 req/h budget, so deletes run one at a time and stop at the first error.
# The API refuses to list hosts without a filter, so with no tags the hosts
# are listed zone by zone: one call for the zones plus at least one per zone.
set -euo pipefail
shopt -s inherit_errexit

api_url=https://api.dnscaster.com/v1/

usage() {
	sed -n '2,9p' "$0" | sed 's/^# \{0,1\}//'
	exit "${1:-0}"
}

apply=false
type=
tags=()
for arg in "$@"; do
	case "$arg" in
	--apply) apply=true ;;
	-h | --help) usage ;;
	?*=*) tags+=("$arg") ;;
	-* | *=*)
		echo "unexpected argument: $arg" >&2
		usage 1 >&2
		;;
	*)
		if [[ -n $type ]]; then
			echo "only one TYPE is allowed, got $type and $arg" >&2
			usage 1 >&2
		fi
		type=${arg^^}
		;;
	esac
done
[[ -n $type ]] || usage 1 >&2
: "${DNSCASTER_API_KEY:?must be set}"

api() {
	curl -sS -g --fail-with-body \
		-H "Authorization: Bearer $DNSCASTER_API_KEY" \
		-H "Accept: application/json" \
		-H "User-Agent: external-dns-dnscaster-webhook/hack (+https://github.com/pinax-network/external-dns-dnscaster-webhook)" \
		"$@"
}

# list_all PATH [CURL_ARGS...] prints every record of a paged list endpoint,
# one JSON object per line. Each page starts after the last ID seen.
list_all() {
	local path=$1 after='' page last
	shift
	while :; do
		local args=("$@" --data-urlencode max_results=1000)
		[[ -z $after ]] || args+=(--data-urlencode "after=$after")
		if ! page=$(api -G "${args[@]}" "$api_url$path/"); then
			echo "listing $path failed: $page" >&2
			exit 1
		fi
		jq -c '.collection[]' <<<"$page"
		[[ $(jq -r .more_results <<<"$page") == true ]] || return 0

		last=$(jq -r '.collection[-1].id // empty' <<<"$page")
		if [[ -z $last || $last == "$after" ]]; then
			echo "paging $path stalled after ${after:-<none>}; aborting" >&2
			exit 1
		fi
		after=$last
	done
}

# The API ANDs property filters; props mirrors them for the local check below.
query=(--data-urlencode "dns_type=$type")
props='{}'
for tag in "${tags[@]}"; do
	key=${tag%%=*} value=${tag#*=}
	query+=(--data-urlencode "properties[$key]=$value")
	props=$(jq -c --arg k "$key" --arg v "$value" '. + {($k): $v}' <<<"$props")
done

if ((${#tags[@]} > 0)); then
	hosts=$(list_all hosts "${query[@]}")
	scope=${tags[*]}
else
	zones=$(list_all zones)
	hosts=
	while read -r zone_id; do
		hosts+=$(list_all hosts "${query[@]}" --data-urlencode "zone_id=$zone_id")$'\n'
	done < <(jq -r .id <<<"$zones")
	scope="all $(jq -s length <<<"$zones") zone(s)"
fi

# Re-check the filters locally so an ignored query parameter can never widen
# the deletion to other record types or owners.
matches=$(jq -sc --arg types "$type" --argjson p "$props" '
	($types | split(",")) as $t
	| [.[] | select(.dns_type | IN($t[]))
	  | (.properties // {}) as $hp
	  | select(all($p | to_entries[]; $hp[.key] == .value))]
' <<<"$hosts")
count=$(jq length <<<"$matches")

jq -r '.[] | [.id, .dns_type, .fqdn, .data] | @tsv' <<<"$matches"
echo "$count $type record(s) match $scope" >&2

if [[ $apply != true ]]; then
	((count == 0)) || echo "re-run with --apply to delete them" >&2
	exit 0
fi

deleted=0
while IFS=$'\t' read -r id fqdn; do
	if ! api -X DELETE "${api_url}hosts/$id"; then
		echo "failed to delete $fqdn ($id); stopping after $deleted of $count" >&2
		exit 1
	fi
	deleted=$((deleted + 1))
	echo "deleted $fqdn ($id)"
done < <(jq -r '.[] | [.id, .fqdn] | @tsv' <<<"$matches")

echo "deleted $deleted of $count record(s)" >&2
