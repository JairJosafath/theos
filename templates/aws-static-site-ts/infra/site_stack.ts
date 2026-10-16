import {
  AssetHashType,
  CfnCondition,
  CfnOutput,
  CfnParameter,
  CfnRule,
  DockerImage,
  Duration,
  Fn,
  RemovalPolicy,
  Stack,
  Validations,
} from 'aws-cdk-lib';
import type { StackProps } from 'aws-cdk-lib';
import {
  CfnDistribution,
  Distribution,
  HttpVersion,
  PriceClass,
  ResponseHeadersPolicy,
  ViewerProtocolPolicy,
} from 'aws-cdk-lib/aws-cloudfront';
import { S3BucketOrigin } from 'aws-cdk-lib/aws-cloudfront-origins';
import { PolicyStatement, Role, ServicePrincipal } from 'aws-cdk-lib/aws-iam';
import { CfnFunction, Function as LambdaFunction } from 'aws-cdk-lib/aws-lambda';
import { LogGroup } from 'aws-cdk-lib/aws-logs';
import { BlockPublicAccess, Bucket, BucketEncryption, ObjectOwnership } from 'aws-cdk-lib/aws-s3';
import { BucketDeployment, Source } from 'aws-cdk-lib/aws-s3-deployment';
import { CfnWebACL } from 'aws-cdk-lib/aws-wafv2';
import type { Construct } from 'constructs';
import { execFileSync } from 'node:child_process';
import path from 'node:path';

import { Alarms } from './alarms';
import { TIER_RULES } from './standard';
import type { Operations, Tier } from './standard';

// The repository root: index.html, src/ and Vite's config are there.
const ROOT = path.join(import.meta.dirname, '..');

/**
 * How long the bucket keeps a file's earlier versions where the tier keeps
 * data: point-in-time recovery's 35 days, as a table has, or the backups'
 * retention when that is longer.
 */
export function versionsKeptDays(operations: Operations): number {
  return Math.max(35, operations.backups.retentionDays);
}

/** What the stack is built for. */
export interface SiteStackProps extends StackProps {
  /** Where it runs, which decides what it keeps, guards and traces. */
  readonly tier: Tier;
  /** What the Container promises whatever the tier: `OPERATIONS`. */
  readonly operations: Operations;
}

/**
 * The Container's one stack: the site, built by Vite at synth, in a private
 * S3 bucket that only a CloudFront distribution reads, through origin access
 * control. The distribution logs every request, answers HTTPS only, and, on
 * a domain the deploy names, with TLS 1.2 or later; in production a web
 * application firewall screens what reaches it, and the bucket keeps every
 * earlier version of the site's files. Alarms go to a topic the stack
 * outputs with the site's URL.
 */
