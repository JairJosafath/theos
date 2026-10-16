import { App } from 'aws-cdk-lib';
import { Match, Template } from 'aws-cdk-lib/assertions';
import { beforeAll, describe, expect, it } from 'vitest';

import { SiteStack, versionsKeptDays } from './site_stack';
import type { Operations, Tier } from './standard';

const OPERATIONS: Operations = {
  backups: { retentionDays: 35, copyToRegion: '' },
  recovery: { rpo: '1h', rto: '4h' },
  alerts: { email: '' },
};

/** The stack's template for `tier`, with `operations.backups` changed as given. */
function synth(tier: Tier, backups: Partial<Operations['backups']> = {}): Template {
  return Template.fromStack(
    new SiteStack(new App(), 'test', {
      env: { region: 'us-east-1' },
      tier,
      operations: { ...OPERATIONS, backups: { ...OPERATIONS.backups, ...backups } },
    }),
  );
}

/** The one resource of `type` in `template` whose logical id starts with `prefix`. */
function resource(template: Template, type: string, prefix: string): [string, unknown] {
  const found = Object.entries(template.findResources(type)).filter(([id]) =>
    id.startsWith(prefix),
  );
  expect(found.map(([id]) => id)).toHaveLength(1);
  const [only] = found;
  if (only === undefined) {
    throw new Error(`No ${type} ${prefix}.`);
  }
  return only;
}

let production: Template;
let development: Template;

beforeAll(() => {
  production = synth('production');
  development = synth('development');
});

describe('versionsKeptDays', () => {
  it.each([
    { retentionDays: 0, want: 35 },
    { retentionDays: 7, want: 35 },
    { retentionDays: 35, want: 35 },
    { retentionDays: 90, want: 90 },
  ])(
    'keeps earlier versions $want days when retentionDays is $retentionDays',
    ({ retentionDays, want }) => {
      expect(
        versionsKeptDays({ ...OPERATIONS, backups: { retentionDays, copyToRegion: '' } }),
      ).toBe(want);
    },
  );
});

