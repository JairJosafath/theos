import { CfnOutput, CfnParameter, Duration, RemovalPolicy, Stack, Validations } from 'aws-cdk-lib';
import type { StackProps } from 'aws-cdk-lib';
import {
  AdditionalHealthCheckType,
  AutoScalingGroup,
  GroupMetrics,
  HealthChecks,
  Signals,
  UpdatePolicy,
} from 'aws-cdk-lib/aws-autoscaling';
import { ComparisonOperator, Metric, Stats, TreatMissingData } from 'aws-cdk-lib/aws-cloudwatch';
import { AttributeType, BillingMode, Table, TableEncryption } from 'aws-cdk-lib/aws-dynamodb';
import {
  BlockDeviceVolume,
  EbsDeviceVolumeType,
  FlowLogDestination,
  FlowLogTrafficType,
  GatewayVpcEndpointAwsService,
  InstanceClass,
  InstanceSize,
  InstanceType,
  InterfaceVpcEndpointAwsService,
  IpAddresses,
  LaunchTemplate,
  MachineImage,
  Peer,
  Port,
  SecurityGroup,
  SubnetType,
  UserData,
  Vpc,
  AmazonLinuxCpuType,
} from 'aws-cdk-lib/aws-ec2';
import {
  ApplicationLoadBalancer,
  ApplicationProtocol,
  HttpCodeElb,
  HttpCodeTarget,
  ListenerCertificate,
  SslPolicy,
} from 'aws-cdk-lib/aws-elasticloadbalancingv2';
import { AnyPrincipal, PolicyStatement, Role, ServicePrincipal } from 'aws-cdk-lib/aws-iam';
import { LogGroup } from 'aws-cdk-lib/aws-logs';
import { CfnDBCluster } from 'aws-cdk-lib/aws-rds';
import type { DatabaseCluster } from 'aws-cdk-lib/aws-rds';
import { BlockPublicAccess, Bucket, BucketEncryption } from 'aws-cdk-lib/aws-s3';
import type { CustomThreshold } from 'cdk-monitoring-constructs';
import type { Construct } from 'constructs';

import { Alarms } from './alarms';
import { Backups } from './backups';
import { DATA_STORES } from './connectors';
import type { Connect, DataStore } from './connectors';
import { serverAsset } from './server_asset';
import { TIER_RULES } from './standard';
import type { Operations, Tier } from './standard';
import { Users } from './user_pool';

/** The VPC's addresses; written out, so CDK Nag can read the rules that name them. */
export const VPC_CIDR = '10.0.0.0/16';

/** The port the server listens on, behind the load balancer. */
export const SERVER_PORT = 8080;

/** The fewest instances the group runs, one per zone, and the most it scales to. */
export const MIN_INSTANCES = 2;
export const MAX_INSTANCES = 4;

/**
 * How long a new instance has to boot, start the server and pass its own
 * health check before it signals CloudFormation; past it, the instance
 * counts as failed and the update rolls back.
 */
export const SIGNAL_TIMEOUT = Duration.minutes(15);

// How long a booted instance waits for its server to answer /health before
// it gives up and signals a failure.
const HEALTH_WAIT_SECONDS = 300;

// How long a new instance may fail the load balancer's health check before
// the group replaces it: its boot plus the health wait, with room to spare.
const HEALTH_CHECK_GRACE = Duration.minutes(10);

// The slowest answer the latency alarm allows, at the 99th percentile: an
// item is one DynamoDB call, which takes milliseconds.
const MAX_LATENCY = Duration.seconds(1);
// Aurora PostgreSQL's max_connections at a Serverless v2 cluster's largest
// capacity, the lowest column of the table in the Aurora User Guide's
// "Maximum connections for Aurora Serverless v2", interpolated between its
// rows: the documented formula, LEAST(DBInstanceClassMemory / 9531392, 5000),
// counts memory after what the OS keeps, so 2 GiB an ACU overstates it.
const MAX_CONNECTIONS_BY_ACU: readonly (readonly [number, number])[] = [
  [1, 189],
  [4, 823],
  [8, 1669],
  [16, 3360],
  [32, 5000],
];
// A cluster whose minimum is 0 or 0.5 ACU stops at 2,000 whatever its largest.
const LOW_MINIMUM_MAX_CONNECTIONS = 2000;

