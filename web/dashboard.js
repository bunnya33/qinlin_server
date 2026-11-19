'use strict';

const pages = {
  overview: '总览', accounts: '亲邻账号', doors: '门禁管理', shares: '分享接口',
  shortcuts: '快捷指令', activity: '开门记录', updates: '服务更新'
};
const shareStates = {
  ready: ['可用', 'green'], paused: ['已暂停', 'neutral'], denied: ['门无权限', 'red'],
  expired: ['待重新授权', 'peach'], disabled: ['手机服务未启用', 'neutral']
};
let mobileOverview = null, mobileLoad = null, selectedShare = '';
let currentPage = '';

function navigate(page) {
  if (location.hash !== '#' + page) location.hash = page;
  renderPage();
}

function renderPage() {
  const hash = location.hash.slice(1);
  const page = Object.hasOwn(pages, hash) ? hash : 'overview';
  if (currentPage === page) return;
  currentPage = page;
  const view = document.getElementById('view-' + page);
  for (const section of document.querySelectorAll('[data-page]')) section.hidden = section !== view;
  for (const link of document.querySelectorAll('[data-destination]')) {
    if (link.dataset.destination === page) link.setAttribute('aria-current', 'page');
    else link.removeAttribute('aria-current');
  }
  document.getElementById('page-crumb').textContent = pages[page];
  window.scrollTo(0, 0);
  if (page === 'accounts' || page === 'shares') void loadMobileOverview();
}

function badge(label, tone) {
  const result = text('span', label, 'badge');
  result.dataset.tone = tone;
  return result;
}

function elapsed(since) {
  const minutes = Math.max(0, Math.floor((Date.now() - since) / 60000));
  if (minutes >= 1440) return Math.floor(minutes / 1440) + ' 天 ' + Math.floor(minutes % 1440 / 60) + ' 小时';
  if (minutes >= 60) return Math.floor(minutes / 60) + ' 小时 ' + minutes % 60 + ' 分钟';
  return minutes ? minutes + ' 分钟' : '不到 1 分钟';
}

function shareState(share, account) {
  if (!mobileOverview.enabled) return 'disabled';
  if (!share.enabled) return 'paused';
  if (account.expired) return 'expired';
  return share.door_available ? 'ready' : 'denied';
}

function doorLabel(share) { return share.door_name || '历史门禁'; }

function fillFilter(select, label, options) {
  const selected = select.value;
  select.replaceChildren(new Option(label, ''), ...Array.from(options, ([id, name]) => new Option(name, id)));
  if (options.has(selected)) select.value = selected;
}

function renderMobileOverview() {
  const {accounts, shares, enabled} = mobileOverview;
  const summary = $('overview-mobile-accounts');
  const valid = accounts.filter(account => !account.expired).length;
  const item = text('div', '', 'summary-row');
  item.append(text('span', '手机端账号'), badge(enabled ? valid + ' / ' + accounts.length + ' 个已授权' : '服务未启用', enabled && valid ? 'green' : 'neutral'));
  summary.replaceChildren(item);

  const root = $('mobile-accounts');
  root.replaceChildren();
  if (!enabled) root.append(text('p', '手机服务未启用。', 'table-footer muted'));
  if (!accounts.length && enabled) root.append(text('p', '暂无已绑定的手机端账号。', 'table-footer muted'));
  if (accounts.length) {
    const [table, rows] = dataTable(['手机号', '授权状态', '上次授权', '距上次授权', '最近验证']);
    for (const account of accounts) {
      tableRow(rows, [account.masked_phone, account.expired ? badge('待重新授权', 'peach') : badge('已授权', 'green'),
        date(account.authorized_at), elapsed(account.authorized_at), date(account.checked_at)]);
    }
    root.append(table);
  }

  fillFilter($('share-account-filter'), '全部账号', new Map(accounts.map(a => [a.id, a.masked_phone])));
  fillFilter($('share-door-filter'), '全部门禁', new Map(shares.map(s => [s.door_id, doorLabel(s)])));
  renderShares();
}

