import {
  CfnOutput,
  CfnParameter,
  CfnResource,
  DefaultStackSynthesizer,
  Duration,
  RemovalPolicy,
  Stack,
  Validations,
} from 'aws-cdk-lib';
import type { StackProps } from 'aws-cdk-lib';
import {
  ComparisonOperator,
  MathExpression,
  Metric,
  Stats,
  TreatMissingData,
} from 'aws-cdk-lib/aws-cloudwatch';
import { AttributeType, BillingMode, Table, TableEncryption } from 'aws-cdk-lib/aws-dynamodb';
import {
  FlowLogDestination,
  FlowLogTrafficType,
  GatewayVpcEndpointAwsService,
  SubnetType,
  Vpc,
} from 'aws-cdk-lib/aws-ec2';
import {
  AlarmBehavior,
  Cluster,
  ContainerDependencyCondition,
  ContainerImage,
  ContainerInsights,
  CpuArchitecture,
  FargateService,
  FargateTaskDefinition,
  LogDrivers,
  OperatingSystemFamily,
} from 'aws-cdk-lib/aws-ecs';
import type { ContainerDefinition } from 'aws-cdk-lib/aws-ecs';
import {
  ApplicationLoadBalancer,
  ApplicationProtocol,
  HttpCodeElb,
  HttpCodeTarget,
  ListenerAction,
  ListenerCertificate,
  SslPolicy,
} from 'aws-cdk-lib/aws-elasticloadbalancingv2';
import { PolicyStatement, ServicePrincipal } from 'aws-cdk-lib/aws-iam';
import { LogGroup } from 'aws-cdk-lib/aws-logs';
import type { RetentionDays } from 'aws-cdk-lib/aws-logs';
import { BlockPublicAccess, Bucket, BucketEncryption } from 'aws-cdk-lib/aws-s3';
import type { CustomThreshold } from 'cdk-monitoring-constructs';
import type { Construct } from 'constructs';

import { Alarms } from './alarms';
import { Backups } from './backups';
import { DATA_STORES } from './connectors';
import type { Connect } from './connectors';
import { TIER_RULES } from './standard';
import type { Operations, Tier } from './standard';
import { Users } from './user_pool';

// The port the service listens on in its container: src/main.ts's default.
const PORT = 8080;
// The Availability Zones the VPC spans.
const ZONES = 2;
// NAT gateways per tier: one per zone in production, so the tasks in a zone
// keep their way out when the other zone fails; one in development, for half
// the price.
const NAT_GATEWAYS: Readonly<Record<Tier, number>> = { production: ZONES, development: 1 };
// The fewest tasks the service runs, one per zone, and the most it scales to.
const MIN_TASKS = 2;
const MAX_TASKS = 4;
// The app container's CPU units and memory, in every tier: in development it
// has the task to itself, and where the tier traces the collector's share is
// added to the task, so the app has the same room in both.
const APP_CPU = 256;
const APP_MEMORY_MIB = 512;
// The collector's share: CPU units it is sure of and a hard memory limit, so
// it never takes the app's. With the app's, the task is 512 CPU units and
// 1 GB, the smallest Fargate size with more CPU than the app's alone.
const COLLECTOR_CPU = 256;
const COLLECTOR_MEMORY_MIB = 512;
// The AWS Distro for OpenTelemetry collector that runs beside the app where
// the tier traces, pinned by tag, and the configuration in its image that
// takes spans over OTLP and sends them to X-Ray, and nothing else.
const COLLECTOR_IMAGE = 'public.ecr.aws/aws-observability/aws-otel-collector:v0.50.0';
const COLLECTOR_CONFIG = '/etc/ecs/ecs-xray.yaml';
// Where the app sends its spans: the collector's OTLP over HTTP receiver, in
// the same task. src/tracing.ts starts only when it is set.
const OTLP_ENDPOINT = 'http://localhost:4318';
// The only actions X-Ray's exporter needs, which take no resource but `*`.
const XRAY_WRITE_ACTIONS = ['xray:PutTraceSegments', 'xray:PutTelemetryRecords'];
// The slowest answer the latency alarm allows, at the 99th percentile: an
// item is one DynamoDB call, which takes milliseconds.
const MAX_LATENCY = Duration.seconds(1);
// A deployment rolls back when over this share of the answers, in percent,
// are 5xx from the load balancer or from the tasks, for this many one-minute
// periods in a row: a stray 5xx, or one bad minute, is no reason to.
const MAX_5XX_PERCENT = 5;
const ROLLBACK_MINUTES = 3;
// The alarms that fail a deployment and roll it back, by their names after
// the stack's: the share of 5xx above, from the load balancer or the tasks,
// or a task failing its health check.
const DEPLOYMENT_ALARMS = [
  'LoadBalancer-5xx-Rate-Critical',
  'Service-5xx-Rate-Critical',
  'Service-Unhealthy-Tasks-Critical',
];

