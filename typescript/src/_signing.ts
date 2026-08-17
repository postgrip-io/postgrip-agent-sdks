// Agent task-result signing.
//
// Mirrors the canonical-request layout in agent-sdk-protocol/signing.go:
//
//   agent-task-v1\n
//   {METHOD}\n
//   {PATH}\n
//   {QUERY}\n
//   {TIMESTAMP}\n
//   {BODY_SHA256_BASE64}\n
//
// The orchestrator verifies every signed POST against the agent's enrolled
// Ed25519 public key. Strict by default — signing is mandatory in production.

import { createHash, createPrivateKey, createPublicKey, generateKeyPairSync, sign, type KeyObject } from 'node:crypto';

export const HEADER_AGENT_SIGNATURE = 'X-Agent-Signature';
export const HEADER_AGENT_SIGNATURE_KEY_ID = 'X-Agent-Signature-Key-Id';
export const HEADER_AGENT_SIGNATURE_TIMESTAMP = 'X-Agent-Signature-Timestamp';

const AGENT_SIGNATURE_CONTEXT = 'agent-task-v1';

export interface AgentSigningKey {
  privateKey: KeyObject;
  publicKey: KeyObject;
  publicKeyBase64: string;
  keyId: string;
}

export function generateSigningKey(): AgentSigningKey {
  const { privateKey, publicKey } = generateKeyPairSync('ed25519');
  // jwk format gives us the raw 32-byte public key in `x` (base64url-encoded).
  const jwk = publicKey.export({ format: 'jwk' });
  if (typeof jwk.x !== 'string') {
    throw new Error('Ed25519 public key export missing x');
  }
  const rawPub = Buffer.from(jwk.x, 'base64url');
  const publicKeyBase64 = rawPub.toString('base64');
  const keyId = createHash('sha256').update(rawPub).digest('hex').slice(0, 16);
  return { privateKey, publicKey, publicKeyBase64, keyId };
}

export function importSigningKeyFromBase64(encoded: string): AgentSigningKey {
  const raw = Buffer.from(encoded, 'base64');
  if (raw.length !== 32 && raw.length !== 64) {
    throw new Error(`Ed25519 private key has ${raw.length} bytes, want 32 or 64`);
  }
  const seed = raw.subarray(0, 32);
  const privateKey = createPrivateKey({
    key: Buffer.concat([
      Buffer.from('302e020100300506032b657004220420', 'hex'),
      seed,
    ]),
    format: 'der',
    type: 'pkcs8',
  });
  const publicKey = createPublicKey(privateKey);
  const jwk = publicKey.export({ format: 'jwk' });
  if (typeof jwk.x !== 'string') {
    throw new Error('Ed25519 public key export missing x');
  }
  const rawPub = Buffer.from(jwk.x, 'base64url');
  const publicKeyBase64 = rawPub.toString('base64');
  const keyId = createHash('sha256').update(rawPub).digest('hex').slice(0, 16);
  return { privateKey, publicKey, publicKeyBase64, keyId };
}

export function canonicalRequest(method: string, path: string, query: string, timestampUnix: number, body: Buffer): Buffer {
  const bodyDigest = createHash('sha256').update(body).digest('base64');
  return Buffer.from([
    AGENT_SIGNATURE_CONTEXT,
    method.toUpperCase(),
    path,
    query,
    String(timestampUnix),
    bodyDigest,
    '',
  ].join('\n'));
}

export function signRequest(privateKey: KeyObject, method: string, path: string, query: string, timestampUnix: number, body: Buffer): string {
  const canonical = canonicalRequest(method, path, query, timestampUnix, body);
  return sign(null, canonical, privateKey).toString('base64');
}
