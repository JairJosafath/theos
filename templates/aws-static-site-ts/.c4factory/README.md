# aws-static-site-ts (Framework)

A static site on AWS in TypeScript: a page built with Vite, in a private S3
bucket that only a CloudFront distribution reads, with its infrastructure as a
CDK app. Nothing deploys: the check builds the site and synthesizes the
CloudFormation offline. Version v1.0.0.

- **Supports:** `static-site`
- **Layout:** `index.html` is the entry Vite builds; its `<title>` is the
  display name, HTML escaped, which `src/main.ts` shows as the heading, so no
  TypeScript file is rendered for the name. `src/main.ts` only finds the
  page's elements (failing at load, with the selector, when one is missing)
  and wires the input to `greeting` in `src/greeting.ts`, which touches no
  DOM and is tested by `src/greeting.test.ts` in plain Node.
  `src/index.css` styles the page; `public/favicon.svg` is copied as is. No
  UI framework: a page that needs one is a `react-vite` Container's job.
- **Operations:** `infra/operations.ts`, rendered from `.Operations` as
  aws-lambda-ts renders it, exports one frozen `OPERATIONS` of the Standard's
  `Operations` type. The site holds no data AWS Backup backs up, so
  `backups.retentionDays` only lengthens how long the bucket keeps earlier
  versions, and `copyToRegion` copies nothing: CloudFront is global and the
  files are built from git. The factory overwrites the file when the block
  changes; it is not a protected path.