describe('SiteStack', () => {
  it('keeps the site in a private, versioned bucket in production, earlier versions for a while', () => {
    const [, site] = resource(production, 'AWS::S3::Bucket', 'Site');
    expect(site).toMatchObject({
      DeletionPolicy: 'Retain',
      UpdateReplacePolicy: 'Retain',
      Properties: {
        VersioningConfiguration: { Status: 'Enabled' },
        PublicAccessBlockConfiguration: {
          BlockPublicAcls: true,
          BlockPublicPolicy: true,
          IgnorePublicAcls: true,
          RestrictPublicBuckets: true,
        },
        LoggingConfiguration: { LogFilePrefix: 's3/' },
        LifecycleConfiguration: {
          Rules: [expect.objectContaining({ NoncurrentVersionExpiration: { NoncurrentDays: 35 } })],
        },
      },
    });
    const [, longer] = resource(
      synth('production', { retentionDays: 90 }),
      'AWS::S3::Bucket',
      'Site',
    );
    expect(JSON.stringify(longer)).toContain('"NoncurrentDays":90');
  });

  it('throws the site’s bucket away with the stack in development, unversioned', () => {
    const [, site] = resource(development, 'AWS::S3::Bucket', 'Site');
    expect(site).toMatchObject({ DeletionPolicy: 'Delete' });
    expect(JSON.stringify(site)).not.toContain('Versioning');
    expect(JSON.stringify(site)).not.toContain('NoncurrentVersionExpiration');
  });

  it('keeps access logs for the tier’s log retention, in a bucket that stays', () => {
    for (const [template, days] of [
      [production, 365],
      [development, 30],
    ] as const) {
      const [, logs] = resource(template, 'AWS::S3::Bucket', 'Logs');
      expect(logs).toMatchObject({
        DeletionPolicy: 'Retain',
        Properties: {
          OwnershipControls: { Rules: [{ ObjectOwnership: 'ObjectWriter' }] },
          LifecycleConfiguration: { Rules: [{ ExpirationInDays: days, Status: 'Enabled' }] },
        },
      });
    }
  });

  it('lets only the distribution read the site, through origin access control', () => {
    production.resourceCountIs('AWS::CloudFront::OriginAccessControl', 1);
    production.hasResourceProperties('AWS::CloudFront::Distribution', {
      DistributionConfig: Match.objectLike({
        Origins: [
          Match.objectLike({
            OriginAccessControlId: { 'Fn::GetAtt': [Match.anyValue(), 'Id'] },
            S3OriginConfig: { OriginAccessIdentity: '' },
          }),
        ],
        DefaultRootObject: 'index.html',
        DefaultCacheBehavior: Match.objectLike({ ViewerProtocolPolicy: 'redirect-to-https' }),
        Logging: Match.objectLike({ Prefix: 'cloudfront/' }),
      }),
    });
    const [, policy] = resource(production, 'AWS::S3::BucketPolicy', 'SitePolicy');
    expect(JSON.stringify(policy)).toContain('"Service":"cloudfront.amazonaws.com"');
    expect(JSON.stringify(policy)).toContain('AWS:SourceArn');
  });

  it('answers on the deploy’s domain with its certificate and TLS 1.2 or later, else on CloudFront’s', () => {
    const ifDomain = (then: unknown, otherwise: unknown): unknown => ({
      'Fn::If': ['HasDomain', then, otherwise],
    });
    const noValue = { Ref: 'AWS::NoValue' };
    for (const template of [production, development]) {
      template.hasParameter('DomainName', { Type: 'String', Default: '' });
      template.hasParameter('CertificateArn', { Type: 'String', Default: '' });
      template.hasCondition('HasDomain', {
        'Fn::Not': [{ 'Fn::Equals': [{ Ref: 'DomainName' }, ''] }],
      });
      template.hasResourceProperties('AWS::CloudFront::Distribution', {
        DistributionConfig: Match.objectLike({
          Aliases: ifDomain([{ Ref: 'DomainName' }], noValue),
          ViewerCertificate: {
            AcmCertificateArn: ifDomain({ Ref: 'CertificateArn' }, noValue),
            CloudFrontDefaultCertificate: ifDomain(noValue, true),
            SslSupportMethod: ifDomain('sni-only', noValue),
            MinimumProtocolVersion: ifDomain('TLSv1.2_2021', noValue),
          },
        }),
      });
      expect(JSON.stringify(template.toJSON()['Rules'])).toContain('CertificateArn');
      template.hasOutput('EntryPoint', {
        Value: ifDomain(Match.anyValue(), Match.anyValue()),
      });
    }
  });

  it('puts a web application firewall in front of the site in production only', () => {
    const [firewallId] = resource(production, 'AWS::WAFv2::WebACL', 'Firewall');
    production.hasResourceProperties('AWS::WAFv2::WebACL', {
      Scope: 'CLOUDFRONT',
      DefaultAction: { Allow: {} },
      Rules: Match.arrayWith([
        Match.objectLike({
          Statement: {
            ManagedRuleGroupStatement: { VendorName: 'AWS', Name: 'AWSManagedRulesCommonRuleSet' },
          },
        }),
        Match.objectLike({
          Statement: { RateBasedStatement: { Limit: 2000, AggregateKeyType: 'IP' } },
          Action: { Block: {} },
        }),
      ]),
    });
    production.hasResourceProperties('AWS::CloudFront::Distribution', {
      DistributionConfig: Match.objectLike({ WebACLId: { 'Fn::GetAtt': [firewallId, 'Arn'] } }),
    });
    development.resourceCountIs('AWS::WAFv2::WebACL', 0);
  });

  it('copies the built site into the bucket on a deploy, and clears the cache', () => {
    production.hasResourceProperties('Custom::CDKBucketDeployment', {
      DestinationBucketName: { Ref: resource(production, 'AWS::S3::Bucket', 'Site')[0] },
      DistributionPaths: ['/*'],
      Prune: true,
    });
  });

  it('traces the copying function in production only, and gives it its own log group', () => {
    production.hasResourceProperties('AWS::Lambda::Function', {
      TracingConfig: { Mode: 'Active' },
      LoggingConfig: {
        LogGroup: { Ref: resource(production, 'AWS::Logs::LogGroup', 'DeployLogs')[0] },
      },
    });
    development.hasResourceProperties('AWS::Lambda::Function', {
      TracingConfig: Match.absent(),
    });
  });

  it('alarms on the site’s server errors and on the copying function', () => {
    const alarms = JSON.stringify(production.findResources('AWS::CloudWatch::Alarm'));
    for (const metric of ['5xxErrorRate', 'Errors', 'Throttles', 'Duration']) {
      expect(alarms).toContain(`"MetricName":"${metric}"`);
    }
  });
});
