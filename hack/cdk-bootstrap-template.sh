#!/usr/bin/env bash
# Refresh the CDK bootstrap template the cloudFormation Deployer embeds
# (internal/adapters/cloudformation, docs/plans/aws-deploy.md, #1608): fetch the
# npm tarball of the `aws-cdk` CLI the templates pin, take its
# bootstrap-template.yaml, and write it as bootstrap-template-v<N>.yaml, N being
# the template's CdkBootstrapVersion; older bootstrap-template-v*.yaml go.
# The download is data: it is extracted into its own empty directory and nothing
# in it runs.
# Run: hack/cdk-bootstrap-template.sh [VERSION]   (default: the pin in
# templates/aws-lambda-ts/package.json.tmpl)
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
dest="$root/internal/adapters/cloudformation"

version=${1:-}
if [ -z "$version" ]; then
	version=$(sed -n 's/^ *"aws-cdk": *"\([0-9][0-9.]*\)".*/\1/p' "$root/templates/aws-lambda-ts/package.json.tmpl" | head -n1)
fi
[ -n "$version" ] || { echo "no aws-cdk pin found in templates/aws-lambda-ts/package.json.tmpl" >&2; exit 1; }

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir "$work/out"
curl -fsSL -o "$work/cdk.tgz" "https://registry.npmjs.org/aws-cdk/-/aws-cdk-$version.tgz"
tar -xzf "$work/cdk.tgz" -C "$work/out" package/lib/api/bootstrap/bootstrap-template.yaml
src="$work/out/package/lib/api/bootstrap/bootstrap-template.yaml"

n=$(awk '
	/^  CdkBootstrapVersion:/ { in_res = 1; next }
	in_res && /^  [A-Za-z]/ { in_res = 0 }
	in_res && /^      Value:/ { gsub(/[^0-9]/, "", $2); print $2; exit }
' "$src")
case "$n" in
'' | *[!0-9]*) echo "no CdkBootstrapVersion value in aws-cdk $version's bootstrap template" >&2; exit 1 ;;
esac

rm -f "$dest"/bootstrap-template-v*.yaml
cp "$src" "$dest/bootstrap-template-v$n.yaml"
echo "aws-cdk $version: bootstrap template version $n -> internal/adapters/cloudformation/bootstrap-template-v$n.yaml"