- **Infrastructure:** `infra/stacks.ts` exports `defineStacks(app: App, tier:
  Tier): Stack[]`, which adds one stack, `infra/site_stack.ts`'s `SiteStack`,
  named after the package (the Container, and `<name>-development` in
  development, the Standard's `stackNameFor`), in us-east-1 with no account:
  CloudFront takes its web ACL and certificate, and reports its metrics, only
  there. The stack has:
  - the parameters `DomainName` and `CertificateArn` (an ACM certificate in
    us-east-1), both empty by default, the condition `HasDomain` and the
    rule `DomainNeedsCertificate`;
  - a `Logs` bucket for the distribution's access logs (`cloudfront/`) and
    the site bucket's (`s3/`): S3-managed encryption, public access blocked,
    SSL only, `OBJECT_WRITER` ownership (CloudFront's standard logs use
    ACLs), objects expiring after the tier's log retention, `RETAIN` in both
    tiers, since it empties itself;
  - a `Site` bucket: private, S3-managed encryption, SSL only, its access
    logged; in production versioned, `RETAIN`, noncurrent versions expiring
    after `versionsKeptDays` (35, or `retentionDays` when longer) and
    incomplete uploads after 7 days; in development unversioned and
    `DESTROY`;
  - a `Distribution` reading the bucket through origin access control,
    redirecting HTTP to HTTPS, with AWS's managed `SECURITY_HEADERS`
    policy, HTTP/2 and HTTP/3, `PRICE_CLASS_100`, `index.html` as the
    default root object and standard logging; with a domain, its aliases
    and the certificate by SNI with `TLSv1.2_2021`, else CloudFront's own
    domain;
  - in production a `Firewall`, an AWS WAF web ACL (scope `CLOUDFRONT`) with
    AWS's `AWSManagedRulesAmazonIpReputationList`,
    `AWSManagedRulesCommonRuleSet` and `AWSManagedRulesKnownBadInputsRuleSet`
    and a rate-based rule `RequestsPerAddress` blocking an address past
    2,000 requests in five minutes, with metrics and sampled requests;
  - `Deploy`, a `BucketDeployment` of the site built by Vite, which copies
    it into the bucket on each deploy and invalidates `/*`. It keeps the
    files earlier builds had (`prune: false`): a browser holding an earlier
    `index.html` still finds the hashed assets it names. Its function has its own role (`DeployRole`,
    allowed to write only its log group, `DeployLogs`, kept for the tier's
    log retention) and X-Ray `Active` in production. In development
    `retainOnDelete` is false, so the files go with the stack and the bucket
    deletes cleanly.

  `infra/alarms.ts`'s `Alarms` is aws-lambda-ts's: the encrypted topic, the
  `AlertEmail` parameter and its conditional email subscription, and
  `cdk-monitoring-constructs`' `MonitoringFacade` with a dashboard named
  after the stack. It alarms on the distribution's `5xxErrorRate` (average
  over 1 % in a five-minute period, `Site-Fault-Rate-Critical`) and the
  copying function's errors, throttles and p99 duration over 12 of its 15
  minutes (`Deploy-Fault-Count-Critical`, `Deploy-Throttled-Count-Critical`,
  `Deploy-Latency-P99-Warning`); the firewall is on the dashboard with no
  alarm. The stack outputs `EntryPoint` (the site's https URL: the domain
  when set, else the distribution's) and `AlarmTopicArn`. No AWS Backup plan
  or vault: nothing in the stack is what AWS Backup backs up.
  `infra/site_stack.test.ts` asserts it with `Template.fromStack` for both
  tiers.
- **Writes:** `package.json`, `package-lock.json`, `tsconfig.json`,
  `vitest.config.mts`, `cdk.json`, `index.html`, `src/`,
  `public/favicon.svg`, `infra/stacks.ts`, `infra/site_stack.ts`,
  `infra/site_stack.test.ts`, `infra/alarms.ts`, `infra/operations.ts`,
  `README.md`, `CHANGELOG.md`, `.gitignore`
- **Package:** `name` is the Container, version `0.0.1`, private and
  `UNLICENSED`, an ES module, `engines.node` `>=22`. Every package is a
  `devDependency`: the page imports none yet, and Vite bundles what it
  will. Scripts: `dev`, `build`, `preview` (Vite), `typecheck`, `test`,
  `lint`, `format` and `synth`.
- **Build:** `BucketDeployment`'s asset is the repository root with local
  bundling that runs the installed `vite build` into the asset's folder
  (`AssetHashType.OUTPUT`, so the asset changes only when the build does);
  never Docker, which CI and agents lack. The bundling's Docker image is
  named only because CDK requires one. `npm run build` writes the same to
  `dist/`, and `npm run dev` serves it with hot reload. There is no
  `vite.config.ts`: Vite's defaults build it. `cdk.json`'s `app` runs
  `infra/main.ts` with tsx, and its context holds the feature flags
  `cdk init` of aws-cdk 2.1145.0 recommends.
- **Tests:** vitest (`vitest.config.mts`) runs `src/**/*.test.ts` and
  `infra/**/*.test.ts` in Node, with a 60-second timeout for a test that
  synthesizes the stack, which builds the site with Vite, and 120 seconds
  for a `beforeAll` that synthesizes both tiers.
- **Type check:** `tsconfig.json` is `strict` with `noUncheckedIndexedAccess`
  and the other strict flags, the DOM libraries and the `vite/client` and
  Node types (the page, the CDK app and the configs share it),
  `resolveJsonModule`; `noEmit`. It includes `src`, `infra` and
  `vitest.config.mts`. `exactOptionalPropertyTypes` is off, as in
  aws-ec2-ts: aws-cdk-lib's `Bucket` doesn't satisfy `IBucket` under it.
- **Lockfile:** `package-lock.json` was generated by npm 11 on Node 24; only its
  two `name` fields are templated, so `npm ci` installs exactly the versions
  in `package.json`. `allowScripts` lets esbuild's install script run: tsx
  needs esbuild.
- **Standard:** made for `aws-standard` v1.7.0 or later, which owns
  `infra/main.ts`, `infra/standard.ts` (the tier, `TIER_RULES` and the
  `Operations` type this Framework imports) and `infra/standard.test.ts`,
  whose data-bucket and distribution checks this stack needs. Lists its
  tools in `devDependencies` with exact versions, aws-lambda-ts's without
  Powertools, the SDK, esbuild and `@types/aws-lambda`, plus Vite as
  react-vite pins it: `eslint` 10.11.0, `@eslint/js` 10.0.1,
  `typescript-eslint` 8.71.0, `prettier` 3.9.9, `typescript` 6.0.3,
  `vitest` 5.0.3, `vite` 8.3.3, `@types/node` 24.19.1, `aws-cdk-lib`
  2.273.0, `constructs` 10.8.1, `aws-cdk` 2.1145.0, `cdk-nag` 3.0.2,
  `cdk-monitoring-constructs` 10.3.2 and `tsx` 4.23.15. TypeScript is 6.0.3
  because typescript-eslint 8.71 supports `<6.1.0`.
- **CDK Nag:** every finding is fixed but these, acknowledged on the one
  construct with the reason:
  - `AwsSolutions-S1` on `Logs`: logging its access would need another
    bucket, whose own access would go unlogged.
  - `AwsSolutions-CFR1`: the site is for anyone; a geographic restriction
    is the owner's choice when the law or the audience asks.
  - `AwsSolutions-CFR2`, in development only: a firewall costs a monthly fee
    per stack and a development site serves no one who needs it.
  - `AwsSolutions-CFR4`: with a domain the distribution takes TLS 1.2 or
    later through an override CDK Nag doesn't read; without one it answers
    on CloudFront's domain, whose certificate CloudFront keeps at TLSv1.
  - `AwsSolutions-IAM5` on `DeployRole`, one per wildcard
    `BucketDeployment` grants (`s3:Abort*`, `s3:DeleteObject*`,
    `s3:GetBucket*`, `s3:GetObject*`, `s3:List*`, the site bucket's and the
    asset's objects, and `*` for CloudFront's invalidation and X-Ray).
  - `AwsSolutions-L1` on the copying function: `BucketDeployment` picks its
    Python runtime with the AWS CLI layer it ships.

  cfn-lint's W1030 is ignored on the distribution: it checks the
  certificate's ARN against the parameter's empty default even where
  `HasDomain` keeps it out.
- **Devcontainer:** `.c4factory/devcontainer.json` uses `c4factory/go:1.26`, the
  Go and Node toolchain CI and agents use, with ESLint and Prettier set up.

## How a template works

Everything in this repository is copied into the new Container repository at the
same path, except `.c4factory/`, which is input for the factory. Files ending in
`.tmpl` are rendered with Go `text/template` (missing keys are errors) and lose the
suffix; a `.tmpl` that renders to only whitespace is left out. Other files are copied
byte for byte. Paths are never rendered, only contents.

Data: `.Project`, `.SoftwareSystem`, `.Container`, `.DisplayName`, `.Type`, the chosen
`.Framework`, `.Standard`, `.Compute` and `.Deployment` (empty when none is chosen),
`.Connectors` (sorted names) and `.Operations` (the Container's
`spec.operations`, defaults filled in). Functions: `has`, `snake`, `upper`,
`lower`, `dart`, `ts` (a TypeScript string literal, quoted as Prettier
quotes it), plus the `text/template` builtins.
`infra/operations.ts.tmpl` is the one TypeScript file rendered: the CDK app
reads the stack's name and description from `package.json`.

Each path belongs to exactly one layer; `.devcontainer/**` and `.c4factory.yaml`
belong to the operator. Release a change by tagging a new version and bumping it in
`templates/versions.txt` of C4 Factory; a tag is never moved.