/** The documented max_connections at `acu`, the first row's below it and the last's above. */
function documentedMaxConnections(acu: number): number {
  let previous: readonly [number, number] | undefined;
  for (const row of MAX_CONNECTIONS_BY_ACU) {
    const [rowAcu, rowConnections] = row;
    if (acu >= rowAcu) {
      previous = row;
      continue;
    }
    if (previous === undefined) {
      return rowConnections;
    }
    const [belowAcu, below] = previous;
    return Math.floor(below + ((rowConnections - below) * (acu - belowAcu)) / (rowAcu - belowAcu));
  }
  return previous === undefined ? 0 : previous[1];
}

/**
 * Nine tenths of the connections `cluster` allows at its largest, from the
 * Serverless v2 scaling configuration its Connector gave it; 1 ACU's when it
 * has none the stack can read.
 */
export function maxConnections(cluster: DatabaseCluster): number {
  // The configuration is a lazy token until resolved; a provisioned cluster has none.
  const scaling: unknown = Stack.of(cluster).resolve(
    (cluster.node.defaultChild as CfnDBCluster).serverlessV2ScalingConfiguration,
  );
  const capacity = (name: 'minCapacity' | 'maxCapacity'): number =>
    typeof scaling === 'object' &&
    scaling !== null &&
    name in scaling &&
    typeof (scaling as Record<string, unknown>)[name] === 'number'
      ? ((scaling as Record<string, unknown>)[name] as number)
      : 1;
  const documented = documentedMaxConnections(capacity('maxCapacity'));
  const connections =
    capacity('minCapacity') < 1 ? Math.min(documented, LOW_MINIMUM_MAX_CONNECTIONS) : documented;
  return Math.floor(connections * 0.9);
}

/** What the stack is built for. */
export interface ServiceStackProps extends StackProps {
  /** Where it runs, which decides what it keeps, backs up and how long it keeps its logs. */
  readonly tier: Tier;
  /** What the Container promises whatever the tier: `OPERATIONS`. */
  readonly operations: Operations;
  /** The data stores it connects the service to: `DATA_STORES` unless a test names others. */
  readonly dataStores?: readonly (readonly [string, Connect])[];
}

/**
 * The Container's one stack: an internal Application Load Balancer serving
 * HTTPS with the certificate the deployer names, in front of an Auto Scaling
 * group of EC2 instances that run the HTTP server in `src/`, keeping items in
 * a DynamoDB table. Every route but `/health` wants an access token of the
 * stack's Cognito user pool. The instances have no route to the internet:
 * they reach S3, DynamoDB, CloudWatch Logs, CloudFormation and Cognito
 * through VPC endpoints, and only the load balancer reaches them. Every part has alarms, to a topic the
 * stack outputs with the load balancer's name; a rolling update waits for
 * each new instance to signal it is healthy, and stops when one doesn't;
 * production keeps and backs up the data.
 */
export class ServiceStack extends Stack {
  /** The load balancer's DNS name, reachable from inside the VPC. */
  readonly dnsName: string;

