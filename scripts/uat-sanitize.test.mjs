import { test } from 'node:test';
import assert from 'node:assert/strict';
import { sanitizer } from './uat-sanitize.mjs';

test('synthetic quote/backslash secrets are removed at repeated JSON escaping depths', () => {
  const safe = sanitizer({});
  const secret = 'synthetic-quote"-slash\\-line\n-end';
  safe.add(secret);
  for (const value of [secret, encodeURIComponent(secret)]) {
    let nested = { unrelated: value, token: value };
    for (let depth = 0; depth < 8; depth++) {
      const encoded = JSON.stringify(nested);
      let decoded = JSON.parse(safe.redact(encoded));
      for (let i = 0; i < depth; i++) decoded = JSON.parse(decoded);
      assert.equal(decoded.unrelated, '[REDACTED]', 'Nested secret was not removed');
      assert.equal(decoded.token, '[REDACTED]', 'Nested token was not removed');
      nested = encoded;
    }
  }
});

test('credential-shaped environment values are redacted; other values and short strings are not', () => {
  const safe = sanitizer({ PROVIDER_API_KEY: 'synthetic-env-secret-value', GITHUB_TOKEN: 'synthetic-token-value', PATH: '/usr/bin', SHORT_SECRET: 'abc' });
  const text = safe.redact('key synthetic-env-secret-value token synthetic-token-value path /usr/bin short abc');
  assert.equal(text, 'key [REDACTED] token [REDACTED] path /usr/bin short abc');
});

test('token-shaped query parameters, bearer credentials and JSON credential fields are redacted', () => {
  const { redact } = sanitizer({});
  assert.equal(redact('/api?token=abc123&x=1'), '/api?token=[REDACTED]&x=1');
  assert.equal(redact('Authorization: Bearer abc.def-123'), 'Authorization: Bearer [REDACTED]');
  assert.equal(redact('{"apiKey": "value \\" quoted", "name": "kept"}'), '{"apiKey": "[REDACTED]", "name": "kept"}');
});
