const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const { runInNewContext } = require('node:vm');
const { webcrypto } = require('node:crypto');
const assert = require('node:assert/strict');
const { test } = require('node:test');

const createRequestID = runInNewContext(
  readFileSync(join(__dirname, 'request-id.js'), 'utf8') + '\ncreateRequestID;'
);

test('HTTPS uses the browser UUID API', () => {
  const id = createRequestID(webcrypto);
  assert.match(id, /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
});

test('HTTP works when randomUUID is unavailable', () => {
  const httpCrypto = { getRandomValues: values => webcrypto.getRandomValues(values) };
  const ids = Array.from({ length: 100 }, () => createRequestID(httpCrypto));
  for (const id of ids) assert.match(id, /^[0-9a-f]{32}$/);
  assert.equal(new Set(ids).size, ids.length);
});

test('missing secure randomness fails before a command can be dispatched', () => {
  assert.throws(() => createRequestID({}), /不支持安全随机数/);
});