export class SiteStack extends Stack {
  constructor(scope: Construct, id: string, props: SiteStackProps) {
    super(scope, id, props);
    const rules = TIER_RULES[props.tier];
    const { alerts } = props.operations;

    // The site's domain and its certificate are the environment's: a deploy
    // target sets both, and with neither the site answers on CloudFront's
    // own domain.
    const domainName = new CfnParameter(this, 'DomainName', {
      type: 'String',
      default: '',
      allowedPattern:
        '^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+)?$',
      description:
        'The domain the site answers on, such as www.example.com; empty for CloudFront’s own.',
    });
    const certificateArn = new CfnParameter(this, 'CertificateArn', {
      type: 'String',
      default: '',
      allowedPattern: '^(arn:aws[a-z-]*:acm:us-east-1:[0-9]{12}:certificate/[0-9a-f-]+)?$',
      description:
        'The ARN of an ACM certificate in us-east-1 for DomainName; empty when DomainName is.',
    });
    const hasDomain = new CfnCondition(this, 'HasDomain', {
      expression: Fn.conditionNot(Fn.conditionEquals(domainName.valueAsString, '')),
    });
    new CfnRule(this, 'DomainNeedsCertificate', {
      ruleCondition: Fn.conditionNot(Fn.conditionEquals(domainName.valueAsString, '')),
      assertions: [
        {
          assert: Fn.conditionNot(Fn.conditionEquals(certificateArn.valueAsString, '')),
          assertDescription: 'A DomainName needs the CertificateArn of its certificate.',
        },
      ],
    });

    // Where CloudFront and S3 write their access logs. Logs are not data:
    // they expire after the tier's log retention, and the bucket stays when
    // the stack goes, emptying itself. CloudFront writes its logs with ACLs,
    // which the bucket's owner must allow.
    const logs = new Bucket(this, 'Logs', {
      encryption: BucketEncryption.S3_MANAGED,
      blockPublicAccess: BlockPublicAccess.BLOCK_ALL,
      enforceSSL: true,
      objectOwnership: ObjectOwnership.OBJECT_WRITER,
      lifecycleRules: [{ expiration: Duration.days(rules.logRetention) }],
      removalPolicy: RemovalPolicy.RETAIN,
    });
    Validations.of(logs).acknowledge({
      id: 'AwsSolutions-S1',
      reason:
        'The bucket holds only the access logs of the distribution and the site’s bucket; logging access to it would need another bucket whose own access went unlogged.',
    });

    // The site's files: data where the tier keeps data, so kept with the
    // stack, versioned, and every earlier version kept for a while; thrown
    // away with the stack in development.
    const site = new Bucket(this, 'Site', {
      encryption: BucketEncryption.S3_MANAGED,
      blockPublicAccess: BlockPublicAccess.BLOCK_ALL,
      enforceSSL: true,
      versioned: rules.keepData,
      serverAccessLogsBucket: logs,
      serverAccessLogsPrefix: 's3/',
      lifecycleRules: [
        {
          abortIncompleteMultipartUploadAfter: Duration.days(7),
          ...(rules.keepData && {
            noncurrentVersionExpiration: Duration.days(versionsKeptDays(props.operations)),
          }),
        },
      ],
      removalPolicy: rules.keepData ? RemovalPolicy.RETAIN : RemovalPolicy.DESTROY,
    });

    // In production a web application firewall in front of the site: AWS's
    // managed rules for common attacks, known bad inputs and addresses with
    // a bad reputation, and a limit per address. CloudFront takes only a
    // firewall made in us-east-1, which is why the stack is there.
    const firewall = rules.keepData
      ? new CfnWebACL(this, 'Firewall', {
          scope: 'CLOUDFRONT',
          defaultAction: { allow: {} },
          visibilityConfig: {
            cloudWatchMetricsEnabled: true,
            metricName: `${id}-firewall`,
            sampledRequestsEnabled: true,
          },
          rules: [
            managedRule('AWSManagedRulesAmazonIpReputationList', 0),
            managedRule('AWSManagedRulesCommonRuleSet', 1),
            managedRule('AWSManagedRulesKnownBadInputsRuleSet', 2),
            {
              name: 'RequestsPerAddress',
              priority: 3,
              // 2,000 requests in five minutes from one address is far past
              // what a person browsing sends.
              statement: { rateBasedStatement: { limit: 2000, aggregateKeyType: 'IP' } },
              action: { block: {} },
              visibilityConfig: {
                cloudWatchMetricsEnabled: true,
                metricName: 'RequestsPerAddress',
                sampledRequestsEnabled: true,
              },
            },
          ],
        })
      : undefined;

    const distribution = new Distribution(this, 'Distribution', {
      defaultBehavior: {
        origin: S3BucketOrigin.withOriginAccessControl(site),
        viewerProtocolPolicy: ViewerProtocolPolicy.REDIRECT_TO_HTTPS,
        // HSTS, no sniffing, no framing, a strict referrer policy.
        responseHeadersPolicy: ResponseHeadersPolicy.SECURITY_HEADERS,
      },
      defaultRootObject: 'index.html',
      httpVersion: HttpVersion.HTTP2_AND_3,
      // North America and Europe's edge locations: the cheapest class.
      priceClass: PriceClass.PRICE_CLASS_100,
      enableLogging: true,
      logBucket: logs,
      logFilePrefix: 'cloudfront/',
      ...(firewall && { webAclId: firewall.attrArn }),
    });
    if (firewall === undefined) {
      Validations.of(distribution).acknowledge({
        id: 'AwsSolutions-CFR2',
        reason:
          'The development tier has no web application firewall: it costs a monthly fee per stack, and a development site serves no one who needs it. Production has one.',
      });
    }
    Validations.of(distribution).acknowledge({
      id: 'AwsSolutions-CFR1',
      reason:
        'The site is for anyone, anywhere: no country is refused. A geographic restriction is the owner’s choice when the law or the audience asks for one.',
    });
    // The domain and its certificate, when the deploy names them: TLS 1.2
    // or later, by SNI. Without them CloudFront answers on its own domain,
    // whose certificate it sets the TLS versions of itself.
    const cfn = distribution.node.defaultChild as CfnDistribution;
    cfn.addPropertyOverride(
      'DistributionConfig.Aliases',
      Fn.conditionIf(hasDomain.logicalId, [domainName.valueAsString], Fn.ref('AWS::NoValue')),
    );
    const ifDomain = (then: unknown, otherwise: unknown): unknown =>
      Fn.conditionIf(hasDomain.logicalId, then, otherwise);
    cfn.addPropertyOverride('DistributionConfig.ViewerCertificate', {
      AcmCertificateArn: ifDomain(certificateArn.valueAsString, Fn.ref('AWS::NoValue')),
      CloudFrontDefaultCertificate: ifDomain(Fn.ref('AWS::NoValue'), true),
      SslSupportMethod: ifDomain('sni-only', Fn.ref('AWS::NoValue')),
      MinimumProtocolVersion: ifDomain('TLSv1.2_2021', Fn.ref('AWS::NoValue')),
    });
    // cfn-lint checks the certificate's ARN against the parameter's default,
    // empty, even where HasDomain keeps it out; the rule above makes a
    // DomainName bring one.
    cfn.addMetadata('cfn-lint', { config: { ignore_checks: ['W1030'] } });
    Validations.of(distribution).acknowledge({
      id: 'AwsSolutions-CFR4',
      reason:
        'With a DomainName the distribution takes TLS 1.2 or later (TLSv1.2_2021, set by the override CDK Nag doesn’t read); without one it answers on CloudFront’s domain, whose certificate CloudFront keeps at TLSv1 whatever the setting.',
    });

    // The site, built by Vite at synth into the asset BucketDeployment
    // copies into the bucket on deploy, then the cache cleared. The
    // installed Vite builds it; never Docker, which CI doesn't have.
    const deployLogs = new LogGroup(this, 'DeployLogs', {
      retention: rules.logRetention,
      removalPolicy: RemovalPolicy.DESTROY,
    });
    // The copying function's own role, allowed only to write its log group
    // (and its traces): the default role would carry the AWS managed
    // AWSLambdaBasicExecutionRole, which may write to every log group.
    const deployRole = new Role(this, 'DeployRole', {
      assumedBy: new ServicePrincipal('lambda.amazonaws.com'),
    });
    deployLogs.grantWrite(deployRole);
    new BucketDeployment(this, 'Deploy', {
      sources: [
        Source.asset(ROOT, {
          assetHashType: AssetHashType.OUTPUT,
          bundling: {
            image: DockerImage.fromRegistry('node:24'),
            local: {
              tryBundle(outputDir: string): boolean {
                execFileSync(
                  path.join(ROOT, 'node_modules', '.bin', 'vite'),
                  ['build', '--logLevel', 'warn', '--emptyOutDir', '--outDir', outputDir],
                  { cwd: ROOT, stdio: 'inherit' },
                );
                return true;
              },
            },
          },
        }),
      ],
      destinationBucket: site,
      distribution,
      distributionPaths: ['/*'],
      role: deployRole,
      logGroup: deployLogs,
      memoryLimit: 512,
    });
    const copier = Stack.of(this)
      .node.findAll()
      .filter((node) => node instanceof CfnFunction)
      .find((fn) => fn.node.path.includes('CDKBucketDeployment'));
    if (copier === undefined) {
      throw new Error('BucketDeployment made no function.');
    }
    if (rules.tracing) {
      copier.tracingConfig = { mode: 'Active' };
      deployRole.addToPrincipalPolicy(
        new PolicyStatement({
          actions: ['xray:PutTraceSegments', 'xray:PutTelemetryRecords'],
          resources: ['*'],
        }),
      );
    }
    // BucketDeployment grants its function what copying needs: reading
    // every object of the asset, writing and deleting every object of the
    // site's bucket, and invalidating the cache, which it grants on every
    // distribution. The function and the role are the stack's alone.
    for (const [finding, why] of [
      ['Action::s3:Abort*', 'aborting the uploads it started'],
      ['Action::s3:DeleteObject*', 'deleting the files a deploy drops'],
      ['Action::s3:GetBucket*', 'reading the buckets it copies between'],
      ['Action::s3:GetObject*', 'reading the files it copies'],
      ['Action::s3:List*', 'listing the files it copies'],
      [
        'Resource::<SiteE53D7754.Arn>/*',
        'every file of the site’s bucket, which holds nothing else',
      ],
      [
        'Resource::arn:aws:s3:::cdk-hnb659fds-assets-<AWS::AccountId>-us-east-1/*',
        'the asset CDK uploaded, among the bootstrap bucket’s',
      ],
      [
        'Resource::*',
        'CloudFront’s invalidation, which BucketDeployment grants on every distribution, and X-Ray’s writes',
      ],
    ] as const) {
      Validations.of(deployRole).acknowledge({
        id: `AwsSolutions-IAM5[${finding}]`,
        reason: `BucketDeployment’s function copies the built site into the bucket on a deploy: ${why}.`,
      });
    }
    Validations.of(copier).acknowledge({
      id: 'AwsSolutions-L1',
      reason:
        'BucketDeployment picks its function’s Python runtime with the AWS CLI layer it ships; the next aws-cdk-lib moves it on.',
    });

    // Each alarm fires on the first bad five minutes: the site answering
    // server errors, or the copy of its files failing on a deploy.
    // docs/runbook.md says what each means and what to do first.
    const alarms = new Alarms(this, 'Alarms', alerts.email);
    alarms.monitoring
      .monitorCloudFrontDistribution({
        distribution,
        alarmFriendlyName: 'Site',
        addFault5xxRate: { Critical: { maxErrorRate: 1 } },
      })
      .monitorLambdaFunction({
        lambdaFunction: LambdaFunction.fromFunctionName(this, 'MonitoredDeploy', copier.ref),
        alarmFriendlyName: 'Deploy',
        addFaultCountAlarm: { Critical: { maxErrorCount: 0 } },
        addThrottlesCountAlarm: { Critical: { maxErrorCount: 0 } },
        addLatencyP99Alarm: { Warning: { maxLatency: Duration.minutes(12) } },
      });
    if (firewall !== undefined) {
      alarms.monitoring.monitorWebApplicationFirewallAclV2({
        acl: firewall,
        alarmFriendlyName: 'Firewall',
      });
    }

    new CfnOutput(this, 'EntryPoint', {
      value: Fn.conditionIf(
        hasDomain.logicalId,
        `https://${domainName.valueAsString}/`,
        `https://${distribution.distributionDomainName}/`,
      ).toString(),
      description: 'The URL of the site.',
    });
  }
}

/** A rule of the web ACL that runs AWS's managed rule group `name`, blocking what it blocks. */
function managedRule(name: string, priority: number): CfnWebACL.RuleProperty {
  return {
    name,
    priority,
    statement: { managedRuleGroupStatement: { vendorName: 'AWS', name } },
    overrideAction: { none: {} },
    visibilityConfig: {
      cloudWatchMetricsEnabled: true,
      metricName: name,
      sampledRequestsEnabled: true,
    },
  };
}
