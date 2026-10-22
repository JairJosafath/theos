# CloudFormation adapter fixtures

Each folder is the `make dist VERSION=v0.1.0` of one AWS Framework on
`aws-standard` (#1607), both tiers, as `KEEP=1 COMBO="<combo>" make
test-templates` renders it: `aws lambda function` (aws-lambda-ts), `aws lambda
function in go`, `aws python lambda function` and `aws ec2 service`. The
manifests, templates and assets.json files are whole; each `asset.*` file is a
one-line stand-in. Regenerate them the same way after a change to
`aws-standard`'s `dist` or to one of these Frameworks' stacks.
