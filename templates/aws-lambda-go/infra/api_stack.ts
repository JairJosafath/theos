import { AssetHashType, DockerImage, Duration, RemovalPolicy, Stack } from 'aws-cdk-lib';
import type { StackProps } from 'aws-cdk-lib';
import { HttpApi, HttpMethod, LogGroupLogDestination } from 'aws-cdk-lib/aws-apigatewayv2';
import { HttpIamAuthorizer } from 'aws-cdk-lib/aws-apigatewayv2-authorizers';
import { HttpLambdaIntegration } from 'aws-cdk-lib/aws-apigatewayv2-integrations';
import { AttributeType, BillingMode, Table, TableEncryption } from 'aws-cdk-lib/aws-dynamodb';
import { Role, ServicePrincipal } from 'aws-cdk-lib/aws-iam';
import { Architecture, Code, Function, LoggingFormat, Runtime } from 'aws-cdk-lib/aws-lambda';
import { LogGroup, RetentionDays } from 'aws-cdk-lib/aws-logs';
import type { Construct } from 'constructs';
import { execFileSync } from 'node:child_process';
import path from 'node:path';

// The function's Go module: its own go.mod, apart from node_modules/, whose
// stray .go files would otherwise be part of it.
const SRC = path.join(import.meta.dirname, '..', 'src');

/**
 * The function's code: `src/` built by the installed Go for Lambda's arm64
 * Linux into one `bootstrap` binary, the file the provided.al2023 runtime
 * runs. Never Docker, which CI doesn't have: a failed build fails synth.
 */
function goFunctionCode(): Code {
  return Code.fromAsset(SRC, {
    // The hash of the binary, what the source builds to: -trimpath and
    // -buildvcs=false keep it the same from one build to the next.
    assetHashType: AssetHashType.OUTPUT,
    bundling: {
      // Required by the type, never pulled: local bundling always succeeds or throws.
      image: DockerImage.fromRegistry('public.ecr.aws/docker/library/golang'),
      local: {
        tryBundle(outputDir: string): boolean {
          execFileSync(
            'go',
            [
              'build',
              '-trimpath',
              '-buildvcs=false',
              '-tags',
              'lambda.norpc',
              '-ldflags',
              '-s -w',
              '-o',
              path.join(outputDir, 'bootstrap'),
              '.',
            ],
            {
              cwd: SRC,
              env: { ...process.env, GOOS: 'linux', GOARCH: 'arm64', CGO_ENABLED: '0' },
              stdio: ['ignore', 'inherit', 'inherit'],
            },
          );
          return true;
        },
      },
    },
  });
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
    const handler = new Function(this, 'Handler', {
      role,
      code: goFunctionCode(),
      handler: 'bootstrap',
      runtime: Runtime.PROVIDED_AL2023,
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