  constructor(scope: Construct, id: string, props: ServiceStackProps) {
    super(scope, id, props);
    const rules = TIER_RULES[props.tier];
    const { backups, alerts } = props.operations;

    // The certificate is the deployer's: an ACM certificate for the domain
    // the service answers on inside the VPC. A parameter, so synth needs no
    // account.
    const certificateArn = new CfnParameter(this, 'CertificateArn', {
      type: 'String',
      description: 'The ARN of the ACM certificate the load balancer serves HTTPS with.',
      allowedPattern: '^arn:aws[a-z-]*:acm:[a-z0-9-]+:[0-9]{12}:certificate/.+$',
    });

    // The table holds the data: kept, protected and recoverable where the
    // tier keeps data, thrown away with the stack where it doesn't.
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

    // Two zones of isolated subnets and no NAT gateway: what the instances
    // need from AWS comes through endpoints, and nothing else gets out.
    const vpc = new Vpc(this, 'Network', {
      ipAddresses: IpAddresses.cidr(VPC_CIDR),
      maxAzs: 2,
      natGateways: 0,
      subnetConfiguration: [
        { name: 'service', subnetType: SubnetType.PRIVATE_ISOLATED, cidrMask: 24 },
      ],
      // The custom resource that empties the default security group is a
      // Lambda function with a managed role; nothing here uses that group.
      restrictDefaultSecurityGroup: false,
      flowLogs: {
        rejected: {
          // What the security groups refuse: the traffic worth looking into.
          trafficType: FlowLogTrafficType.REJECT,
          destination: FlowLogDestination.toCloudWatchLogs(
            new LogGroup(this, 'FlowLogs', {
              retention: rules.logRetention,
              removalPolicy: RemovalPolicy.DESTROY,
            }),
          ),
        },
      },
    });
    // S3 for the server's code and the operating system's packages (Amazon
    // Linux 2023's repositories are in S3), DynamoDB for the items.
    vpc.addGatewayEndpoint('S3', { service: GatewayVpcEndpointAwsService.S3 });
    vpc.addGatewayEndpoint('DynamoDb', { service: GatewayVpcEndpointAwsService.DYNAMODB });
    // CloudWatch Logs for the server's log, which the CloudWatch agent
    // ships; CloudFormation for the signal each new instance sends once its
    // server is healthy.
    const endpointSecurityGroup = new SecurityGroup(this, 'EndpointSecurityGroup', {
      vpc,
      description: 'The VPC endpoints: HTTPS from inside the VPC.',
      allowAllOutbound: false,
    });
    endpointSecurityGroup.addIngressRule(
      Peer.ipv4(VPC_CIDR),
      Port.tcp(443),
      'HTTPS from inside the VPC',
    );
    vpc.addInterfaceEndpoint('Logs', {
      service: InterfaceVpcEndpointAwsService.CLOUDWATCH_LOGS,
      securityGroups: [endpointSecurityGroup],
      open: false,
    });
    vpc.addInterfaceEndpoint('CloudFormation', {
      service: InterfaceVpcEndpointAwsService.CLOUDFORMATION,
      securityGroups: [endpointSecurityGroup],
      open: false,
    });
    // Cognito for the user pool's signing keys, which the server fetches to
    // verify a token.
    vpc.addInterfaceEndpoint('CognitoIdp', {
      service: InterfaceVpcEndpointAwsService.COGNITO_IDP,
      securityGroups: [endpointSecurityGroup],
      open: false,
    });

    const users = new Users(this, 'Users', { keepData: rules.keepData });

    const serverLogs = new LogGroup(this, 'ServerLogs', {
      retention: rules.logRetention,
      removalPolicy: RemovalPolicy.DESTROY,
    });

    // The instances' own role: no managed policy, only the calls they make;
    // addSignalOnExitCommand below lets it signal this stack.
    const role = new Role(this, 'InstanceRole', {
      assumedBy: new ServicePrincipal('ec2.amazonaws.com'),
    });
    table.grant(role, 'dynamodb:GetItem', 'dynamodb:PutItem');
    // The instances' security group, made before the stores so a cluster can
    // let the instances in; the load balancer adds its own rule when it
    // targets them.
    const instanceSecurityGroup = new SecurityGroup(this, 'InstanceSecurityGroup', {
      vpc,
      description: 'The service instances: only the load balancer reaches them.',
      allowAllOutbound: true,
    });
    // The DataStores the Container's Relationships name, from their
    // Connectors: a table or an Aurora cluster each. Each grants the instance
    // role its calls and puts its names in the environment the server's unit
    // is rendered with; a cluster goes in the VPC's isolated subnets and lets
    // the instances in, and the server gets its host and port too. Every
    // store is connected here, before bootCommands renders the unit: an
    // addEnvironment after that would be dropped.
    const environment: Record<string, string> = { ...users.environment };
    const user = {
      grantPrincipal: role,
      addEnvironment: (key: string, value: string) => {
        environment[key] = value;
      },
      network: { vpc, connections: instanceSecurityGroup.connections },
    };
    const stores = (props.dataStores ?? DATA_STORES).map(
      ([name, connect]) => [name, connect(this, user, rules)] as const,
    );
    const isTable = (store: DataStore): store is Table => store instanceof Table;
    const tables = stores.flatMap(([name, store]) =>
      isTable(store) ? [[name, store] as const] : [],
    );
    const clusters = stores.flatMap(([name, store]) =>
      isTable(store) ? [] : [[name, store] as const],
    );
    if (clusters.length > 0) {
      // A cluster's secret, which a driver fetches and its rotation function
      // changes, and its Data API: the instances have no other way to them.
      vpc.addInterfaceEndpoint('SecretsManager', {
        service: InterfaceVpcEndpointAwsService.SECRETS_MANAGER,
        securityGroups: [endpointSecurityGroup],
        open: false,
      });
      vpc.addInterfaceEndpoint('RdsData', {
        service: InterfaceVpcEndpointAwsService.RDS_DATA,
        securityGroups: [endpointSecurityGroup],
        open: false,
      });
    }
    serverLogs.grantWrite(role);
    const server = serverAsset(this, 'Server');
    role.addToPrincipalPolicy(
      new PolicyStatement({
        actions: ['s3:GetObject'],
        resources: [server.bucket.arnForObjects(server.s3ObjectKey)],
      }),
    );

    const userData = UserData.forLinux();
    // Stop at the first failing command, the download included: the exit
    // trap below then signals the failure, and the instance is replaced.
    userData.addCommands('set -euo pipefail', INSTALL_COMMAND);
    const serverFile = userData.addS3DownloadCommand({
      bucket: server.bucket,
      bucketKey: server.s3ObjectKey,
      region: this.region,
    });
    userData.addCommands(
      ...bootCommands(
        serverFile,
        table.tableName,
        serverLogs.logGroupName,
        this.region,
        environment,
      ),
    );

    const launchTemplate = new LaunchTemplate(this, 'LaunchTemplate', {
      // Graviton: the same work for less energy and money than x86.
      instanceType: InstanceType.of(InstanceClass.T4G, InstanceSize.SMALL),
      machineImage: MachineImage.latestAmazonLinux2023({ cpuType: AmazonLinuxCpuType.ARM_64 }),
      role,
      userData,
      securityGroup: instanceSecurityGroup,
      requireImdsv2: true,
      detailedMonitoring: true,
      blockDevices: [
        {
          // The root volume holds the system and the server's code, never
          // data (docs/style/aws.md): it goes with its instance and is not
          // backed up.
          deviceName: '/dev/xvda',
          volume: BlockDeviceVolume.ebs(8, {
            volumeType: EbsDeviceVolumeType.GP3,
            encrypted: true,
            deleteOnTermination: true,
          }),
        },
      ],
    });

    // Each alarm fires on the first bad five minutes; docs/runbook.md says
    // what each means and what to do first. The group's scaling events go
    // to the same topic.
    const alarms = new Alarms(this, 'Alarms', alerts.email);

    const group = new AutoScalingGroup(this, 'Instances', {
      vpc,
      vpcSubnets: { subnetType: SubnetType.PRIVATE_ISOLATED },
      launchTemplate,
      // Two instances in two zones, so one zone or instance can fail.
      minCapacity: MIN_INSTANCES,
      maxCapacity: MAX_INSTANCES,
      cooldown: Duration.minutes(5),
      healthChecks: HealthChecks.withAdditionalChecks({
        additionalTypes: [AdditionalHealthCheckType.ELB],
        // Longer than a boot: the packages, the download and the server's
        // health wait (HEALTH_WAIT_SECONDS) all come before the instance
        // signals, and the group must not replace it for failing the load
        // balancer's check meanwhile.
        gracePeriod: HEALTH_CHECK_GRACE,
      }),
      groupMetrics: [GroupMetrics.all()],
      // CloudFormation waits for each new instance's cfn-signal, which it
      // sends once its server answers /health (bootCommands): at creation
      // for the first two, and in a rolling update for each replacement.
      // A failed or missing signal fails the update, and CloudFormation
      // rolls the stack back to the instances it had.
      signals: Signals.waitForMinCapacity({ timeout: SIGNAL_TIMEOUT }),
      // Replace one instance at a time, starting its replacement first, so
      // the group never runs fewer than its two.
      updatePolicy: UpdatePolicy.rollingUpdate({
        maxBatchSize: 1,
        minInstancesInService: MIN_INSTANCES,
        waitOnResourceSignals: true,
        pauseTime: SIGNAL_TIMEOUT,
      }),
      notifications: [{ topic: alarms.topic }],
    });
    group.scaleOnCpuUtilization('Cpu', { targetUtilizationPercent: 60 });
    // On exit, the boot script signals the group with its exit status: 0
    // once the server is healthy, anything else if a step failed.
    userData.addSignalOnExitCommand(group);
    // Auto Scaling publishes the group's notifications as its service-linked
    // role, which the topic's key must let encrypt them. The role may not
    // exist before the group does, and a key policy can't name a principal
    // that doesn't, so the statement names it by condition.
    alarms.key.addToResourcePolicy(
      new PolicyStatement({
        principals: [new AnyPrincipal()],
        actions: ['kms:Decrypt', 'kms:GenerateDataKey*'],
        resources: ['*'],
        conditions: {
          ArnEquals: {
            'aws:PrincipalArn': `arn:${this.partition}:iam::${this.account}:role/aws-service-role/autoscaling.amazonaws.com/AWSServiceRoleForAutoScaling`,
          },
          StringEquals: { 'kms:CallerAccount': this.account },
        },
      }),
    );

    const accessLogs = new Bucket(this, 'AccessLogs', {
      encryption: BucketEncryption.S3_MANAGED,
      enforceSSL: true,
      blockPublicAccess: BlockPublicAccess.BLOCK_ALL,
      // Logs, not data: they expire after the tier's log retention.
      lifecycleRules: [{ expiration: Duration.days(rules.logRetention) }],
      // CloudFormation can't delete a bucket that holds objects: deleting the
      // stack keeps it, in either tier, emptying itself as the logs expire.
      removalPolicy: RemovalPolicy.RETAIN,
    });
    Validations.of(accessLogs).acknowledge({
      id: 'AwsSolutions-S1',
      reason:
        'This bucket holds the load balancer access logs; logging access to it would need another bucket, whose own access would go unlogged.',
    });

    const loadBalancerSecurityGroup = new SecurityGroup(this, 'LoadBalancerSecurityGroup', {
      vpc,
      description: 'The load balancer: HTTPS from inside the VPC.',
      allowAllOutbound: false,
    });
    loadBalancerSecurityGroup.addIngressRule(
      Peer.ipv4(VPC_CIDR),
      Port.tcp(443),
      'HTTPS from inside the VPC',
    );
    const loadBalancer = new ApplicationLoadBalancer(this, 'LoadBalancer', {
      vpc,
      internetFacing: false,
      securityGroup: loadBalancerSecurityGroup,
      dropInvalidHeaderFields: true,
    });
    // Access logs, written by the log delivery service, which every region
    // supports; logAccessLogs would need the stack's region at synth time to
    // pick a regional account, and this stack names no region.
    accessLogs.addToResourcePolicy(
      new PolicyStatement({
        principals: [new ServicePrincipal('logdelivery.elasticloadbalancing.amazonaws.com')],
        actions: ['s3:PutObject'],
        resources: [accessLogs.arnForObjects(`AWSLogs/${this.account}/*`)],
        conditions: {
          ArnLike: {
            'aws:SourceArn': `arn:${this.partition}:elasticloadbalancing:${this.region}:${this.account}:loadbalancer/*`,
          },
        },
      }),
    );
    loadBalancer.setAttribute('access_logs.s3.enabled', 'true');
    loadBalancer.setAttribute('access_logs.s3.bucket', accessLogs.bucketName);
    // The load balancer checks it may write to the bucket when it is created.
    if (accessLogs.policy !== undefined) {
      loadBalancer.node.addDependency(accessLogs.policy);
    }
    // HTTPS only, even inside the VPC: no listener speaks plain HTTP.
    const listener = loadBalancer.addListener('Https', {
      port: 443,
      protocol: ApplicationProtocol.HTTPS,
      certificates: [ListenerCertificate.fromArn(certificateArn.valueAsString)],
      sslPolicy: SslPolicy.RECOMMENDED_TLS,
      open: false,
    });
    const targetGroup = listener.addTargets('Service', {
      port: SERVER_PORT,
      protocol: ApplicationProtocol.HTTP,
      targets: [group],
      healthCheck: { path: '/health', healthyHttpCodes: '200', interval: Duration.seconds(15) },
      deregistrationDelay: Duration.seconds(30),
    });
    this.dnsName = loadBalancer.loadBalancerDnsName;

    // No requests is no 5xx and no slow answer.
    const above = (threshold: number): CustomThreshold => ({
      threshold,
      comparisonOperator: ComparisonOperator.GREATER_THAN_THRESHOLD,
      treatMissingDataOverride: TreatMissingData.NOT_BREACHING,
    });
    const groupMetric = (namespace: string, metricName: string): Metric =>
      new Metric({
        namespace,
        metricName,
        dimensionsMap: { AutoScalingGroupName: group.autoScalingGroupName },
        statistic: Stats.AVERAGE,
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
                // 502, 503 and 504: no instance answered, or none in time.
                metric: loadBalancer.metrics.httpCodeElb(HttpCodeElb.ELB_5XX_COUNT, {
                  statistic: Stats.SUM,
                }),
                alarmFriendlyName: '5xx',
                addAlarm: { Critical: above(0) },
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
            title: '5xx answers from the instances',
            metrics: [
              {
                metric: loadBalancer.metrics.httpCodeTarget(HttpCodeTarget.TARGET_5XX_COUNT, {
                  statistic: Stats.SUM,
                }),
                alarmFriendlyName: '5xx',
                addAlarm: { Critical: above(0) },
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
            title: 'Unhealthy instances',
            metrics: [
              {
                // An instance is unhealthy when every load balancer node
                // finds it so (the minimum), as Elastic Load Balancing's
                // guide advises.
                metric: targetGroup.metrics.unhealthyHostCount({ statistic: Stats.MINIMUM }),
                alarmFriendlyName: 'Unhealthy-Instances',
                addAlarm: { Critical: above(0) },
              },
            ],
          },
        ],
      })
      .monitorCustom({
        alarmFriendlyName: 'Instances',
        humanReadableName: 'Instances',
        metricGroups: [
          {
            title: 'CPU',
            metrics: [
              {
                // Above the scaling policy's 60 %: scaling is at its most
                // instances, or can't keep up.
                metric: groupMetric('AWS/EC2', 'CPUUtilization'),
                alarmFriendlyName: 'CPU-Usage',
                addAlarm: {
                  Warning: {
                    threshold: 90,
                    comparisonOperator: ComparisonOperator.GREATER_THAN_THRESHOLD,
                  },
                },
              },
            ],
          },
          {
            title: 'Instances in service',
            metrics: [
              {
                // The group's own count, against the fewest it runs.
                metric: groupMetric('AWS/AutoScaling', 'GroupInServiceInstances'),
                alarmFriendlyName: 'In-Service',
                addAlarm: {
                  Critical: {
                    threshold: MIN_INSTANCES,
                    comparisonOperator: ComparisonOperator.LESS_THAN_THRESHOLD,
                  },
                },
              },
            ],
          },
        ],
      })
      .monitorDynamoTable({
        table,
        alarmFriendlyName: 'Items',
        billingMode: BillingMode.PAY_PER_REQUEST,
        addSystemErrorCountAlarm: { Critical: { maxErrorCount: 0 } },
        addReadThrottledEventsCountAlarm: { Critical: { maxThrottledEventsThreshold: 0 } },
        addWriteThrottledEventsCountAlarm: { Critical: { maxThrottledEventsThreshold: 0 } },
      });
    for (const [name, store] of tables) {
      alarms.monitoring.monitorDynamoTable({
        table: store,
        alarmFriendlyName: name,
        billingMode: BillingMode.PAY_PER_REQUEST,
        addSystemErrorCountAlarm: { Critical: { maxErrorCount: 0 } },
        addReadThrottledEventsCountAlarm: { Critical: { maxThrottledEventsThreshold: 0 } },
        addWriteThrottledEventsCountAlarm: { Critical: { maxThrottledEventsThreshold: 0 } },
      });
    }
    // A cluster near its CPU or its connections is one the server will soon
    // find slow or closed.
    for (const [name, cluster] of clusters) {
      alarms.monitoring.monitorAuroraCluster({
        // The cluster itself, not its identifier: the monitoring reads its
        // ARN to tell a Serverless cluster.
        cluster,
        alarmFriendlyName: name,
        addCpuUsageAlarm: { Critical: { maxUsagePercent: 90 } },
        addMaxConnectionCountAlarm: { Critical: { maxConnectionCount: maxConnections(cluster) } },
      });
    }

    if (rules.backups && backups.retentionDays > 0) {
      new Backups(this, 'Backups', {
        retentionDays: backups.retentionDays,
        copyToRegion: backups.copyToRegion,
        tables: [table, ...tables.map(([, store]) => store)],
        clusters: clusters.map(([, cluster]) => cluster),
      });
    }

    new CfnOutput(this, 'EntryPoint', {
      description:
        'The load balancer’s DNS name, inside the VPC: point the certificate’s domain at it (a CNAME or an alias record).',
      value: this.dnsName,
    });
  }
}

