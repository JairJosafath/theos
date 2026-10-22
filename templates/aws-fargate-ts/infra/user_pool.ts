import { CfnOutput, Duration, RemovalPolicy, Validations } from 'aws-cdk-lib';
import { AccountRecovery, Mfa, UserPool } from 'aws-cdk-lib/aws-cognito';
import type { UserPoolClient } from 'aws-cdk-lib/aws-cognito';
import { Construct } from 'constructs';

/** What the user pool is built for. */
export interface UsersProps {
  /** Whether the tier keeps data: then the pool, and its users, outlive the stack. */
  readonly keepData: boolean;
}

/**
 * The Cognito user pool whose access tokens the service accepts on every
 * route but `/health`, and the one app client callers sign in through. The
 * deployer creates the users (no self sign-up); they sign in with their
 * email and a password, and send the access token they get as
 * `Authorization: Bearer <token>`. The stack outputs the pool's id, the
 * client's id and the issuer.
 */
export class Users extends Construct {
  readonly userPool: UserPool;
  readonly client: UserPoolClient;

  constructor(scope: Construct, id: string, props: UsersProps) {
    super(scope, id);

    this.userPool = new UserPool(this, 'Pool', {
      selfSignUpEnabled: false,
      signInAliases: { email: true },
      accountRecovery: AccountRecovery.EMAIL_ONLY,
      passwordPolicy: {
        minLength: 12,
        requireLowercase: true,
        requireUppercase: true,
        requireDigits: true,
        requireSymbols: true,
        tempPasswordValidity: Duration.days(3),
      },
      // A user may add an authenticator app; text messages cost money and
      // can be intercepted.
      mfa: Mfa.OPTIONAL,
      mfaSecondFactor: { otp: true, sms: false },
      deletionProtection: props.keepData,
      removalPolicy: props.keepData ? RemovalPolicy.RETAIN : RemovalPolicy.DESTROY,
    });
    Validations.of(this.userPool).acknowledge(
      {
        id: 'AwsSolutions-COG2',
        reason:
          'The users are the callers the deployer creates; each may add an authenticator app (optional MFA). Set mfa to Mfa.REQUIRED when people, not programs, sign in.',
      },
      {
        id: 'AwsSolutions-COG8',
        reason:
          'The Plus tier’s threat protection is billed per monthly active user; the pool has no self sign-up, and its users are the ones the deployer creates.',
      },
    );

    // No secret: callers sign in with their own password and hold only the
    // tokens. No OAuth: there is no hosted sign-in page.
    this.client = this.userPool.addClient('Client', {
      authFlows: { userPassword: true, userSrp: true },
      generateSecret: false,
      disableOAuth: true,
      preventUserExistenceErrors: true,
      accessTokenValidity: Duration.hours(1),
    });

    new CfnOutput(this, 'UserPoolId', {
      description: 'The Cognito user pool whose users may call the service.',
      value: this.userPool.userPoolId,
    });
    new CfnOutput(this, 'UserPoolClientId', {
      description: 'The app client to sign in through (initiate-auth --client-id).',
      value: this.client.userPoolClientId,
    });
    new CfnOutput(this, 'Issuer', {
      description: 'The issuer of the access tokens the service accepts.',
      value: this.userPool.userPoolProviderUrl,
    });
  }

  /** The environment the service reads the pool from (`src/main.ts`). */
  get environment(): Record<string, string> {
    return {
      USER_POOL_ID: this.userPool.userPoolId,
      USER_POOL_CLIENT_ID: this.client.userPoolClientId,
    };
  }
}
