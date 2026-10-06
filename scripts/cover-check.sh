#!/bin/sh
# F5: per-package statement-coverage floor for the security-critical packages.
#
# `make cover` reports a whole-tree number, which a well-covered package can
# mask a regression in a weakly-covered one behind. This gate pins a
# conservative floor (a few points under the measured baseline) on the
# packages where a silent hole matters most, so a regression fails CI rather
# than hiding in an aggregate.
#
# Usage: `make cover-check` (or `sh scripts/cover-check.sh`) from the repo
# root. Bump a floor deliberately when coverage improves; lowering one is a
# decision that should be explained in the commit.
#
# `internal/gate` is intentionally absent: it is test-only (its "code" is the
# executable gates themselves), so `go test -cover` reports no statements.

set -eu

CGO_ENABLED=1
export CGO_ENABLED
GO_TAGS="-tags sqlite_fts5"

# "<module-relative package>:<floor percent>" pairs.
FLOORS="
internal/airlock/paths:60
internal/gateway:80
internal/toolenvelope:90
internal/internalapi:65
internal/mce/division:78
"

status=0
for entry in $FLOORS; do
	pkg=${entry%:*}
	floor=${entry#*:}
	out=$(go test $GO_TAGS -cover "./$pkg/" 2>&1 || true)
	pct=$(printf '%s\n' "$out" | sed -n 's/.*coverage: \([0-9.]*\)% of statements.*/\1/p' | tail -1)
	if [ -z "$pct" ]; then
		printf 'FAIL %s: no coverage reported\n' "$pkg"
		printf '%s\n' "$out" | tail -5
		status=1
		continue
	fi
	if awk -v p="$pct" -v f="$floor" 'BEGIN { exit !(p + 0 >= f + 0) }'; then
		printf 'ok   %s: %s%% (floor %s%%)\n' "$pkg" "$pct" "$floor"
	else
		printf 'FAIL %s: %s%% < floor %s%%\n' "$pkg" "$pct" "$floor"
		status=1
	fi
done

exit $status
