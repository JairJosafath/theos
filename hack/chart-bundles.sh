#!/usr/bin/env bash
# make validate-yaml: every Bundle the chart renders (values.yaml,
# catalog.bundles) names entries the chart installs in the same namespace, of
# the kind its side takes, and they fit together as the operator checks them
# for a Container (docs/plans/done/catalog-bundles.md, decision 8): the entries
# share a Container type (the Bundle's computed supportedTypes are not empty),
# the Standard supports the Framework, and a Framework that deploys itself
# comes with no Compute or Deployment. A Bundle that doesn't fails with its
# namespace, its name and what is wrong; every failing Bundle is reported
# before the exit.
# Run: hack/chart-bundles.sh RENDERED..., each a file of the chart's catalog
# objects (make validate-yaml's chart-gitea-catalog.yaml).
set -euo pipefail

[ $# -gt 0 ] || { echo "usage: hack/chart-bundles.sh RENDERED..." >&2; exit 2; }
for f in "$@"; do
	awk -v file="$(basename "$f")" '
	function flush(   i, k) {
		if (kind == "Framework" || kind == "Standard" || kind == "Compute" || kind == "Deployment") {
			k = ns SUBSEP kind SUBSEP name
			have[k] = 1; types[k] = typesList; frameworks[k] = frameworksList; self[k] = deploysItself
		} else if (kind == "Bundle") {
			n++; bns[n] = ns; bname[n] = name
			for (i in ref) bref[n, i] = ref[i]
		}
		kind = ""; name = ""; ns = ""; typesList = ""; frameworksList = ""; deploysItself = 0; list = ""; side = ""
		delete ref
	}
	# Two spaces: metadata and spec; four: a ref name or a list item.
	/^---/ { flush(); next }
	/^kind: / { kind = $2; next }
	/^  name: / && name == "" { name = $2; next }
	/^  namespace: / { ns = $2; next }
	/^  supportedTypes:$/ { list = "types"; next }
	/^  supportedFrameworks:$/ { list = "frameworks"; next }
	/^  deploysItself: true$/ { deploysItself = 1; next }
	/^  (framework|standard|compute|deployment)Ref:$/ { side = $1; sub(/Ref:$/, "", side); next }
	/^    name: / && side != "" { ref[side] = $2; side = ""; next }
	/^  - / && list != "" {
		if (list == "types") typesList = typesList " " $2; else frameworksList = frameworksList " " $2
		next
	}
	{ list = ""; side = "" }
	END {
		flush()
		for (i = 1; i <= n; i++) {
			who = "The chart (" file "): Bundle " bns[i] "/" bname[i]
			if (bref[i, "framework"] == "") {
				print who " names no frameworkRef" > "/dev/stderr"; bad = 1; continue
			}
			shared = ""; first = 1; missing = 0
			split("framework Framework standard Standard compute Compute deployment Deployment", sides, " ")
			for (s = 1; s <= 8; s += 2) {
				r = bref[i, sides[s]]
				if (r == "") continue
				k = bns[i] SUBSEP sides[s + 1] SUBSEP r
				if (!(k in have)) {
					print who " names " sides[s + 1] " " r ", which the chart does not install in " bns[i] > "/dev/stderr"
					bad = 1; missing = 1; continue
				}
				if (first) { shared = types[k]; first = 0 } else shared = intersect(shared, types[k])
			}
			if (missing) continue
			if (shared == "") {
				print who ": its entries share no Container type (" sideTypes(i) ")" > "/dev/stderr"; bad = 1
			}
			fw = bref[i, "framework"]; fk = bns[i] SUBSEP "Framework" SUBSEP fw
			if (bref[i, "standard"] != "") {
				sk = bns[i] SUBSEP "Standard" SUBSEP bref[i, "standard"]
				if (!has(frameworks[sk], fw)) {
					print who ": Standard " bref[i, "standard"] " does not support Framework " fw " (supportedFrameworks:" frameworks[sk] ")" > "/dev/stderr"; bad = 1
				}
			}
			if (self[fk] && (bref[i, "compute"] != "" || bref[i, "deployment"] != "")) {
				print who ": Framework " fw " deploys itself; remove computeRef and deploymentRef" > "/dev/stderr"; bad = 1
			}
		}
		if (bad) exit 1
		print n + 0 " Bundles in " file " name entries the chart installs, each supporting a Container type"
	}
	function has(l, x) { return index(" " l " ", " " x " ") > 0 }
	function intersect(a, b,   out, parts, j, m) {
		out = ""; m = split(a, parts, " ")
		for (j = 1; j <= m; j++) if (has(b, parts[j])) out = out " " parts[j]
		return out
	}
	function sideTypes(i,   out, s, r) {
		out = ""
		for (s = 1; s <= 8; s += 2) {
			r = bref[i, sides[s]]
			if (r == "") continue
			out = out (out == "" ? "" : "; ") r ":" types[bns[i] SUBSEP sides[s + 1] SUBSEP r]
		}
		return out
	}' "$f"
done
