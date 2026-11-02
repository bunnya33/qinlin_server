'use strict';

function createRequestID(cryptoAPI = globalThis.crypto) {
  if (typeof cryptoAPI?.randomUUID === 'function') return cryptoAPI.randomUUID();
  // getRandomValues is available on ordinary HTTP pages as well as HTTPS.
  if (typeof cryptoAPI?.getRandomValues !== 'function') {
    throw new Error('当前浏览器不支持安全随机数，请使用更新的浏览器。');
  }
  const bytes = new Uint8Array(16);
  cryptoAPI.getRandomValues(bytes);
  return Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('');
}
