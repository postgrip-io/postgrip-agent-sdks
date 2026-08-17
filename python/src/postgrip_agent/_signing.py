"""Agent task-result signing.

Mirrors the canonical-request layout in agent-sdk-protocol/signing.go:

    agent-task-v1\n
    {METHOD}\n
    {PATH}\n
    {QUERY}\n
    {TIMESTAMP}\n
    {BODY_SHA256_BASE64}\n

The orchestrator verifies every signed POST against the agent's enrolled
Ed25519 public key. Strict by default — signing is mandatory in production.
"""

from __future__ import annotations

import base64
import hashlib

from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey, Ed25519PublicKey
from cryptography.hazmat.primitives.serialization import Encoding, PrivateFormat, NoEncryption, PublicFormat

HEADER_AGENT_SIGNATURE = "X-Agent-Signature"
HEADER_AGENT_SIGNATURE_KEY_ID = "X-Agent-Signature-Key-Id"
HEADER_AGENT_SIGNATURE_TIMESTAMP = "X-Agent-Signature-Timestamp"

AGENT_SIGNATURE_CONTEXT = "agent-task-v1"


def encode_public_key(pub: Ed25519PublicKey) -> str:
    raw = pub.public_bytes(Encoding.Raw, PublicFormat.Raw)
    return base64.b64encode(raw).decode("ascii")


def decode_private_key(encoded: str) -> Ed25519PrivateKey:
    raw = base64.b64decode(encoded)
    if len(raw) == 64:
        raw = raw[:32]
    if len(raw) != 32:
        raise ValueError(f"Ed25519 private key has {len(raw)} bytes, want 32 or 64")
    return Ed25519PrivateKey.from_private_bytes(raw)


def public_key_id(pub: Ed25519PublicKey) -> str:
    raw = pub.public_bytes(Encoding.Raw, PublicFormat.Raw)
    return hashlib.sha256(raw).hexdigest()[:16]


def canonical_request(method: str, path: str, query: str, timestamp_unix: int, body: bytes) -> bytes:
    body_sha = base64.b64encode(hashlib.sha256(body).digest()).decode("ascii")
    return "\n".join([
        AGENT_SIGNATURE_CONTEXT,
        method.upper(),
        path,
        query,
        str(timestamp_unix),
        body_sha,
        "",
    ]).encode()


def sign_request(priv: Ed25519PrivateKey, method: str, path: str, query: str, timestamp_unix: int, body: bytes) -> str:
    canonical = canonical_request(method, path, query, timestamp_unix, body)
    return base64.b64encode(priv.sign(canonical)).decode("ascii")
