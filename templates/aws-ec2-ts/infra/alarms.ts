import {
  CfnCondition,
  CfnOutput,
  CfnParameter,
  Duration,
  Fn,
  RemovalPolicy,
  Stack,
} from 'aws-cdk-lib';
import { ServicePrincipal } from 'aws-cdk-lib/aws-iam';
import { Key } from 'aws-cdk-lib/aws-kms';
import { CfnSubscription, Topic } from 'aws-cdk-lib/aws-sns';
import {
  DefaultDashboardFactory,
  MonitoringFacade,
  SnsAlarmActionStrategy,
} from 'cdk-monitoring-constructs';
import { Construct } from 'constructs';

/**
 * A stack's alarms: one encrypted SNS topic every alarm notifies, an email
 * subscription to it when the stack's `AlertEmail` parameter is set, and the
 * `MonitoringFacade` that builds the alarms and the stack's dashboard. The
 * stack outputs the topic's ARN as `AlarmTopicArn`, so a deploy target can
 * subscribe its own channel.
 */
export class Alarms extends Construct {
  /** The topic every alarm of the stack notifies. */
  readonly topic: Topic;
  /** The key the topic is encrypted with, which whatever else publishes to it must be let use. */
  readonly key: Key;
  /** Add each resource here (`monitorDynamoTable`, ...) to give it alarms and a dashboard row. */
  readonly monitoring: MonitoringFacade;

  /** `email` is the default of the `AlertEmail` parameter: `OPERATIONS.alerts.email`. */
  constructor(scope: Construct, id: string, email: string) {
    super(scope, id);
    const stack = Stack.of(this);

    // A topic encrypted with an AWS managed key can't take CloudWatch's
    // alarms; its own key lets CloudWatch encrypt what it publishes.
    const key = new Key(this, 'TopicKey', {
      description: `Encrypts the alarm topic of ${stack.stackName}.`,
      enableKeyRotation: true,
      // Alarms are not data: the key goes with the stack.
      removalPolicy: RemovalPolicy.DESTROY,
      pendingWindow: Duration.days(7),
    });
    this.key = key;
    const cloudWatch = new ServicePrincipal('cloudwatch.amazonaws.com');
    key.grant(cloudWatch, 'kms:Decrypt', 'kms:GenerateDataKey*');
    this.topic = new Topic(this, 'Topic', { masterKey: key, enforceSSL: true });
    this.topic.applyRemovalPolicy(RemovalPolicy.DESTROY);
    this.topic.grantPublish(cloudWatch);

    // Who the alarms tell is the environment's: a deploy target sets the
    // parameter, and an empty one subscribes nobody.
    const alertEmail = new CfnParameter(stack, 'AlertEmail', {
      type: 'String',
      default: email,
      allowedPattern: '^([^@\\s]+@[^@\\s]+)?$',
      description: 'The email address the alarms notify; empty for none.',
    });
    const hasAlertEmail = new CfnCondition(stack, 'HasAlertEmail', {
      expression: Fn.conditionNot(Fn.conditionEquals(alertEmail.valueAsString, '')),
    });
    const subscription = new CfnSubscription(this, 'EmailSubscription', {
      topicArn: this.topic.topicArn,
      protocol: 'email',
      endpoint: alertEmail.valueAsString,
    });
    subscription.cfnOptions.condition = hasAlertEmail;

    this.monitoring = new MonitoringFacade(this, 'Monitoring', {
      alarmFactoryDefaults: {
        actionsEnabled: true,
        alarmNamePrefix: stack.stackName,
        // Five-minute periods, and one bad one is enough.
        evaluationPeriods: 1,
        datapointsToAlarm: 1,
        action: new SnsAlarmActionStrategy({ onAlarmTopic: this.topic }),
      },
      metricFactoryDefaults: {},
      dashboardFactory: new DefaultDashboardFactory(this, 'Dashboard', {
        dashboardNamePrefix: stack.stackName,
        createSummaryDashboard: false,
        createAlarmDashboard: false,
      }),
    });

    new CfnOutput(stack, 'AlarmTopicArn', {
      value: this.topic.topicArn,
      description: 'The SNS topic every alarm of this stack notifies.',
    });
  }
}
