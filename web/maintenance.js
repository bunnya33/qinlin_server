'use strict';
let maintenanceLoading=false,checkSettingsDirty=false,updateSettingsDirty=false,updatePoll=null,waitingForRestart=false,manualChecking=false,lastCheckStatus=null;
const checkMessages={valid:'账号验证通过',expired:'亲邻登录已过期，请重新短信登录',unavailable:'亲邻暂时无法连接，无法确认是否过期',protocol:'亲邻响应暂不兼容，无法确认是否过期',denied:'亲邻拒绝了权限查询，请检查账号权限',rejected:'亲邻拒绝了检测请求',rate_limited:'亲邻要求降低查询频率'};
const updateMessages={idle:'尚未检查更新。',checking:'正在检查仓库发布的正式版本…',current:'当前已是最新正式版本。',available:'发现新版本，可下载到服务器。',downloading:'正在下载并校验新版本，当前服务继续运行。',ready:'新版本已下载并通过校验，点击重启后生效。',restarting:'正在重启以使用新版本，请稍候…',updated:'新版本已启用。',rolled_back:'新版本启动检查失败，已恢复旧版本。',update_invalid:'更新文件无效，已保留当前版本，请重新下载。',error:'更新操作失败，可手动重试。'};
Object.assign(errors,{invalid_check_interval:'检测间隔请设为 5～1440 分钟，或填写 0 关闭。',account_check_busy:'账号正在检测或处理开门请求，请稍后操作。',update_busy:'更新任务正在进行，请等待当前任务完成。',update_not_ready:'尚无可用的下载版本，请先检查或重新下载。',update_not_managed:'下载和重启更新需要使用 Linux 安装版。',service_restarting:'服务正在重启，本次开门请求未发送。'});

