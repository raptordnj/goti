#!/usr/bin/env bash
# check-brand.sh fails when the old brand ("swiftfetch", any case) appears in
# tracked files outside docs/ and the few places that must keep it: the data
# dir migration and deprecated-env fallback constants, their tests, the README
# upgrade note and this script. It also rejects the old Go module path.
set -euo pipefail
cd "$(dirname "$0")/.."

allowed=(
	internal/config/config.go          # LegacyDataDir
	internal/config/datadir.go         # LegacyEnvDataDir, LegacyDBName
	internal/config/datadir_test.go
	cmd/goti-cli/main_test.go          # legacy env fallback test
	README.md                          # "Upgrading from SwiftFetch"
	scripts/check-brand.sh
)

status=0
while IFS= read -r f; do
	ok=0
	for a in "${allowed[@]}"; do [[ "$f" == "$a" ]] && ok=1; done
	if [[ $ok == 0 ]]; then
		echo "check-brand: old brand in $f:" >&2
		git grep -n -I -i swiftfetch -- "$f" >&2
		status=1
	fi
done < <(git grep -l -I -i swiftfetch -- . ':!docs' ':!.superpowers' || true)

if git grep -q 'raptordnj/swiftfetch' -- '*.go' go.mod; then
	echo "check-brand: old module path in go.mod or imports" >&2
	status=1
fi
[[ $status == 0 ]] && echo "check-brand: ok"
exit $status