function renderShareInspector(share, account, state) {
  const inspector = $('share-inspector');
  inspector.replaceChildren();
  inspector.hidden = !share;
  $('share-layout').classList.toggle('has-inspector', Boolean(share));
  if (!share) return;
  const heading = text('div', '', 'inspector-top');
  heading.append(text('h2', share.name), badge(...shareStates[state]));
  const details = document.createElement('dl');
  for (const [label, value] of [
    ['归属账号', account.masked_phone],
    ['绑定门禁', doorLabel(share)],
    ['小区', share.community || '—'],
    ['接口开关', share.enabled ? '已开启' : '已暂停'],
    ['门禁权限', share.door_available ? '有权限' : '无权限'],
    ['上次授权', date(account.authorized_at)],
    ['距上次授权', elapsed(account.authorized_at)]
  ]) {
    const row = text('div', '', 'kv-row');
    row.append(text('dt', label), text('dd', value));
    details.append(row);
  }
  inspector.append(heading, details, text('p', '接口所有者可在手机 App 中启用、暂停或删除。', 'muted small'));
}

function renderShares() {
  if (!mobileOverview) return;
  const {accounts, shares, enabled} = mobileOverview;
  const owners = new Map(accounts.map(account => [account.id, account]));
  const accountFilter = $('share-account-filter').value;
  const doorFilter = $('share-door-filter').value;
  const stateFilter = $('share-state-filter').value;
  const filtered = shares.filter(share =>
    (!accountFilter || accountFilter === share.account_id) &&
    (!doorFilter || doorFilter === share.door_id) &&
    (!stateFilter || stateFilter === shareState(share, owners.get(share.account_id))));
  const root = $('share-list');
  root.replaceChildren();
  if (!filtered.length) {
    renderShareInspector(null);
    root.append(text('p', shares.length ? '没有符合筛选条件的接口。' : enabled ? '暂无手机端分享接口。' : '手机服务未启用。', 'table-footer muted'));
    return;
  }
  const [table, rows] = dataTable(['接口名称', '归属账号', '绑定门禁', '状态']);
  table.classList.add('share-table');
  const active = filtered.find(share => share.id === selectedShare) || filtered[0];
  let previousButton = null;
  for (const share of filtered) {
    const owner = owners.get(share.account_id), state = shareState(share, owner);
    const button = text('button', share.name, 'share-name');
    button.type = 'button';
    button.setAttribute('aria-controls', 'share-inspector');
    button.setAttribute('aria-pressed', String(share === active));
    tableRow(rows, [button, owner.masked_phone, doorLabel(share), badge(...shareStates[state])]);
    if (share === active) previousButton = button;
    button.addEventListener('click', () => {
      previousButton.setAttribute('aria-pressed', 'false');
      button.setAttribute('aria-pressed', 'true');
      previousButton = button;
      selectedShare = share.id;
      renderShareInspector(share, owner, state);
    });
  }
  root.append(table);
  selectedShare = active.id;
  const owner = owners.get(active.account_id);
  renderShareInspector(active, owner, shareState(active, owner));
}

function resetDashboard() {
  mobileLoad = null;
  mobileOverview = null;
  selectedShare = '';
  $('overview-mobile-accounts').replaceChildren();
  $('mobile-accounts').replaceChildren(text('p', '正在读取账号', 'table-footer muted'));
  $('share-list').replaceChildren(text('p', '正在读取接口', 'table-footer muted'));
  for (const [id, label] of [['share-account-filter', '全部账号'], ['share-door-filter', '全部门禁']]) {
    $(id).replaceChildren(new Option(label, ''));
  }
  $('share-state-filter').value = '';
  renderShareInspector(null);
}

function placeNotice(inWorkspace) {
  if (inWorkspace) document.querySelector('.main-content').prepend($('notice'));
  else $('login-notice-slot').append($('notice'));
}

async function loadMobileOverview(reportError = false) {
  if (document.getElementById('workspace').hidden) return;
  if (!mobileLoad) mobileLoad = api('/admin/mobile');
  const request = mobileLoad;
  try {
    const result = await request;
    if (mobileLoad !== request) return;
    mobileOverview = result;
    renderMobileOverview();
  } catch (error) {
    if (mobileLoad !== request) return;
    if (!mobileOverview) {
      $('overview-mobile-accounts').replaceChildren(text('p', '手机端账号暂未读取。', 'small muted'));
      for (const id of ['mobile-accounts', 'share-list']) {
        $(id).replaceChildren(text('p', '暂未读取，可在分享接口页刷新。', 'table-footer muted'));
      }
    }
    if (reportError) throw error;
  } finally {
    if (mobileLoad === request) mobileLoad = null;
  }
}

window.addEventListener('hashchange', renderPage);
for (const id of ['share-account-filter', 'share-door-filter', 'share-state-filter']) {
  document.getElementById(id).addEventListener('change', renderShares);
}
document.getElementById('reload-shares').addEventListener('click', event => {
  busy(event.currentTarget, () => loadMobileOverview(true));
});
renderPage();