function renderAccountCheck(check){
  lastCheckStatus=check;
  const interval=check.interval_minutes;
  const result=checkMessages[check.code]||'尚未执行账号检测';
  $('account-check-result').textContent=result+(check.checked_at?' · '+date(check.checked_at):'');
  const schedule=interval===0?'自动检测已关闭':check.paused?'自动检测已暂停，关联有效账号后继续':'每 '+interval+' 分钟自动检测 · 下次 '+date(check.next_check_at);
  $('account-check-summary').textContent=check.checking?'正在检测账号状态…':schedule;
  $('account-check-result').dataset.state=check.code==='expired'?'expired':check.code==='valid'?'valid':'unknown';
  if(!checkSettingsDirty)$('check-interval').value=interval;
  $('account-check-now').disabled=manualChecking||check.checking||['disconnected','expired','relogin_required'].includes(currentStatus.account.state);
  document.querySelector('.status-strip').dataset.state=currentStatus.account.state;
}
function renderUpdate(status){
  const busy=['checking','downloading','restarting'].includes(status.state);
  $('current-version').textContent='v'+status.current.version;
  $('update-state-badge').textContent=status.ready?'待重启':status.available?'有新版本':'';
  $('update-state-badge').hidden=!status.ready&&!status.available;
  $('latest-version').textContent=status.latest?'最新正式版本：'+status.latest:status.pending_version?'下载版本：'+status.pending_version:'发布来源：GitHub · bunnya33/qinlin_server';
  let message=updateMessages[status.state]||updateMessages.error;
  if(status.code==='update_not_published')message='仓库尚未发布正式版本，请稍后检查。';
  if(status.code==='update_check_failed')message='无法连接版本发布服务，当前版本继续运行。';
  if(status.code==='update_download_failed')message='下载或校验失败，当前版本继续运行，可重新下载。';
  if(status.ready&&status.pending_version)message+=' 下载版本：'+status.pending_version+'。';
  $('update-message').textContent=message;
  $('update-checked-at').textContent=status.checked_at?'最近检查：'+date(status.checked_at):'';
  $('update-progress').hidden=status.state!=='downloading';
  $('update-progress').max=status.total||1;$('update-progress').value=status.downloaded;
  $('update-download-size').hidden=status.state!=='downloading';
  $('update-download-size').textContent=(status.downloaded/1048576).toFixed(1)+' / '+(status.total/1048576).toFixed(1)+' MB';
  $('update-check').disabled=busy;$('update-download').disabled=busy||!status.managed||!status.available;
  $('update-restart').disabled=busy||!status.managed||!status.ready;
  $('update-restart').dataset.target=status.pending_version||'';
  $('update-requirement').hidden=status.managed;
  $('update-auto-check').disabled=!status.managed;$('update-auto-download').disabled=!status.managed;$('save-update-settings').disabled=!status.managed;
  if(!updateSettingsDirty){$('update-auto-check').checked=status.preferences.auto_check;$('update-auto-download').checked=status.preferences.auto_download;}
  $('update-notes').textContent=status.notes||'';$('update-notes').hidden=!status.notes;
  $('update-release-link').hidden=!status.release_url;
  if(status.release_url)$('update-release-link').href=status.release_url;
  if(updatePoll){clearTimeout(updatePoll);updatePoll=null;}
  if(busy&&!waitingForRestart)updatePoll=setTimeout(()=>refreshUpdate().catch(()=>{}),2000);
}
async function refreshUpdate(){const result=await api('/updates');renderUpdate(result.update);}
async function loadMaintenance(){
  if(maintenanceLoading||$('workspace').hidden||waitingForRestart)return;
  maintenanceLoading=true;
  try{const [check,updates]=await Promise.all([api('/account/check'),api('/updates')]);renderAccountCheck(check.check);renderUpdate(updates.update);}
  catch(error){if(!$('workspace').hidden)$('account-check-summary').textContent='检测设置暂未读取，稍后会重新读取。';}
  finally{maintenanceLoading=false;}
}
$('check-interval').addEventListener('input',()=>{checkSettingsDirty=true;});
$('check-settings').addEventListener('submit',event=>{
  event.preventDefault();busy(event.submitter,async()=>{
    const interval=Number($('check-interval').value);
    if(!Number.isInteger(interval)||!$('check-interval').value.trim()||(interval!==0&&(interval<5||interval>1440)))throw new Error(errors.invalid_check_interval);
    const result=await api('/account/check','PUT',{interval_minutes:interval});checkSettingsDirty=false;renderAccountCheck(result.check);notice('账号检测设置已保存，即刻生效。');
  });
});
$('account-check-now').addEventListener('click',async()=>{
  manualChecking=true;$('account-check-now').disabled=true;
  $('account-check-summary').textContent='正在检测账号状态…';
  try{
    const result=await api('/account/check','POST',{});await load();renderAccountCheck(result.check);
    notice(checkMessages[result.check.code]||'检测已完成。',result.check.code!=='valid');
  }catch(error){showError(error);}
  finally{manualChecking=false;if(lastCheckStatus)renderAccountCheck(lastCheckStatus);}
});
for(const id of ['update-auto-check','update-auto-download'])$(id).addEventListener('change',()=>{updateSettingsDirty=true;});
$('update-settings').addEventListener('submit',event=>{
  event.preventDefault();busy(event.submitter,async()=>{
    const result=await api('/updates/settings','PUT',{auto_check:$('update-auto-check').checked,auto_download:$('update-auto-download').checked});updateSettingsDirty=false;renderUpdate(result.update);notice('更新设置已保存。新版本仍需要你点击重启后生效。');
  });
});
for(const [id,action]of [['update-check','check'],['update-download','download']])$(id).addEventListener('click',async()=>{
  $(id).disabled=true;
  try{const result=await api('/updates/'+action,'POST',{});renderUpdate(result.update);}
  catch(error){$(id).disabled=false;showError(error);}
});
$('update-restart').addEventListener('click',()=>busy($('update-restart'),async()=>{
  const target=$('update-restart').dataset.target;
  if(!confirm('重启后将使用 '+target+'，服务会短暂中断。确认现在重启？'))return;
  waitingForRestart=true;
  try{const result=await api('/updates/restart','POST',{});renderUpdate(result.update);}
  catch(error){waitingForRestart=false;throw error;}
  let recovered=false;
  for(let attempt=0;attempt<90;attempt++){
    await new Promise(resolve=>setTimeout(resolve,1000));
    try{
      const response=await fetch('/healthz',{cache:'no-store'}),health=await response.json();
      recovered=health.ok;
      if(health.ok&&health.version===target.replace(/^v/,'')){
        waitingForRestart=false;showLogin();notice('更新完成，当前版本 v'+health.version+'。请重新登录管理页。');return;
      }
    }catch{}
  }
  waitingForRestart=false;showLogin();notice(recovered?'服务已恢复，仍运行原版本。请重新登录查看更新结果。':'暂未收到重启完成信号，请稍后刷新页面，或查看服务器服务状态。',true);
}));
setInterval(()=>{if(!document.hidden&&!$('workspace').hidden&&!waitingForRestart)load().catch(()=>{});},30000);
if(!$('workspace').hidden)void loadMaintenance();
