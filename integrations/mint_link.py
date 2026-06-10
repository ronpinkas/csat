#!/usr/bin/env python3
"""Generate a CSAT survey link token.

The token encrypts "subject|subjectTime|lang" with AES-256-GCM, keyed by
SHA-256(crypto_secret), and base64url-encodes (no padding) nonce||ciphertext||tag.
This matches the CSAT server's validation exactly.

Requires: pip install cryptography

    from mint_link import mint_link
    url = mint_link("https://csat.example.com", CRYPTO_SECRET,
                    subject="+15551234567", subject_time=1717286400, lang="es")
"""
import base64
import hashlib
import secrets
import time
from cryptography.hazmat.primitives.ciphers.aead import AESGCM


def mint_token(crypto_secret: str, subject: str, subject_time: int, lang: str = "en", ref: str = "") -> str:
    """Return the opaque survey token (the value for the ?t= query param).

    ref binds the token to a tenant in the server's multi-tenant mode; leave it
    empty for a single-tenant deployment (the field is then omitted entirely, so
    existing links stay byte-for-byte identical).
    """
    if "|" in subject or "|" in lang or "|" in ref:
        raise ValueError("subject, lang, and ref must not contain '|'")
    lang = lang or "en"
    key = hashlib.sha256(crypto_secret.encode("utf-8")).digest()       # 32-byte AES-256 key
    nonce = secrets.token_bytes(12)                                    # fresh random nonce
    payload = f"{subject}|{int(subject_time)}|{lang}"
    if ref:
        payload += f"|{ref}"
    sealed = AESGCM(key).encrypt(nonce, payload.encode("utf-8"), None)  # ciphertext || 16-byte tag
    return base64.urlsafe_b64encode(nonce + sealed).rstrip(b"=").decode("ascii")


def mint_link(base_url: str, crypto_secret: str, subject: str, subject_time: int, lang: str = "en", ref: str = "") -> str:
    """Return the full survey URL to text to the customer."""
    token = mint_token(crypto_secret, subject, subject_time, lang, ref)
    return f"{base_url.rstrip('/')}/s?t={token}"


def provision_url(base_url: str, crypto_secret: str, ref: str, ttl_seconds: int = 86400) -> str:
    """Return the tenant-provisioning URL (multi-tenant / platform-hosted CSAT).

    POST this URL to the CSAT server; the JSON reply contains `invite_url`, the
    admin invite link to hand to the new tenant. Signed with the shared secret,
    so only the platform can call it.
    """
    expiry = int(time.time()) + ttl_seconds
    token = mint_token(crypto_secret, "__provision__", expiry, "en", ref)
    return f"{base_url.rstrip('/')}/provision?t={token}"


if __name__ == "__main__":
    import argparse
    import os
    import time

    p = argparse.ArgumentParser(description="Mint a CSAT survey link")
    p.add_argument("--secret", default=os.environ.get("CSAT_CRYPTO_SECRET", ""), help="crypto_secret (or set CSAT_CRYPTO_SECRET)")
    p.add_argument("--base", default="https://csat.example.com", help="base URL")
    p.add_argument("--subject", required=True, help="subject (phone, order id, ticket id…)")
    p.add_argument("--ts", type=int, default=int(time.time()), help="subject time, unix seconds")
    p.add_argument("--lang", default="en", help="en | es")
    p.add_argument("--ref", default="", help="tenant ref (multi-tenant mode; empty = single-tenant)")
    a = p.parse_args()
    if not a.secret:
        p.error("provide --secret or set CSAT_CRYPTO_SECRET")
    print(mint_link(a.base, a.secret, a.subject, a.ts, a.lang, a.ref))
