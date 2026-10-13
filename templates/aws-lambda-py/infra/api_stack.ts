import { AssetHashType, Duration, RemovalPolicy, Stack } from 'aws-cdk-lib';
import type { StackProps } from 'aws-cdk-lib';
import { HttpApi, HttpMethod, LogGroupLogDestination } from 'aws-cdk-lib/aws-apigatewayv2';
import { HttpIamAuthorizer } from 'aws-cdk-lib/aws-apigatewayv2-authorizers';
import { HttpLambdaIntegration } from 'aws-cdk-lib/aws-apigatewayv2-integrations';
import { AttributeType, BillingMode, Table, TableEncryption } from 'aws-cdk-lib/aws-dynamodb';
import { Role, ServicePrincipal } from 'aws-cdk-lib/aws-iam';
import {
  Architecture,
  Code,
  Function as LambdaFunction,
  LoggingFormat,
  Runtime,
} from 'aws-cdk-lib/aws-lambda';
import { LogGroup, RetentionDays } from 'aws-cdk-lib/aws-logs';
import type { Construct } from 'constructs';
import { execFileSync } from 'node:child_process';
import { cpSync, mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';

// The repository root: the function's package app/, pyproject.toml and uv.lock
// are there.
const ROOT = path.join(import.meta.dirname, '..');
const RUNTIME = Runtime.PYTHON_3_14;

/**
 * The function's code: the package `app/` and the packages uv.lock pins for
 * it, without the dev group, as wheels for Lambda's arm64 Linux. uv installs
 * them here, from its cache or PyPI. It returns true or throws, so CDK never
 * falls back to Docker, which CI doesn't have.
 */
function bundle(outputDir: string): boolean {
  const work = mkdtempSync(path.join(tmpdir(), 'bundle-'));
  try {
    const requirements = path.join(work, 'requirements.txt');
    const uv = (args: string[]): void => {
      execFileSync('uv', args, { cwd: ROOT, stdio: ['ignore', 'ignore', 'inherit'] });
    };
    uv(['export', '--frozen', '--no-dev', '--no-emit-project', '--quiet', '-o', requirements]);
    uv([
      'pip',
      'install',
      '--quiet',
      '--requirements',
      requirements,
      '--target',
      outputDir,
      '--no-deps',
      '--only-binary',
      ':all:',
      '--python-version',
      '3.14',
      '--python-platform',
      'aarch64-manylinux_2_28',
    ]);
  } finally {
    rmSync(work, { recursive: true, force: true });
  }
  cpSync(path.join(ROOT, 'app'), path.join(outputDir, 'app'), {
    recursive: true,
    filter: (source) => path.basename(source) !== '__pycache__',
  });
  return true;
}

/**
 * The Container's one stack: an HTTP API whose routes call a Lambda function
 * that keeps items in a DynamoDB table. Callers sign their requests with IAM
 * (SigV4); the API refuses everyone else.
 */
export class ApiStack extends Stack {
  /** The API's URL, `https://<id>.execute-api.<region>.amazonaws.com/`. */
  readonly url: string;

  constructor(scope: Construct, id: string, props?: StackProps) {
    super(scope, id, props);

    const table = new Table(this, 'Items', {
      partitionKey: { name: 'id', type: AttributeType.STRING },
      billingMode: BillingMode.PAY_PER_REQUEST,
      encryption: TableEncryption.AWS_MANAGED,
      pointInTimeRecoverySpecification: { pointInTimeRecoveryEnabled: true },
      deletionProtection: true,
      // The data outlives the stack: deleting the stack keeps the table.
      removalPolicy: RemovalPolicy.RETAIN,
    });

    const functionLogs = new LogGroup(this, 'HandlerLogs', {
      retention: RetentionDays.ONE_MONTH,
      removalPolicy: RemovalPolicy.DESTROY,
    });
    // The function's own role, allowed only to write its log group: the
    // default role would carry the AWS managed AWSLambdaBasicExecutionRole,
    // which may write to every log group in the account.
    const role = new Role(this, 'HandlerRole', {
      assumedBy: new ServicePrincipal('lambda.amazonaws.com'),
    });
    functionLogs.grantWrite(role);
    const handler = new LambdaFunction(this, 'Handler', {
      role,
      code: Code.fromAsset(path.join(ROOT, 'app'), {
        // The hash of what bundle writes, so a change to uv.lock is a new asset.
        assetHashType: AssetHashType.OUTPUT,
        bundling: { image: RUNTIME.bundlingImage, local: { tryBundle: bundle } },
      }),
      handler: 'app.handler.handler',
      runtime: RUNTIME,
      architecture: Architecture.ARM_64,
      memorySize: 256,
      timeout: Duration.seconds(10),
      logGroup: functionLogs,
      loggingFormat: LoggingFormat.JSON,
      environment: { TABLE_NAME: table.tableName },
    });
    // Only the calls the handler makes.
    table.grant(handler, 'dynamodb:GetItem', 'dynamodb:PutItem');

    const accessLogs = new LogGroup(this, 'ApiAccessLogs', {
      retention: RetentionDays.ONE_MONTH,
      removalPolicy: RemovalPolicy.DESTROY,
    });
    const api = new HttpApi(this, 'Api', {
      defaultAuthorizer: new HttpIamAuthorizer(),
      createDefaultStage: false,
    });
    api.addStage('DefaultStage', {
      stageName: '$default',
      autoDeploy: true,
      throttle: { rateLimit: 50, burstLimit: 100 },
      accessLogSettings: { destination: new LogGroupLogDestination(accessLogs) },
    });
    api.addRoutes({
      path: '/items/{id}',
      methods: [HttpMethod.GET, HttpMethod.PUT],
      integration: new HttpLambdaIntegration('Items', handler),
    });
    this.url = api.apiEndpoint;
  }
}
