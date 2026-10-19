import { Aws, Duration, RemovalPolicy, Stack, Validations } from 'aws-cdk-lib';
import type { StackProps } from 'aws-cdk-lib';
import { BackupPlan, BackupPlanRule, BackupResource, BackupVault } from 'aws-cdk-lib/aws-backup';
import type { Table } from 'aws-cdk-lib/aws-dynamodb';
import { Schedule } from 'aws-cdk-lib/aws-events';
import { Key } from 'aws-cdk-lib/aws-kms';
import { Construct } from 'constructs';
import { createHash } from 'node:crypto';

/** The name of the vault in `copyToRegion` that `stackName`'s backups are copied to. */
export function copyVaultName(stackName: string): string {
  // A vault name is at most 50 letters, digits, - and _: a longer stack name
  // keeps its start and a hash of the whole, so two never share a vault.
  const hash = createHash('sha256').update(stackName).digest('hex').slice(0, 8);
  const name = stackName.length <= 43 ? stackName : `${stackName.slice(0, 34)}-${hash}`;
  return `${name}-copies`;
}

/** What `Backups` backs up, and for how long. */
export interface BackupsProps {
  /** Days each daily backup is kept, more than 0: `OPERATIONS.backups.retentionDays`. */
  readonly retentionDays: number;
  /** The region the backups are copied to, whose `BackupCopyStack` holds the vault; empty for none. */
  readonly copyToRegion: string;
  /**
   * The stack's own tables: the only resources backed up. Named by ARN, not
   * by the Container's tag, which a `<name>-development` stack in the same
   * account carries too (#1267).
   */
  readonly tables: readonly Table[];
}

/**
 * Daily AWS Backup backups of the stack's own tables, named by ARN, in a vault with its own key, kept `retentionDays` and copied to `copyToRegion`
 * when it is set. Backups are data: the vault and its key outlive the stack.
 * No vault lock, which can never be undone: that is the account owner's call.
 */
export class Backups extends Construct {
  readonly vault: BackupVault;
  readonly plan: BackupPlan;

  constructor(scope: Construct, id: string, props: BackupsProps) {
    super(scope, id);
    const stack = Stack.of(this);
    const key = new Key(this, 'VaultKey', {
      description: `Encrypts the backups of ${stack.stackName}.`,
      enableKeyRotation: true,
      removalPolicy: RemovalPolicy.RETAIN,
    });
    this.vault = new BackupVault(this, 'Vault', {
      encryptionKey: key,
      removalPolicy: RemovalPolicy.RETAIN,
    });
    const deleteAfter = Duration.days(props.retentionDays);
    const copyActions =
      props.copyToRegion === ''
        ? []
        : [
            {
              destinationBackupVault: BackupVault.fromBackupVaultArn(
                this,
                'CopyVault',
                `arn:${Aws.PARTITION}:backup:${props.copyToRegion}:${Aws.ACCOUNT_ID}:backup-vault:${copyVaultName(stack.stackName)}`,
              ),
              deleteAfter,
            },
          ];
    this.plan = new BackupPlan(this, 'Plan', {
      backupPlanName: stack.stackName,
      backupVault: this.vault,
    });
    this.plan.addRule(
      new BackupPlanRule({
        ruleName: 'Daily',
        // 05:00 UTC, AWS Backup's default window.
        scheduleExpression: Schedule.cron({ minute: '0', hour: '5' }),
        deleteAfter,
        copyActions,
      }),
    );
    const selection = this.plan.addSelection('Tables', {
      resources: props.tables.map((table) => BackupResource.fromArn(table.tableArn)),
    });
    Validations.of(selection).acknowledge({
      id: 'AwsSolutions-IAM4[Policy::arn:<AWS::Partition>:iam::aws:policy/service-role/AWSBackupServiceRolePolicyForBackup]',
      reason:
        "AWS Backup's role carries AWSBackupServiceRolePolicyForBackup, the managed policy AWS keeps current with what Backup needs for each resource type it backs up.",
    });
  }
}

/**
 * The vault in another region that a stack's backups are copied to, so a
 * lost region loses no data. `defineStacks` adds it in production when
 * `OPERATIONS.backups.copyToRegion` is set, in that region.
 */
export class BackupCopyStack extends Stack {
  /** `sourceStackName` names the stack whose backups come here. */
  constructor(scope: Construct, id: string, sourceStackName: string, props: StackProps) {
    super(scope, id, props);
    const key = new Key(this, 'VaultKey', {
      description: `Encrypts the copies of the backups of ${sourceStackName}.`,
      enableKeyRotation: true,
      removalPolicy: RemovalPolicy.RETAIN,
    });
    new BackupVault(this, 'Vault', {
      backupVaultName: copyVaultName(sourceStackName),
      encryptionKey: key,
      removalPolicy: RemovalPolicy.RETAIN,
    });
  }
}
