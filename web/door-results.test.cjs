const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const { createContext, runInContext } = require('node:vm');
const assert = require('node:assert/strict');
const { test } = require('node:test');

// A small DOM keeps these request/response workflow tests dependency-free.
class Element {
  constructor() { this.children = []; this.dataset = {}; this.value = ''; }
  set textContent(value) { this.content = value; }
  get textContent() { return (this.content || '') + this.children.map(child => child.textContent).join(' '); }
  append(...children) { this.children.push(...children); }
  replaceChildren(...children) { this.children = children; }
  setAttribute() {}
  addEventListener() {}
}

function setup({ open, mock = false, refreshFails = false, history = [] } = {}) {
  const elements = new Map();
  const element = id => {
    if (!elements.has(id)) elements.set(id, new Element());
    return elements.get(id);
  };
  const door = { id: 'test-door', name: '测试门', community: '模拟环境', enabled: true, available: true };
  let calls = 0;
  const response = (value, status = 200) => ({ status, ok: status < 400, json: async () => value });
  const operation = (state, code) => ({ request_id: 'test-request-0001', door_id: door.id, state, code, created_at: Date.now(), updated_at: Date.now() });
  const context = createContext({
    document: { getElementById: element, createElement: () => new Element(), createTextNode: value => ({ textContent: value }) },
    createRequestID: () => 'test-request-0001',
    fetch: async (url, options) => {
      if (url === '/api/v1/auth/session') return new Promise(() => {});
      if (url.endsWith('/open')) {
        calls++;
        assert.equal(options.method, 'POST');
        assert.equal(options.headers['Idempotency-Key'], 'test-request-0001');
        return open ? open({ response, operation }) : response({ operation: operation('accepted', 'upstream_accepted') });
      }
      if (refreshFails) throw new Error('refresh disconnected');
      const values = { '/status': { mock, account: { state: 'valid' } }, '/doors': { doors: [door] }, '/tokens': { tokens: [] }, '/operations': { operations: history } };
      return response(values[url.slice('/api/v1'.length)]);
    }
  });
  runInContext(readFileSync(join(__dirname, 'app.js'), 'utf8'), context);
  context.testDoor = door;
  context.testMock = mock;
  runInContext("currentStatus={mock:testMock,account:{state:'valid'}};currentDoors=[testDoor];renderDoors();", context);
  return {
    element, history,
    request: () => runInContext('requestOpen(testDoor)', context),
    reload: () => runInContext('load()', context),
    rerender: () => runInContext('renderDoors()', context),
    calls: () => calls,
    text: () => element('doors').textContent,
    openButton: () => element('doors').children[0].children.find(child => child.className === 'door-actions').children[0]
  };
}

test('a successful response stays visible when refreshing the page data fails', async () => {
  const ui = setup({ refreshFails: true });
  await ui.request();
  assert.match(ui.text(), /亲邻返回成功/);
  assert.match(ui.text(), /请确认门的实际状态/);
  assert.match(ui.element('notice-message').textContent, /记录刷新失败/);
  assert.equal(ui.calls(), 1);
});

test('mock success is explicitly distinguished from a real door command', async () => {
  const ui = setup({ mock: true });
  await ui.request();
  assert.match(ui.text(), /模拟请求成功/);
  assert.match(ui.text(), /未向真实门禁发送命令/);
  assert.doesNotMatch(ui.text(), /亲邻返回成功/);
});

test('expired login and unknown upstream responses have distinct visible results', async () => {
  for (const [state, code, label, detail] of [
    ['rejected', 'expired', /请求被拒绝/, /重新短信登录/],
    ['unknown', 'unavailable', /结果不确定/, /检查门的实际状态/]
  ]) {
    const ui = setup({ open: ({ response, operation }) => response({ operation: operation(state, code) }, 409) });
    await ui.request();
    assert.match(ui.text(), label);
    assert.match(ui.text(), detail);
    assert.equal(ui.calls(), 1);
  }
});

test('both a lost response and an unreadable body remain uncertain and never retry', async () => {
  for (const open of [
    async () => { throw new Error('disconnected'); },
    () => ({ status: 200, json: async () => { throw new Error('body truncated'); } }),
    ({ response }) => response({ ok: true })
  ]) {
    const ui = setup({ open });
    await ui.request();
    assert.match(ui.text(), /结果不确定/);
    assert.match(ui.text(), /不会自动重试/);
    assert.equal(ui.calls(), 1);
  }
});

test('repeated clicks and another render cannot dispatch a second in-flight command', async () => {
  let release;
  const ui = setup({ open: ({ response, operation }) => new Promise(resolve => { release = () => resolve(response({ operation: operation('accepted', 'upstream_accepted') })); }) });
  const first = ui.request();
  await ui.request();
  ui.rerender();
  assert.equal(ui.calls(), 1);
  assert.equal(ui.openButton().disabled, true);
  assert.match(ui.text(), /正在处理/);
  release();
  await first;
  assert.equal(ui.openButton().disabled, false);
  assert.match(ui.text(), /亲邻返回成功/);
});

test('reading stored results restores the card and resolves a lost response without another command', async () => {
  const history = [{ request_id: 'older-request', door_id: 'test-door', state: 'accepted', created_at: 1 }];
  const ui = setup({ history, open: async () => { throw new Error('response lost'); } });
  await ui.reload();
  assert.match(ui.text(), /亲邻返回成功/);
  await ui.request();
  await ui.reload();
  assert.match(ui.text(), /结果不确定/); // An older success cannot overwrite this request.
  history.unshift({ request_id: 'test-request-0001', door_id: 'test-door', state: 'accepted', created_at: Date.now() });
  await ui.reload();
  assert.match(ui.text(), /亲邻返回成功/);
  assert.match(ui.element('operations').textContent, /test-request-0001/);
  assert.equal(ui.calls(), 1);
});
