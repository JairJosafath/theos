import { createSign, generateKeyPairSync } from 'node:crypto';
import { CognitoJwtVerifier } from 'aws-jwt-verify';
import { describe, expect, it } from 'vitest';

import { bearerAuthenticator, bearerToken } from './auth';

// A user pool of the tests' own: a key pair whose public half the verifier
// holds as the pool's JWKS, so nothing is fetched from Cognito.
const USER_POOL_ID = 'eu-west-1_TestPool';
const CLIENT_ID = 'test-client';
const ISSUER = `https://cognito-idp.eu-west-1.amazonaws.com/${USER_POOL_ID}`;
const { privateKey, publicKey } = generateKeyPairSync('rsa', { modulusLength: 2048 });

const verifier = CognitoJwtVerifier.create({
  userPoolId: USER_POOL_ID,
  clientId: CLIENT_ID,
  tokenUse: 'access',
});
verifier.cacheJwks({
  keys: [
    { ...publicKey.export({ format: 'jwk' }), kty: 'RSA', kid: 'key-1', alg: 'RS256', use: 'sig' },
  ],
});
const authenticate = bearerAuthenticator(verifier);

/** An RS256 token signed with the pool's key; `claims` override a valid access token's. */
function token(claims: Record<string, unknown> = {}): string {
  const now = Math.floor(Date.now() / 1000);
  const encode = (value: unknown): string =>
    Buffer.from(JSON.stringify(value)).toString('base64url');
  const unsigned = `${encode({ alg: 'RS256', kid: 'key-1', typ: 'JWT' })}.${encode({
    sub: 'user-1',
    iss: ISSUER,
    client_id: CLIENT_ID,
    token_use: 'access',
    iat: now,
    exp: now + 3600,
    ...claims,
  })}`;
  const signature = createSign('RSA-SHA256').update(unsigned).sign(privateKey, 'base64url');
  return `${unsigned}.${signature}`;
}

describe('bearerAuthenticator', () => {
  it('accepts an access token of the pool and client', async () => {
    expect(await authenticate(`Bearer ${token()}`)).toBe(true);
  });

  it.each([
    { name: 'no header', header: undefined },
    { name: 'an empty header', header: '' },
    { name: 'another scheme', header: `Basic ${token()}` },
    { name: 'a token of another client', header: `Bearer ${token({ client_id: 'other' })}` },
    { name: 'an expired token', header: `Bearer ${token({ exp: 1 })}` },
    { name: 'an ID token', header: `Bearer ${token({ token_use: 'id' })}` },
    {
      name: 'a token of another pool',
      header: `Bearer ${token({ iss: 'https://cognito-idp.eu-west-1.amazonaws.com/eu-west-1_Other' })}`,
    },
    { name: 'a tampered token', header: `Bearer ${token()}x` },
    { name: 'a token that is no JWT', header: 'Bearer nonsense' },
  ])('refuses $name', async ({ header }) => {
    expect(await authenticate(header)).toBe(false);
  });
});

describe('bearerToken', () => {
  it.each([
    { header: 'Bearer abc', want: 'abc' },
    { header: 'bearer abc', want: 'abc' },
    { header: 'Bearer', want: undefined },
    { header: 'Bearer a b', want: undefined },
    { header: undefined, want: undefined },
  ])('reads $header', ({ header, want }) => {
    expect(bearerToken(header)).toBe(want);
  });
});
