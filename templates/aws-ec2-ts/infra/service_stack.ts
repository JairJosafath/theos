import { Duration, RemovalPolicy, Stack, Validations } from 'aws-cdk-lib';
import type { StackProps } from 'aws-cdk-lib';
import {
  AdditionalHealthCheckType,
  AutoScalingGroup,
  GroupMetrics,
  HealthChecks,
  UpdatePolicy,
} from 'aws-cdk-lib/aws-autoscaling';
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
} from 'aws-cdk-lib/aws-elasticloadbalancingv2';
import { PolicyStatement, Role, ServicePrincipal } from 'aws-cdk-lib/aws-iam';
import { LogGroup, RetentionDays } from 'aws-cdk-lib/aws-logs';
import { BlockPublicAccess, Bucket, BucketEncryption } from 'aws-cdk-lib/aws-s3';
import type { Construct } from 'constructs';

import { serverAsset } from './server_asset';

/** The VPC's addresses; written out, so CDK Nag can read the rules that name them. */
export const VPC_CIDR = '10.0.0.0/16';

/** The port the server listens on, behind the load balancer. */
export const SERVER_PORT = 8080;

/**
 * The Container's one stack: an internal Application Load Balancer in front
 * of an Auto Scaling group of EC2 instances that run the HTTP server in
 * `src/`, keeping items in a DynamoDB table. The instances have no route to
 * the internet: they reach S3, DynamoDB and CloudWatch Logs through VPC
 * endpoints, and only the load balancer reaches them.
 */
export class ServiceStack extends Stack {
  /** The load balancer's DNS name, reachable from inside the VPC. */
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
              retention: RetentionDays.ONE_MONTH,
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
    // CloudWatch Logs for the server's log, which the CloudWatch agent ships.
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

    const serverLogs = new LogGroup(this, 'ServerLogs', {
      retention: RetentionDays.ONE_MONTH,
      removalPolicy: RemovalPolicy.DESTROY,
    });

    // The instances' own role: no managed policy, only the calls they make.
    const role = new Role(this, 'InstanceRole', {
      assumedBy: new ServicePrincipal('ec2.amazonaws.com'),
    });
    table.grant(role, 'dynamodb:GetItem', 'dynamodb:PutItem');
    serverLogs.grantWrite(role);
    const server = serverAsset(this, 'Server');
    role.addToPrincipalPolicy(
      new PolicyStatement({
        actions: ['s3:GetObject'],
        resources: [server.bucket.arnForObjects(server.s3ObjectKey)],
      }),
    );

    const userData = UserData.forLinux();
    // Stop at the first failing command, the download included: an instance
    // that can't start the server fails its health check and is replaced.
    userData.addCommands('set -euo pipefail');
    const serverFile = userData.addS3DownloadCommand({
      bucket: server.bucket,
      bucketKey: server.s3ObjectKey,
      region: this.region,
    });
    userData.addCommands(
      ...bootCommands(serverFile, table.tableName, serverLogs.logGroupName, this.region),
    );

    const instanceSecurityGroup = new SecurityGroup(this, 'InstanceSecurityGroup', {
      vpc,
      description: 'The service instances: only the load balancer reaches them.',
      allowAllOutbound: true,
    });
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
          deviceName: '/dev/xvda',
          volume: BlockDeviceVolume.ebs(8, {
            volumeType: EbsDeviceVolumeType.GP3,
            encrypted: true,
            deleteOnTermination: true,
          }),
        },
      ],
    });

    const group = new AutoScalingGroup(this, 'Instances', {
      vpc,
      vpcSubnets: { subnetType: SubnetType.PRIVATE_ISOLATED },
      launchTemplate,
      // Two instances in two zones, so one zone or instance can fail.
      minCapacity: 2,
      maxCapacity: 4,
      cooldown: Duration.minutes(5),
      healthChecks: HealthChecks.withAdditionalChecks({
        additionalTypes: [AdditionalHealthCheckType.ELB],
        gracePeriod: Duration.minutes(5),
      }),
      groupMetrics: [GroupMetrics.all()],
      // Replace one instance at a time. Without resource signals (cfn-signal
      // would need a CloudFormation endpoint), the update waits a fixed time
      // per instance and does not see whether the new one is healthy.
      updatePolicy: UpdatePolicy.rollingUpdate({
        maxBatchSize: 1,
        minInstancesInService: 1,
        pauseTime: Duration.minutes(5),
      }),
    });
    group.scaleOnCpuUtilization('Cpu', { targetUtilizationPercent: 60 });
    Validations.of(group).acknowledge({
      id: 'AwsSolutions-AS3',
      reason:
        'Scaling events are in the group activity history and its CloudWatch group metrics; a notification topic would need a subscriber this Container does not have.',
    });

    const accessLogs = new Bucket(this, 'AccessLogs', {
      encryption: BucketEncryption.S3_MANAGED,
      enforceSSL: true,
      blockPublicAccess: BlockPublicAccess.BLOCK_ALL,
      lifecycleRules: [{ expiration: Duration.days(90) }],
      // Access logs are evidence: deleting the stack keeps them.
      removalPolicy: RemovalPolicy.RETAIN,
    });
    Validations.of(accessLogs).acknowledge({
      id: 'AwsSolutions-S1',
      reason:
        'This bucket holds the load balancer access logs; logging access to it would need another bucket, whose own access would go unlogged.',
    });

    const loadBalancerSecurityGroup = new SecurityGroup(this, 'LoadBalancerSecurityGroup', {
      vpc,
      description: 'The load balancer: HTTP from inside the VPC.',
      allowAllOutbound: false,
    });
    loadBalancerSecurityGroup.addIngressRule(
      Peer.ipv4(VPC_CIDR),
      Port.tcp(80),
      'HTTP from inside the VPC',
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
    const listener = loadBalancer.addListener('Http', {
      port: 80,
      protocol: ApplicationProtocol.HTTP,
      open: false,
    });
    listener.addTargets('Service', {
      port: SERVER_PORT,
      protocol: ApplicationProtocol.HTTP,
      targets: [group],
      healthCheck: { path: '/health', healthyHttpCodes: '200', interval: Duration.seconds(15) },
      deregistrationDelay: Duration.seconds(30),
    });

    this.url = `http://${loadBalancer.loadBalancerDnsName}`;
  }
}

/**
 * The shell commands each instance runs at its first boot, after it has
 * downloaded the server to `serverFile`: install Node.js and the CloudWatch
 * agent, run the server as a systemd service under its own user, and ship
 * its log.
 */
export function bootCommands(
  serverFile: string,
  tableName: string,
  logGroupName: string,
  region: string,
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
    'dnf install -y nodejs22 amazon-cloudwatch-agent logrotate',
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
  ];
}
