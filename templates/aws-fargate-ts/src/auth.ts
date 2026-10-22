import { CognitoJwtVerifier } from 'aws-jwt-verify';
import { FetchError } from 'aws-jwt-verify/error';

/**
 * Whether a request's `Authorization` header carries a token the service
 * accepts. A token that doesn't verify is false; it throws only when the
 * pool's keys can't be fetched, which is the service's failure, not the
 * caller's, so it is answered 500 and logged.
 */
export type Authenticate = (authorization: string | undefined) => Promise<boolean>;

/** What verifies one token: aws-jwt-verify's verifier, or a test's stand-in. */
export interface TokenVerifier {
  verify(token: string): Promise<unknown>;
}

/**
 * A verifier of the access tokens the user pool issues to the app client:
 * signature against the pool's published keys (fetched once and cached),
 * issuer, expiry, `token_use` `access` and `client_id`.
 */
export function cognitoVerifier(userPoolId: string, clientId: string): TokenVerifier {
  return CognitoJwtVerifier.create({ userPoolId, clientId, tokenUse: 'access' });
}

/** Accepts `Authorization: Bearer <token>` when `verifier` verifies the token. */
export function bearerAuthenticator(verifier: TokenVerifier): Authenticate {
  return async (authorization) => {
    const token = bearerToken(authorization);
    if (token === undefined) {
      return false;
    }
    try {
      await verifier.verify(token);
      return true;
    } catch (error: unknown) {
      if (error instanceof FetchError) {
        throw error;
      }
      return false;
    }
  };
}

/** The token of a `Bearer` header; undefined for none or another scheme. */
export function bearerToken(authorization: string | undefined): string | undefined {
  const match = /^Bearer +(\S+) *$/i.exec(authorization ?? '');
  return match?.[1];
}