/** What the stack is built for. */
export interface ServiceStackProps extends StackProps {
  /** Where it runs: what it keeps, backs up and traces, and how many NAT gateways it has. */
  readonly tier: Tier;
  /** What the Container promises whatever the tier: `OPERATIONS`. */
  readonly operations: Operations;
  /** The data stores it connects the service to: `DATA_STORES` unless a test names others. */
  readonly dataStores?: readonly (readonly [string, Connect])[];
}

/**
 * The Container's one stack: a service of Fargate tasks in private subnets,
 * behind an internet-facing Application Load Balancer that serves HTTPS
 * with the certificate the deployer names, keeping items in a DynamoDB table.
 * Every route but `/health` wants an access token of the stack's Cognito
 * user pool.
 * Every part has alarms, to a topic the stack outputs with the load
 * balancer's name, and a deployment that sets off a 5xx-share or
 * unhealthy-task alarm rolls back; production keeps and backs up the data
 * and traces every request.
 */
export class ServiceStack extends Stack {
  constructor(scope: Construct, id: string, props: ServiceStackProps) {
    super(scope, id, props);
    const rules = TIER_RULES[props.tier];
    const { backups, alerts } = props.operations;

    // The certificate is the deployer's: an ACM certificate for the domain
    // the service answers on. A parameter, so synth needs no account.
    const certificateArn = new CfnParameter(this, 'CertificateArn', {
      type: 'String',
      description: 'The ARN of the ACM certificate the load balancer serves HTTPS with.',
      allowedPattern: '^arn:aws[a-z-]*:acm:[a-z0-9-]+:[0-9]{12}:certificate/.+$',
    });
    // The release's image, by digest: the release builds and pushes it, and
    // the deployer copies it into the bootstrap's container-assets repository
    // and passes it here, so synth needs no Docker.
    const imageUri = new CfnParameter(this, 'ImageUri', {
      type: 'String',
      description:
        'The image the service runs, <account>.dkr.ecr.<region>.amazonaws.com/<repository>@sha256:<digest>.',
      allowedPattern:
        '^[0-9]{12}\\.dkr\\.ecr\\.[a-z0-9-]+\\.amazonaws\\.com(\\.cn)?/[a-z0-9._/-]+@sha256:[0-9a-f]{64}$',
    });

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

    // Two zones, the load balancer in public subnets and the tasks in private
    // ones; NAT gateways for ECR, CloudWatch Logs and X-Ray, and a gateway
    // endpoint, free, so DynamoDB traffic never leaves the VPC.
    const vpc = new Vpc(this, 'Network', {
      maxAzs: ZONES,
      natGateways: NAT_GATEWAYS[props.tier],
      subnetConfiguration: [
        { name: 'Public', subnetType: SubnetType.PUBLIC, cidrMask: 24 },
        { name: 'Private', subnetType: SubnetType.PRIVATE_WITH_EGRESS, cidrMask: 24 },
      ],
      gatewayEndpoints: { DynamoDb: { service: GatewayVpcEndpointAwsService.DYNAMODB } },
      // Restricting the default security group takes a custom resource: a
      // Lambda function CDK writes, with no log group of its own. Nothing here
      // uses the default group; every resource gets its own.
      restrictDefaultSecurityGroup: false,
    });
    const flowLogs = new LogGroup(this, 'FlowLogs', {
      retention: rules.logRetention,
      removalPolicy: RemovalPolicy.DESTROY,
    });
    vpc.addFlowLog('FlowLog', {
      destination: FlowLogDestination.toCloudWatchLogs(flowLogs),
      trafficType: FlowLogTrafficType.REJECT,
    });

    const cluster = new Cluster(this, 'Cluster', {
      vpc,
      containerInsightsV2: ContainerInsights.ENABLED,
    });
    const serviceLogs = new LogGroup(this, 'ServiceLogs', {
      retention: rules.logRetention,
      removalPolicy: RemovalPolicy.DESTROY,
    });
    const users = new Users(this, 'Users', { keepData: rules.keepData });

    const taskDefinition = new FargateTaskDefinition(this, 'Task', {
      cpu: APP_CPU + (rules.tracing ? COLLECTOR_CPU : 0),
      memoryLimitMiB: APP_MEMORY_MIB + (rules.tracing ? COLLECTOR_MEMORY_MIB : 0),
      runtimePlatform: {
        cpuArchitecture: CpuArchitecture.ARM64,
        operatingSystemFamily: OperatingSystemFamily.LINUX,
      },
    });
    const app = taskDefinition.addContainer('App', {
      image: ContainerImage.fromRegistry(imageUri.valueAsString),
      cpu: APP_CPU,
      memoryReservationMiB: APP_MEMORY_MIB,
      portMappings: [{ containerPort: PORT }],
      environment: { TABLE_NAME: table.tableName, ...users.environment },
      logging: LogDrivers.awsLogs({ streamPrefix: 'app', logGroup: serviceLogs }),
      readonlyRootFilesystem: true,
      // ECS sends SIGTERM once the load balancer has drained the task, then
      // SIGKILL after this: time for the requests still in flight to finish.
      stopTimeout: Duration.seconds(30),
    });
    if (rules.tracing) {
      this.traceThroughCollector(taskDefinition, app, rules.logRetention);
    }
    Validations.of(taskDefinition).acknowledge({
      id: 'AwsSolutions-ECS2',
      reason:
        'The variables name the stack’s own table and user pool and app client, the tables of the data stores the Container’s Relationships name and, where the tier traces, the collector’s address in the task and the service’s name in its traces; none is a secret.',
    });
    // Only the calls the service makes.
    table.grant(taskDefinition.taskRole, 'dynamodb:GetItem', 'dynamodb:PutItem');
    // The DataStores the Container's Relationships name, from their Connectors:
    // each grants the task role its calls and hands the app the table's name.
    const user = {
      grantPrincipal: taskDefinition.taskRole,
      addEnvironment: (key: string, value: string) => {
        app.addEnvironment(key, value);
      },
    };
    const stores = (props.dataStores ?? DATA_STORES).map(
      ([name, connect]) => [name, connect(this, user, rules)] as const,
    );
    // ECS pulls the image with the execution role: pulls from the bootstrap's
    // container-assets repository only, and ecr:GetAuthorizationToken, an
    // action that takes no resource but "*".
    const executionRole = taskDefinition.obtainExecutionRole();
    executionRole.addToPrincipalPolicy(
      new PolicyStatement({ actions: ['ecr:GetAuthorizationToken'], resources: ['*'] }),
    );
    executionRole.addToPrincipalPolicy(
      new PolicyStatement({
        actions: [
          'ecr:BatchCheckLayerAvailability',
          'ecr:BatchGetImage',
          'ecr:GetDownloadUrlForLayer',
        ],
        resources: [
          this.formatArn({
            service: 'ecr',
            resource: 'repository',
            resourceName: `cdk-${this.synthesizer.bootstrapQualifier ?? DefaultStackSynthesizer.DEFAULT_QUALIFIER}-container-assets-${this.account}-${this.region}`,
          }),
        ],
      }),
    );
    Validations.of(executionRole).acknowledge({
      id: 'AwsSolutions-IAM5[Resource::*]',
      reason:
        'ecr:GetAuthorizationToken accepts only the resource "*"; every other action names its resource.',
    });

    const service = new FargateService(this, 'Service', {
      cluster,
      taskDefinition,
      desiredCount: MIN_TASKS,
      // A deployment starts new tasks before stopping old ones, and rolls
      // back when the new ones never get healthy (the circuit breaker) or
      // set off a deployment alarm (below).
      minHealthyPercent: 100,
      maxHealthyPercent: 200,
      circuitBreaker: { enable: true, rollback: true },
      vpcSubnets: { subnetType: SubnetType.PRIVATE_WITH_EGRESS },
      assignPublicIp: false,
    });
    const scaling = service.autoScaleTaskCount({ minCapacity: MIN_TASKS, maxCapacity: MAX_TASKS });
    scaling.scaleOnCpuUtilization('Cpu', { targetUtilizationPercent: 70 });

    // Access logs are logs, not data: they expire after the tier's log
    // retention. The bucket outlives the stack in either tier, as
    // CloudFormation can't delete a bucket that holds objects; it empties
    // itself as they expire.
    const accessLogs = new Bucket(this, 'LoadBalancerLogs', {
      // Elastic Load Balancing writes access logs only with S3-managed keys.
      encryption: BucketEncryption.S3_MANAGED,
      blockPublicAccess: BlockPublicAccess.BLOCK_ALL,
      enforceSSL: true,
      lifecycleRules: [{ expiration: Duration.days(rules.logRetention) }],
      removalPolicy: RemovalPolicy.RETAIN,
    });
    Validations.of(accessLogs).acknowledge({
      id: 'AwsSolutions-S1',
      reason:
        'The bucket holds only the load balancer’s access logs; logging access to it would need another bucket whose own access went unlogged.',
    });
    const loadBalancer = new ApplicationLoadBalancer(this, 'LoadBalancer', {
      vpc,
      internetFacing: true,
      dropInvalidHeaderFields: true,
    });
    Validations.of(loadBalancer).acknowledge({
      id: 'AwsSolutions-EC23',
      reason:
        'A public service: the load balancer takes HTTPS, and HTTP that it redirects, from anywhere; the tasks take traffic only from the load balancer.',
    });
    this.logAccessLogs(loadBalancer, accessLogs);
    const https = loadBalancer.addListener('Https', {
      port: 443,
      protocol: ApplicationProtocol.HTTPS,
      certificates: [ListenerCertificate.fromArn(certificateArn.valueAsString)],
      sslPolicy: SslPolicy.RECOMMENDED_TLS,
    });
    const targetGroup = https.addTargets('Service', {
      port: PORT,
      protocol: ApplicationProtocol.HTTP,
      targets: [service],
      healthCheck: { path: '/health', healthyThresholdCount: 2, interval: Duration.seconds(15) },
      deregistrationDelay: Duration.seconds(30),
    });
    // Plain HTTP only redirects to HTTPS.
    loadBalancer.addListener('Http', {
      port: 80,
      protocol: ApplicationProtocol.HTTP,
      defaultAction: ListenerAction.redirect({ protocol: 'HTTPS', port: '443', permanent: true }),
    });

    // Most alarms fire on the first bad five minutes: a 5xx from the load
    // balancer or the tasks, slow answers, the tasks short of CPU or memory or
    // fewer than the service's minimum, and the table throttled or failing.
    // The ones a deployment rolls back on look closer and wait longer: the
    // share of 5xx minute by minute, and a task failing its health check.
    // docs/runbook.md says what each means and what to do first.
    const alarms = new Alarms(this, 'Alarms', alerts.email);
    // No requests is no 5xx and no slow answer.
    const above = (threshold: number): CustomThreshold => ({
      threshold,
      comparisonOperator: ComparisonOperator.GREATER_THAN_THRESHOLD,
      treatMissingDataOverride: TreatMissingData.NOT_BREACHING,
    });
    // Over MAX_5XX_PERCENT for ROLLBACK_MINUTES minutes in a row.
    const rollBackAbove: CustomThreshold = {
      ...above(MAX_5XX_PERCENT),
      datapointsToAlarm: ROLLBACK_MINUTES,
      evaluationPeriods: ROLLBACK_MINUTES,
    };
    const requests = loadBalancer.metrics.requestCount({ statistic: Stats.SUM });
    const elb5xx = loadBalancer.metrics.httpCodeElb(HttpCodeElb.ELB_5XX_COUNT, {
      statistic: Stats.SUM,
    });
    const target5xx = loadBalancer.metrics.httpCodeTarget(HttpCodeTarget.TARGET_5XX_COUNT, {
      statistic: Stats.SUM,
    });
    alarms.monitoring
      .monitorCustom({
        alarmFriendlyName: 'LoadBalancer',
        humanReadableName: 'Load balancer',
        metricGroups: [
          {
            title: '5xx answers of its own',
            metrics: [
              {
                // 502, 503 and 504: no task answered, or none in time.
                metric: elb5xx,
                alarmFriendlyName: '5xx',
                addAlarm: { Warning: above(0) },
              },
            ],
          },
          {
            title: '5xx answers of its own, % of requests',
            metrics: [
              {
                // RequestCount leaves out what the load balancer answered
                // before it chose a task, a 503 when none is healthy, so the
                // 5xx are added to it; a 502 or 504 is in both, which at
                // worst halves the share. Metric math drops the minutes with
                // no request (a division by zero): no request is no 5xx.
                metric: new MathExpression({
                  expression: '100 * errors / (requests + errors)',
                  usingMetrics: { errors: elb5xx, requests },
                  period: Duration.minutes(1),
                  label: '5xx, % of requests',
                }),
                alarmFriendlyName: '5xx-Rate',
                addAlarm: { Critical: rollBackAbove },
              },
            ],
          },
        ],
      })
      .monitorCustom({
        alarmFriendlyName: 'Service',
        humanReadableName: 'Service',
        metricGroups: [
          {
            title: '5xx answers from the tasks',
            metrics: [
              {
                metric: target5xx,
                alarmFriendlyName: '5xx',
                addAlarm: { Warning: above(0) },
              },
            ],
          },
          {
            title: '5xx answers from the tasks, % of requests',
            metrics: [
              {
                // Every request a task answered is in RequestCount.
                metric: new MathExpression({
                  expression: '100 * errors / requests',
                  usingMetrics: { errors: target5xx, requests },
                  period: Duration.minutes(1),
                  label: '5xx, % of requests',
                }),
                alarmFriendlyName: '5xx-Rate',
                addAlarm: { Critical: rollBackAbove },
              },
            ],
          },
          {
            title: 'Latency',
            metrics: [
              {
                metric: loadBalancer.metrics.targetResponseTime({ statistic: Stats.p(99) }),
                alarmFriendlyName: 'Latency-P99',
                addAlarm: { Warning: above(MAX_LATENCY.toSeconds()) },
              },
            ],
          },
          {
            title: 'Running tasks',
            metrics: [
              {
                // Container Insights' count, against the fewest the service runs.
                metric: new Metric({
                  namespace: 'ECS/ContainerInsights',
                  metricName: 'RunningTaskCount',
                  dimensionsMap: {
                    ClusterName: cluster.clusterName,
                    ServiceName: service.serviceName,
                  },
                  statistic: Stats.AVERAGE,
                }),
                alarmFriendlyName: 'Running-Tasks',
                addAlarm: {
                  Critical: {
                    threshold: MIN_TASKS,
                    comparisonOperator: ComparisonOperator.LESS_THAN_THRESHOLD,
                    // Container Insights' guide doesn't promise a count of
                    // 0 while no task runs: no data is taken for none.
                    treatMissingDataOverride: TreatMissingData.BREACHING,
                  },
                },
              },
            ],
          },
        ],
      })
      .monitorFargateApplicationLoadBalancer({
        fargateService: service,
        applicationLoadBalancer: loadBalancer,
        applicationTargetGroup: targetGroup,
        alarmFriendlyName: 'Service',
        // Unhealthy tasks by the library's statistic, the Maximum: a task any
        // load balancer node finds unhealthy. ECS replaces a failing task in
        // a minute or two, so a five-minute period, or the Minimum (every
        // node agreeing), would rarely see it: one-minute periods, two bad
        // ones of three, so a task one node doubted for a moment rolls
        // nothing back.
        addUnhealthyTaskCountAlarm: {
          Critical: {
            maxUnhealthyTasks: 0,
            period: Duration.minutes(1),
            datapointsToAlarm: 2,
            evaluationPeriods: 3,
          },
        },
        addCpuUsageAlarm: { Warning: { maxUsagePercent: 90 } },
        addMemoryUsageAlarm: { Warning: { maxUsagePercent: 90 } },
      })
      .monitorDynamoTable({
        table,
        alarmFriendlyName: 'Items',
        billingMode: BillingMode.PAY_PER_REQUEST,
        addSystemErrorCountAlarm: { Critical: { maxErrorCount: 0 } },
        addReadThrottledEventsCountAlarm: { Critical: { maxThrottledEventsThreshold: 0 } },
        addWriteThrottledEventsCountAlarm: { Critical: { maxThrottledEventsThreshold: 0 } },
      });
    for (const [name, store] of stores) {
      alarms.monitoring.monitorDynamoTable({
        table: store,
        alarmFriendlyName: name,
        billingMode: BillingMode.PAY_PER_REQUEST,
        addSystemErrorCountAlarm: { Critical: { maxErrorCount: 0 } },
        addReadThrottledEventsCountAlarm: { Critical: { maxThrottledEventsThreshold: 0 } },
        addWriteThrottledEventsCountAlarm: { Critical: { maxThrottledEventsThreshold: 0 } },
      });
    }

    // A deployment that sets off one of these rolls back. ECS reads the
    // alarms by name: the names are written out, since a reference to an
    // alarm, whose metric names the service, would make the two depend on
    // each other.
    const rollBackOn = DEPLOYMENT_ALARMS.map((alarm) => `${this.stackName}-${alarm}`);
    const created = new Set(alarms.monitoring.createdAlarms().map((alarm) => alarm.alarmName));
    for (const name of rollBackOn) {
      if (!created.has(name)) {
        throw new Error(`The deployment alarm ${name} is not one of the stack's alarms.`);
      }
    }
    service.enableDeploymentAlarms(rollBackOn, { behavior: AlarmBehavior.ROLLBACK_ON_ALARM });

    if (rules.backups && backups.retentionDays > 0) {
      new Backups(this, 'Backups', {
        retentionDays: backups.retentionDays,
        copyToRegion: backups.copyToRegion,
        tables: [table, ...stores.map(([, store]) => store)],
      });
    }

    new CfnOutput(this, 'EntryPoint', {
      description:
        'The load balancer’s DNS name: point the certificate’s domain at it (a CNAME or an alias record).',
      value: loadBalancer.loadBalancerDnsName,
    });
  }

