#!/usr/bin/env bash

set -euo pipefail

limit=250
failed=0

while IFS= read -r -d '' file; do
	path=${file#./}
	pure_loc=$(awk '
		{
			line = $0
			for (;;) {
				if (in_block_comment) {
					comment_end = index(line, "*/")
					if (!comment_end) {
						line = ""
						break
					}
					line = substr(line, comment_end + 2)
					in_block_comment = 0
				}
				comment_start = index(line, "/*")
				if (!comment_start) {
					break
				}
				before = substr(line, 1, comment_start - 1)
				after = substr(line, comment_start + 2)
				comment_end = index(after, "*/")
				if (!comment_end) {
					line = before
					in_block_comment = 1
					break
				}
				line = before substr(after, comment_end + 2)
			}
			sub(/\/\/.*/, "", line)
			if (line ~ /[^[:space:]]/) {
				count++
			}
		}
		END { print count + 0 }
	' "$file")
	if (( pure_loc > limit )); then
		printf '%s: %s pure LOC (limit %s)\n' "$path" "$pure_loc" "$limit" >&2
		failed=1
	fi
done < <(find . -type f -name '*.go' -not -path './vendor/*' -print0)

exit "$failed"
