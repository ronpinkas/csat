// Generate a CSAT survey link token (Node.js, no dependencies).
//
// The token encrypts "subject|subjectTime|lang" with AES-256-GCM, keyed by
// SHA-256(crypto_secret), and base64url-encodes (no padding) nonce||ciphertext||tag.
// This matches the CSAT server's validation exactly.
//
//   const { mintLink } = require("./mint_link");
//   const url = mintLink("https://csat.example.com", CRYPTO_SECRET,
//                        "+15551234567", 1717286400, "es");

"use strict";
const crypto = require("crypto");

/** Return the opaque survey token (the value for the ?t= query param). */
function mintToken(cryptoSecret, subject, subjectTime, lang = "en") {
  if (subject.includes("|") || (lang && lang.includes("|"))) {
    throw new Error("subject and lang must not contain '|'");
  }
  lang = lang || "en";
  const key = crypto.createHash("sha256").update(cryptoSecret, "utf8").digest(); // 32-byte key
  const nonce = crypto.randomBytes(12);                                          // fresh nonce
  const cipher = crypto.createCipheriv("aes-256-gcm", key, nonce);
  const plaintext = Buffer.from(`${subject}|${Math.trunc(subjectTime)}|${lang}`, "utf8");
  const ciphertext = Buffer.concat([cipher.update(plaintext), cipher.final()]);
  const tag = cipher.getAuthTag();                                               // 16-byte tag
  return Buffer.concat([nonce, ciphertext, tag]).toString("base64url");          // no padding
}

/** Return the full survey URL to text to the customer. */
function mintLink(baseUrl, cryptoSecret, subject, subjectTime, lang = "en") {
  const token = mintToken(cryptoSecret, subject, subjectTime, lang);
  return `${baseUrl.replace(/\/+$/, "")}/s?t=${token}`;
}

module.exports = { mintToken, mintLink };

// CLI: node mint_link.js --subject +15551234567 [--ts 1717286400] [--lang es] [--base URL]
//      (secret from --secret or CSAT_CRYPTO_SECRET)
if (require.main === module) {
  const args = Object.fromEntries(
    process.argv.slice(2).join("=").split("--").filter(Boolean)
      .map((s) => s.trim().split("=").slice(0, 2))
  );
  const secret = args.secret || process.env.CSAT_CRYPTO_SECRET || "";
  if (!secret) { console.error("provide --secret or set CSAT_CRYPTO_SECRET"); process.exit(1); }
  if (!args.subject) { console.error("--subject is required"); process.exit(1); }
  const ts = args.ts ? parseInt(args.ts, 10) : Math.floor(Date.now() / 1000);
  console.log(mintLink(args.base || "https://csat.example.com", secret, args.subject, ts, args.lang || "en"));
}