/**
 * The packages each instance installs at its first boot, before anything
 * else: Node.js, the CloudWatch agent, logrotate, and cfn-signal
 * (aws-cfn-bootstrap), which the exit trap needs even when a later step fails.
 */
export const INSTALL_COMMAND =
  'dnf install -y nodejs22 amazon-cloudwatch-agent logrotate aws-cfn-bootstrap';

/**
 * The shell commands each instance runs at its first boot, after it has
 * installed its packages (`INSTALL_COMMAND`) and downloaded the server to
 * `serverFile`: run the server as a systemd service under its own user, with
 * `environment` (the user pool's) beside the table and region, ship
 * its log, and wait until the server answers `/health`, so the boot script
 * exits 0, and signals CloudFormation that the instance is ready, only then.
 */
export function bootCommands(
  serverFile: string,
  tableName: string,
  logGroupName: string,
  region: string,
  environment: Record<string, string>,
): string[] {
  const agentConfig = JSON.stringify({
    logs: {
      logs_collected: {
        files: {
          collect_list: [
            {
              file_path: '/var/log/server/server.log',
              log_group_name: logGroupName,
              log_stream_name: '{instance_id}',
            },
          ],
        },
      },
    },
  });
  return [
    'useradd --system --no-create-home --shell /sbin/nologin server',
    'install -d -o root -g root -m 0755 /opt/server',
    `install -o root -g root -m 0644 '${serverFile}' /opt/server/server.mjs`,
    'install -d -o server -g server -m 0750 /var/log/server',
    // Amazon Linux names a versioned Node `node-22`; fall back to `node`.
    'NODE="$(command -v node-22 || command -v node)"',
    `cat > /etc/systemd/system/server.service <<UNIT
[Unit]
Description=The HTTP server
After=network-online.target
Wants=network-online.target
# Keep restarting: a server that stays down fails the health check and its
# instance is replaced, which is slower than a restart.
StartLimitIntervalSec=0

[Service]
User=server
Environment=NODE_ENV=production
Environment=PORT=${String(SERVER_PORT)}
Environment=AWS_REGION=${region}
Environment=TABLE_NAME=${tableName}
${Object.entries(environment)
  .map(([name, value]) => `Environment=${name}=${value}`)
  .join('\n')}
ExecStart=$NODE /opt/server/server.mjs
StandardOutput=append:/var/log/server/server.log
StandardError=append:/var/log/server/server.log
Restart=always
RestartSec=2
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/log/server

[Install]
WantedBy=multi-user.target
UNIT`,
    // The agent has shipped what was written; keep a few days on the disk.
    `cat > /etc/logrotate.d/server <<'ROTATE'
/var/log/server/server.log {
  daily
  maxsize 100M
  rotate 3
  compress
  missingok
  notifempty
  copytruncate
}
ROTATE`,
    'systemctl enable --now logrotate.timer',
    `cat > /opt/aws/amazon-cloudwatch-agent/etc/server.json <<'AGENT'\n${agentConfig}\nAGENT`,
    '/opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl -a fetch-config -m ec2 -s -c file:/opt/aws/amazon-cloudwatch-agent/etc/server.json',
    'systemctl daemon-reload',
    'systemctl enable --now server.service',
    // The signal says the server is healthy, not just started: five minutes
    // to answer, or the script fails and the instance signals the failure.
    `timeout ${String(HEALTH_WAIT_SECONDS)} bash -c 'until curl -fsS -o /dev/null http://127.0.0.1:${String(SERVER_PORT)}/health; do sleep 5; done'`,
  ];
}
