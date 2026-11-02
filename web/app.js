'use strict';
const $ = id => document.getElementById(id);
let csrf = '', currentStatus = null, currentDoors = [];
const errors = {
  invalid_credentials:'管理员账号或密码不正确。',
  invalid_token:'管理密钥不正确。',authentication_required:'请重新进入管理页面。',invalid_origin:'访问地址与服务器的 QINLIN_PUBLIC_URL 不一致。',csrf_required:'页面登录已失效，请刷新页面后重试。',rate_limited:'操作太频繁，请稍后再试。',sms_rate_limited:'验证码每分钟最多发送一次，每小时最多 5 次。',invalid_phone:'请输入正确的 11 位手机号。',invalid_login_fields:'请检查手机号和验证码。',upstream_rejected:'亲邻拒绝了请求，请检查验证码或按官方提示完成验证。',upstream_expired:'亲邻会话已失效，请重新短信登录。',upstream_denied:'当前亲邻账号没有该门禁权限。',upstream_protocol:'亲邻响应格式暂不兼容，未确认成功。',upstream_unavailable:'亲邻服务暂时无法连接，请稍后手动再试。',upstream_rate_limited:'亲邻要求降低请求频率，请稍后再试。',login_required:'请先重新登录亲邻账号。',door_not_allowed:'该门尚未启用或已失去权限。',door_cooldown:'该门请求正在处理或处于短暂冷却中，请先确认门的实际状态。',storage_error:'服务存储异常，请检查服务器。'
};
function notice(message,error=false){$('notice-message').textContent=message;$('service-link').hidden=true;$('notice').dataset.error=String(error);$('notice').hidden=false;}
function showError(error){
  notice(error.message,true);
  if(error.connectionFailed){
    let url='http://localhost:8080/';
    try{const configured=new URL(document.querySelector('meta[name="qinlin-service-url"]').content);if(['http:','https:'].includes(configured.protocol))url=configured.origin+'/';}catch{}
    $('service-link').href=url;$('service-link').hidden=false;
  }
}
function connectionError(message){const error=new Error(message);error.connectionFailed=true;return error;}
function showLogin(){csrf='';$('login-panel').hidden=false;$('workspace').hidden=true;$('logout').hidden=true;hideSecret();}
async function api(path,method='GET',body){
  const headers={};if(method!=='GET'){headers['X-CSRF-Token']=csrf;if(body!==undefined)headers['Content-Type']='application/json';}
  let response;try{response=await fetch('/api/v1'+path,{method,headers,credentials:'same-origin',cache:'no-store',body:body===undefined?undefined:JSON.stringify(body)});}catch{throw connectionError('当前页面无法连接服务。请将下方服务地址复制到 Chrome、Edge 或 Safari 的地址栏打开，再登录操作。');}
  let data;try{data=await response.json();}catch{throw connectionError('当前页面未收到服务接口响应。请直接打开服务地址后重试。');}
  if(!response.ok){if(response.status===401)showLogin();throw new Error(data.message||errors[data.code]||('请求未成功：'+(data.code||response.status)));}
  return data;
}
async function busy(button,fn){button.disabled=true;try{await fn();}catch(e){showError(e);}finally{button.disabled=false;}}
function text(tag,value,className){const e=document.createElement(tag);e.textContent=value;if(className)e.className=className;return e;}
function date(ms){return ms?new Date(ms).toLocaleString('zh-CN',{hour12:false}):'尚无记录';}
function hideSecret(){$('shortcut-result').hidden=true;$('shortcut-url').value='';$('shortcut-auth').value='';}
const states={disconnected:'尚未关联亲邻账号',valid:'亲邻账号最近验证通过',expired:'亲邻会话已失效',unknown:'亲邻会话状态暂无法确认',relogin_required:'登录后查门未完成，请重新登录'};
async function load(){
  const [status,doors,tokens,ops]=await Promise.all([api('/status'),api('/doors'),api('/tokens'),api('/operations')]);
  currentStatus=status;currentDoors=doors.doors;
  $('login-panel').hidden=true;$('workspace').hidden=false;$('logout').hidden=false;
  $('account-state').textContent=states[status.account.state]||'状态未知';$('verified-at').textContent='最近查门确认：'+date(status.account.verified_at);
  $('mode').hidden=!status.mock;$('mock-help').hidden=!status.mock;
  if(['disconnected','expired','relogin_required'].includes(status.account.state))$('account-details').open=true;
  renderDoors();renderTokens(tokens.tokens);renderOps(ops.operations);
}
function renderDoors(){
  const root=$('doors');root.replaceChildren();$('door-empty').hidden=currentDoors.length>0;
  const canOpen=['valid','unknown'].includes(currentStatus.account.state);
  for(const d of currentDoors){
    const card=text('article','','door-card'),heading=text('div','','door-heading'),names=text('div','');
    names.append(text('div',d.name,'door-name'),text('p',d.community+(d.available?'':' · 当前无权限'),'door-community'));heading.append(names);
    const toggle=text('label','','door-toggle'),checkbox=document.createElement('input');checkbox.type='checkbox';checkbox.checked=d.enabled;checkbox.disabled=!d.available;checkbox.setAttribute('aria-label','启用 '+d.name);toggle.append(checkbox,document.createTextNode('启用'));heading.append(toggle);card.append(heading);
    checkbox.addEventListener('change',()=>busy(checkbox,async()=>{try{await api('/doors/'+d.id,'PATCH',{enabled:checkbox.checked});await load();}catch(e){checkbox.checked=!checkbox.checked;throw e;}}));
    const actions=text('div','','door-actions'),open=text('button',currentStatus.mock?'模拟开门':'开门','primary'),shortcut=text('button','配置快捷指令','secondary');
    open.disabled=!d.enabled||!d.available||!canOpen;shortcut.disabled=!d.enabled||!d.available;
    open.addEventListener('click',()=>busy(open,async()=>{
      const requestID=createRequestID();
      // No automatic retry, service worker, queued action, or reconnect handler.
      let response;try{response=await fetch('/api/v1/doors/'+d.id+'/open',{method:'POST',credentials:'same-origin',cache:'no-store',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf,'Idempotency-Key':requestID},body:'{}'});}catch{throw new Error('开门请求连接中断，结果不确定，请先检查门的实际状态。');}
      const result=await response.json();if(response.status===401)showLogin();notice(result.message||errors[result.code]||'请求失败。',!result.ok);
      if(response.status!==401)await load();
    }));
    shortcut.addEventListener('click',()=>busy(shortcut,async()=>{hideSecret();const data=await api('/tokens','POST',{door_id:d.id,name:'iPhone · '+d.name});$('shortcut-url').value=data.open_url;$('shortcut-auth').value='Bearer '+data.secret;$('shortcut-result').hidden=false;$('shortcut-details').open=true;$('shortcut-result').scrollIntoView({behavior:'smooth',block:'center'});renderTokens((await api('/tokens')).tokens);}));
    actions.append(open,shortcut);card.append(actions);root.append(card);
  }
}
function renderTokens(tokens){const root=$('tokens');root.replaceChildren();if(!tokens.length){root.append(text('p','尚未创建快捷指令令牌。','muted small'));return;}for(const t of tokens){const row=text('div','','token-row'),info=text('div','');info.append(text('strong',t.name),text('p',(t.revoked?'已撤销':t.expires_at<Date.now()?'已到期':'到期时间：'+date(t.expires_at))));row.append(info);if(!t.revoked&&t.expires_at>Date.now()){const revoke=text('button','撤销','quiet');revoke.addEventListener('click',()=>busy(revoke,async()=>{await api('/tokens/'+t.id,'DELETE');hideSecret();await load();}));row.append(revoke);}root.append(row);}}
function renderOps(ops){const root=$('operations');root.replaceChildren();if(!ops.length){root.append(text('p','暂无开门请求。','muted small'));return;}const labels={accepted:'服务端已接受',unknown:'结果不确定',pending:'正在处理',rejected:'已拒绝'};for(const op of ops){const d=currentDoors.find(d=>d.id===op.door_id),row=text('div','','operation-row'),info=text('div','');info.append(text('strong',d?d.name:'历史门禁'),text('p',date(op.created_at)));row.append(info,text('span',labels[op.state]||op.state));root.append(row);}}
$('site-login').addEventListener('submit',e=>{e.preventDefault();busy(e.submitter,async()=>{const token=$('admin-token');const credentials=token?{token:token.value}:{username:$('admin-username').value,password:$('admin-password').value};const result=await api('/auth/login','POST',credentials);if(token)token.value='';else $('admin-password').value='';csrf=result.csrf_token;$('notice').hidden=true;await load();});});
$('logout').addEventListener('click',e=>busy(e.currentTarget,async()=>{await api('/auth/logout','POST',{});showLogin();}));
$('send-sms').addEventListener('click',e=>busy(e.currentTarget,async()=>{const result=await api('/account/sms','POST',{phone:$('phone').value});notice(result.mock?'模拟模式未发送短信，请使用验证码 123456。':'验证码已发送，请查收短信。');}));
$('account-login').addEventListener('submit',e=>{e.preventDefault();busy(e.submitter,async()=>{await api('/account/login','POST',{phone:$('phone').value,code:$('sms-code').value});$('sms-code').value='';$('phone').value='';hideSecret();await load();$('account-details').open=false;notice('账号已关联，请启用需要使用的门禁。');});});
$('refresh').addEventListener('click',e=>busy(e.currentTarget,async()=>{await api('/doors/refresh','POST',{});await load();notice('已刷新当前账号的门禁权限。');}));
$('disconnect').addEventListener('click',e=>{if(!confirm('断开后将删除服务器上的亲邻会话，并撤销全部快捷指令令牌。继续？'))return;busy(e.currentTarget,async()=>{await api('/account','DELETE');hideSecret();await load();notice('已断开亲邻账号。');});});
$('hide-secret').addEventListener('click',hideSecret);
for(const [button,input]of [['copy-url','shortcut-url'],['copy-auth','shortcut-auth']]){$(button).addEventListener('click',()=>busy($(button),async()=>{try{await navigator.clipboard.writeText($(input).value);notice('已复制。');}catch{$(input).focus();$(input).select();notice('请长按选中的内容进行复制。');}}));}
(async()=>{try{const session=await api('/auth/session');csrf=session.csrf_token;await load();}catch(e){showLogin();if(!e.message.includes('重新进入'))showError(e);}})();
