import {
  CfnOutput,
  CfnParameter,
  CfnResource,
  Duration,
  RemovalPolicy,
  Stack,
  Validations,
} from 'aws-cdk-lib';
import type { StackProps } from 'aws-cdk-lib';
import { AttributeType, BillingMode, Table, TableEncryption } from 'aws-cdk-lib/aws-dynamodb';
import {
  FlowLogDestination,
  FlowLogTrafficType,
  GatewayVpcEndpointAwsService,
  SubnetType,
  Vpc,
} from 'aws-cdk-lib/aws-ec2';
import { Platform } from 'aws-cdk-lib/aws-ecr-assets';
import {
  Cluster,
  ContainerImage,
  ContainerInsights,
  CpuArchitecture,
  FargateService,
  FargateTaskDefinition,
  LogDrivers,
  OperatingSystemFamily,
} from 'aws-cdk-lib/aws-ecs';
import {
  ApplicationLoadBalancer,
  ApplicationProtocol,
  ListenerAction,
  ListenerCertificate,
  SslPolicy,
} from 'aws-cdk-lib/aws-elasticloadbalancingv2';
import { PolicyStatement, ServicePrincipal } from 'aws-cdk-lib/aws-iam';
import { LogGroup, RetentionDays } from 'aws-cdk-lib/aws-logs';
import { BlockPublicAccess, Bucket, BucketEncryption } from 'aws-cdk-lib/aws-s3';
import type { Construct } from 'constructs';
import path from 'node:path';

// The repository root: the Dockerfile and the service's code are there.
const ROOT = path.join(import.meta.dirname, '..');
// The port the service listens on in its container: src/main.ts's default.
const PORT = 8080;

/**
 * The Container's one stack: a service of Fargate tasks in private subnets,
 * behind an internet-facing Application Load Balancer that serves HTTPS
 * with the certificate the deployer names, keeping items in a DynamoDB table.
 */
export class ServiceStack extends Stack {
  constructor(scope: Construct, id: string, props?: StackProps) {
    super(scope, id, props);

    // The certificate is the deployer's: an ACM certificate for the domain
    // the service answers on. A parameter, so synth needs no account.
    const certificateArn = new CfnParameter(this, 'CertificateArn', {
      type: 'String',
      description: 'The ARN of the ACM certificate the load balancer serves HTTPS with.',
      allowedPattern: '^arn:aws[a-z-]*:acm:[a-z0-9-]+:[0-9]{12}:certificate/.+$',
    });

    const table = new Table(this, 'Items', {
      partitionKey: { name: 'id', type: AttributeType.STRING },
      billingMode: BillingMode.PAY_PER_REQUEST,
      encryption: TableEncryption.AWS_MANAGED,
      pointInTimeRecoverySpecification: { pointInTimeRecoveryEnabled: true },
      deletionProtection: true,
      // The data outlives the stack: deleting the stack keeps the table.
      removalPolicy: RemovalPolicy.RETAIN,
    });

    // Two zones, the load balancer in public subnets and the tasks in private
    // ones; one NAT gateway, for ECR and CloudWatch Logs, and a gateway
    // endpoint, free, so DynamoDB traffic never leaves the VPC.
    const vpc = new Vpc(this, 'Network', {
      maxAzs: 2,
      natGateways: 1,
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
      retention: RetentionDays.ONE_MONTH,
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
      retention: RetentionDays.ONE_MONTH,
      removalPolicy: RemovalPolicy.DESTROY,
    });
    const taskDefinition = new FargateTaskDefinition(this, 'Task', {
      cpu: 256,
      memoryLimitMiB: 512,
      runtimePlatform: {
        cpuArchitecture: CpuArchitecture.ARM64,
        operatingSystemFamily: OperatingSystemFamily.LINUX,
      },
    });
    taskDefinition.addContainer('App', {
      // Synth only stages the build context in cdk.out/; `cdk deploy` builds
      // and pushes the image, with Docker, on the deployer's machine.
      image: ContainerImage.fromAsset(ROOT, { platform: Platform.LINUX_ARM64 }),
      portMappings: [{ containerPort: PORT }],
      environment: { TABLE_NAME: table.tableName },
      logging: LogDrivers.awsLogs({ streamPrefix: 'app', logGroup: serviceLogs }),
      readonlyRootFilesystem: true,
      // ECS sends SIGTERM once the load balancer has drained the task, then
      // SIGKILL after this: time for the requests still in flight to finish.
      stopTimeout: Duration.seconds(30),
    });
    Validations.of(taskDefinition).acknowledge({
      id: 'AwsSolutions-ECS2',
      reason: 'The one variable, TABLE_NAME, is the name of the stack’s own table, not a secret.',
    });
    // Only the calls the service makes.
    table.grant(taskDefinition.taskRole, 'dynamodb:GetItem', 'dynamodb:PutItem');
    // ECS pulls the image with the execution role, which CDK grants pulls from
    // the asset's repository only, and ecr:GetAuthorizationToken, an action
    // that takes no resource but "*".
    const executionRole = taskDefinition.obtainExecutionRole();
    Validations.of(executionRole).acknowledge({
      id: 'AwsSolutions-IAM5[Resource::*]',
      reason:
        'ecr:GetAuthorizationToken accepts only the resource "*"; every other action names its resource.',
    });

    const service = new FargateService(this, 'Service', {
      cluster,
      taskDefinition,
      desiredCount: 2,
      // A deployment starts new tasks before stopping old ones, and rolls
      // back when the new ones never get healthy.
      minHealthyPercent: 100,
      maxHealthyPercent: 200,
      circuitBreaker: { enable: true, rollback: true },
      vpcSubnets: { subnetType: SubnetType.PRIVATE_WITH_EGRESS },
      assignPublicIp: false,
    });
    const scaling = service.autoScaleTaskCount({ minCapacity: 2, maxCapacity: 4 });
    scaling.scaleOnCpuUtilization('Cpu', { targetUtilizationPercent: 70 });

    const accessLogs = new Bucket(this, 'LoadBalancerLogs', {
      // Elastic Load Balancing writes access logs only with S3-managed keys.
      encryption: BucketEncryption.S3_MANAGED,
      blockPublicAccess: BlockPublicAccess.BLOCK_ALL,
      enforceSSL: true,
      lifecycleRules: [{ expiration: Duration.days(30) }],
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
    https.addTargets('Service', {
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

    new CfnOutput(this, 'LoadBalancerDns', {
      description: 'Point the certificate’s domain at this name (a CNAME or an alias record).',
      value: loadBalancer.loadBalancerDnsName,
    });
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