  /**
   * Runs the AWS Distro for OpenTelemetry collector beside `app`, which
   * sends it its spans, and lets the task write them to X-Ray.
   */
  private traceThroughCollector(
    taskDefinition: FargateTaskDefinition,
    app: ContainerDefinition,
    logRetention: RetentionDays,
  ): void {
    const collectorLogs = new LogGroup(this, 'CollectorLogs', {
      retention: logRetention,
      removalPolicy: RemovalPolicy.DESTROY,
    });
    const collector = taskDefinition.addContainer('Collector', {
      image: ContainerImage.fromRegistry(COLLECTOR_IMAGE),
      command: [`--config=${COLLECTOR_CONFIG}`],
      cpu: COLLECTOR_CPU,
      memoryLimitMiB: COLLECTOR_MEMORY_MIB,
      // A collector that stops loses traces, not requests: the task goes on.
      essential: false,
      logging: LogDrivers.awsLogs({ streamPrefix: 'collector', logGroup: collectorLogs }),
      readonlyRootFilesystem: true,
    });
    app.addContainerDependencies({
      container: collector,
      condition: ContainerDependencyCondition.START,
    });
    app.addEnvironment('OTEL_EXPORTER_OTLP_ENDPOINT', OTLP_ENDPOINT);
    app.addEnvironment('OTEL_SERVICE_NAME', this.stackName);
    taskDefinition.taskRole.addToPrincipalPolicy(
      new PolicyStatement({ actions: XRAY_WRITE_ACTIONS, resources: ['*'] }),
    );
  }

