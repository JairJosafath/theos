import { AssetHashType, CfnOutput, DockerImage, Duration, RemovalPolicy, Stack } from 'aws-cdk-lib';
import type { StackProps } from 'aws-cdk-lib';
import { HttpApi, HttpMethod, LogGroupLogDestination } from 'aws-cdk-lib/aws-apigatewayv2';
import { HttpIamAuthorizer } from 'aws-cdk-lib/aws-apigatewayv2-authorizers';
import { HttpLambdaIntegration } from 'aws-cdk-lib/aws-apigatewayv2-integrations';
import { AttributeType, BillingMode, Table, TableEncryption } from 'aws-cdk-lib/aws-dynamodb';
import { Role, ServicePrincipal } from 'aws-cdk-lib/aws-iam';
import {
  Architecture,
  Code,
  Function,
  LoggingFormat,
  Runtime,
  Tracing,
} from 'aws-cdk-lib/aws-lambda';
import { LogGroup } from 'aws-cdk-lib/aws-logs';
import type { Construct } from 'constructs';
import { execFileSync } from 'node:child_process';
import path from 'node:path';

import { Alarms } from './alarms';
import { Backups } from './backups';
import { TIER_RULES } from './standard';
import type { Operations, Tier } from './standard';

// The function's Go module: its own go.mod, apart from node_modules/, whose
// stray .go files would otherwise be part of it.
const SRC = path.join(import.meta.dirname, '..', 'src');

// The function's time limit, which its duration alarm is a share of.
const TIMEOUT = Duration.seconds(10);

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

/** What the stack is built for. */
export interface ApiStackProps extends StackProps {
  /** Where it runs, which decides what it keeps, backs up and traces. */
  readonly tier: Tier;
  /** What the Container promises whatever the tier: `OPERATIONS`. */
  readonly operations: Operations;
}

/**
 * The Container's one stack: an HTTP API whose routes call a Lambda function
 * that keeps items in a DynamoDB table. Callers sign their requests with IAM
 * (SigV4); the API refuses everyone else. Every part has alarms, to a topic
 * the stack outputs with its URL; production keeps and backs up the data.
 */
export class ApiStack extends Stack {
  /** The API's URL, `https://<id>.execute-api.<region>.amazonaws.com/`. */
  readonly url: string;

  constructor(scope: Construct, id: string, props: ApiStackProps) {
    super(scope, id, props);
    const rules = TIER_RULES[props.tier];
    const { backups, alerts } = props.operations;

    // The table holds data: kept, protected and recoverable where the tier
    // keeps data, thrown away with the stack where it doesn't.
    const table = new Table(this, 'Items', {
      partitionKey: { name: 'id', type: AttributeType.STRING },
      billingMode: BillingMode.PAY_PER_REQUEST,
      encryption: TableEncryption.AWS_MANAGED,
      pointInTimeRecoverySpecification: {
        pointInTimeRecoveryEnabled: rules.pointInTimeRecovery,
      },
      deletionProtection: rules.keepData,
      removalPolicy: rules.keepData ? RemovalPolicy.RETAIN : RemovalPolicy.DESTROY,
    });

    const functionLogs = new LogGroup(this, 'HandlerLogs', {
      retention: rules.logRetention,
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
      timeout: TIMEOUT,
      logGroup: functionLogs,
      loggingFormat: LoggingFormat.JSON,
      // Lambda traces each call itself; the function adds no subsegments.
      tracing: rules.tracing ? Tracing.ACTIVE : Tracing.DISABLED,
      environment: { TABLE_NAME: table.tableName },
    });
    // Only the calls the handler makes.
    table.grant(handler, 'dynamodb:GetItem', 'dynamodb:PutItem');

    const accessLogs = new LogGroup(this, 'ApiAccessLogs', {
      retention: rules.logRetention,
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

    // Each alarm fires on the first bad five minutes: a server error, a
    // failed or throttled call, or calls near their time limit.
    // docs/runbook.md says what each means and what to do first.
    // tsconfig's exactOptionalPropertyTypes keeps a HttpApi or Table from
    // passing as the IHttpApi or ITable the monitoring takes (an optional
    // property they leave undefined), so it gets a reference by id or name.
    const alarms = new Alarms(this, 'Alarms', alerts.email);
    alarms.monitoring
      .monitorApiGatewayV2HttpApi({
        api: HttpApi.fromHttpApiAttributes(this, 'MonitoredApi', { httpApiId: api.apiId }),
        alarmFriendlyName: 'Api',
        add5xxCountAlarm: { Critical: { maxErrorCount: 0 } },
      })
      .monitorLambdaFunction({
        lambdaFunction: handler,
        alarmFriendlyName: 'Handler',
        addFaultCountAlarm: { Critical: { maxErrorCount: 0 } },
        addThrottlesCountAlarm: { Critical: { maxErrorCount: 0 } },
        addLatencyP99Alarm: {
          Warning: { maxLatency: Duration.millis(TIMEOUT.toMilliseconds() * 0.8) },
        },
      })
      .monitorDynamoTable({
        table: Table.fromTableName(this, 'MonitoredItems', table.tableName),
        alarmFriendlyName: 'Items',
        billingMode: BillingMode.PAY_PER_REQUEST,
        addSystemErrorCountAlarm: { Critical: { maxErrorCount: 0 } },
        addReadThrottledEventsCountAlarm: { Critical: { maxThrottledEventsThreshold: 0 } },
        addWriteThrottledEventsCountAlarm: { Critical: { maxThrottledEventsThreshold: 0 } },
      });

    if (rules.backups && backups.retentionDays > 0) {
      new Backups(this, 'Backups', {
        retentionDays: backups.retentionDays,
        copyToRegion: backups.copyToRegion,
        tables: [table],
      });
    }

    new CfnOutput(this, 'EntryPoint', {
      value: this.url,
      description: 'The URL of the HTTP API.',
    });
  }
}