  /**
   * Writes the load balancer's access logs to `bucket`. Not CDK's
   * `logAccessLogs`, which needs the stack's region to pick a principal: the
   * log delivery service principal works in every region, so the stack stays
   * environment-agnostic.
   */
  private logAccessLogs(loadBalancer: ApplicationLoadBalancer, bucket: Bucket): void {
    const prefix = 'load-balancer';
    loadBalancer.setAttribute('access_logs.s3.enabled', 'true');
    loadBalancer.setAttribute('access_logs.s3.bucket', bucket.bucketName);
    loadBalancer.setAttribute('access_logs.s3.prefix', prefix);
    bucket.addToResourcePolicy(
      new PolicyStatement({
        actions: ['s3:PutObject'],
        principals: [new ServicePrincipal('logdelivery.elasticloadbalancing.amazonaws.com')],
        resources: [bucket.arnForObjects(`${prefix}/AWSLogs/${this.account}/*`)],
        // Only this account's load balancers in this region.
        conditions: {
          ArnLike: {
            'aws:SourceArn': `arn:${this.partition}:elasticloadbalancing:${this.region}:${this.account}:loadbalancer/*`,
          },
        },
      }),
    );
    // The load balancer checks it may write when access logs turn on.
    const policy = bucket.policy?.node.defaultChild;
    const balancer = loadBalancer.node.defaultChild;
    if (CfnResource.isCfnResource(policy) && CfnResource.isCfnResource(balancer)) {
      balancer.addResourceDependency(policy);
    }
  }
}
