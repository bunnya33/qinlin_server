#!/usr/bin/env bash
set -uo pipefail
umask 077

INSTALL_DIR=/opt/qinlin
CONFIG_DIR=/etc/qinlin
CONFIG_FILE=$CONFIG_DIR/qinlin.env
BINARY=$INSTALL_DIR/bin/qinlin-server
SERVICE=qinlin.service

if [[ ${EUID} -ne 0 ]]; then echo '请使用 sudo qinlin 打开管理菜单。' >&2; exit 1; fi
[[ -x $BINARY && -f $CONFIG_FILE ]] || { echo '安装不完整，请重新运行 install.sh。' >&2; exit 1; }
exec 9>/run/lock/qinlin-manage.lock
flock -n 9 || { echo '另一个安装或管理操作正在进行。' >&2; exit 1; }

config_field() { "$BINARY" config show --config "$CONFIG_FILE" | python3 -c 'import json,sys;print(json.load(sys.stdin)[sys.argv[1]])' "$1"; }
local_health_url() {
  config_field addr | python3 -c 'import sys;h,p=sys.stdin.read().strip().rsplit(":",1);h=h.strip("[]");h="127.0.0.1" if h in ("","0.0.0.0") else "::1" if h=="::" else h;print("http://"+("["+h+"]" if ":" in h else h)+":"+p+"/healthz")'
}
wait_ready() {
  local url attempt
  url=$(local_health_url) || return 1
  for ((attempt=0;attempt<15;attempt++)); do
    if systemctl is-active --quiet "$SERVICE" && curl -fsS --max-time 2 "$url" >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  return 1
}
status() {
  if systemctl is-active --quiet "$SERVICE"; then echo '服务状态：运行中'; else echo '服务状态：已停止或启动失败'; fi
  if systemctl is-enabled --quiet "$SERVICE"; then echo '开机自启：已启用'; else echo '开机自启：未启用'; fi
  echo "访问地址：$(config_field public_url)"
  echo "管理员账号：$(config_field username)"
  systemctl status "$SERVICE" --no-pager --lines=5 || true
}
service_action() {
  local action=$1
  [[ $action == stop ]] || systemctl reset-failed "$SERVICE" 2>/dev/null || true
  if systemctl "$action" "$SERVICE"; then
    if [[ $action == stop ]]; then echo '服务已停止。'; elif wait_ready; then echo "服务操作成功。访问地址：$(config_field public_url)"; else echo '服务未通过健康检查，请运行 journalctl -u qinlin -n 50 查看日志。'; fi
  else echo '服务操作失败，请检查 systemd 日志。'; fi
}
restart_with_rollback() {
  local backup=$1
  systemctl reset-failed "$SERVICE" 2>/dev/null || true
  if systemctl restart "$SERVICE" && wait_ready; then rm -f -- "$backup"; return 0; fi
  echo '新配置启动失败，正在恢复原配置……'
  cp -p -- "$backup" "$CONFIG_FILE"
  rm -f -- "$backup"
  systemctl reset-failed "$SERVICE" 2>/dev/null || true
  systemctl restart "$SERVICE" || true
  return 1
}
change_port() {
  local current_port new_port backup
  current_port=$(config_field addr); current_port=${current_port##*:}
  echo "当前端口：$current_port"
  read -r -p '请输入新端口（1～65535，直接回车取消）：' new_port || return
  [[ -n $new_port ]] || return
  [[ $new_port =~ ^[0-9]{1,5}$ ]] || { echo '端口格式不正确。'; return; }
  new_port=$((10#$new_port))
  ((new_port>=1 && new_port<=65535)) || { echo '端口超出范围。'; return; }
  backup=$(mktemp "$CONFIG_DIR/.port-backup-XXXXXX") || return
  cp -p -- "$CONFIG_FILE" "$backup"
  if ! "$BINARY" config set-port --config "$CONFIG_FILE" --port "$new_port" >/dev/null; then rm -f -- "$backup"; return; fi
  if restart_with_rollback "$backup"; then echo "端口已更新。新访问地址：$(config_field public_url)"; fi
}
change_admin() {
  local mode=$1 confirm backup credentials
  if [[ $mode == reset ]]; then
    read -r -p '确认随机重置管理员账号和密码？输入 y 确认：' confirm || return
    [[ $confirm == y || $confirm == Y ]] || { echo '已取消。'; return; }
  fi
  backup=$(mktemp "$CONFIG_DIR/.admin-backup-XXXXXX") || return
  cp -p -- "$CONFIG_FILE" "$backup"
  if [[ $mode == reset ]]; then
    if ! credentials=$("$BINARY" admin reset --config "$CONFIG_FILE"); then rm -f -- "$backup"; return; fi
  elif ! "$BINARY" admin set --config "$CONFIG_FILE"; then rm -f -- "$backup"; return
  fi
  if restart_with_rollback "$backup"; then
    [[ $mode != reset ]] || printf '%s\n' "$credentials"
    echo '管理员凭据已生效，旧网页登录已撤销；亲邻账号、门禁和快捷指令令牌已保留。'
  fi
}
uninstall() {
  local confirm delete_data=n
  echo '将停止服务、取消开机自启，并删除 systemd 服务定义、/opt/qinlin 程序和 /usr/local/bin/qinlin 管理命令。'
  echo '共享运行时和系统依赖不会删除。配置与业务数据默认保留。'
  read -r -p '确认卸载？输入 UNINSTALL 继续：' confirm || return
  [[ $confirm == UNINSTALL ]] || { echo '已取消卸载。'; return; }
  read -r -p '是否同时删除 /etc/qinlin 配置和 /var/lib/qinlin 数据？输入 y 删除，回车保留：' delete_data || return
  if [[ $delete_data == y || $delete_data == Y ]]; then
    read -r -p '彻底删除不可恢复。再次输入 DELETE 确认：' confirm || return
    [[ $confirm == DELETE ]] || { echo '未确认彻底删除，已取消卸载。'; return; }
  fi
  if ! systemctl stop "$SERVICE"; then echo '服务停止失败，取消删除。'; return; fi
  if ! systemctl disable "$SERVICE"; then echo '取消开机自启失败，取消删除。'; return; fi
  rm -f -- /etc/systemd/system/qinlin.service /usr/local/bin/qinlin
  systemctl daemon-reload
  systemctl reset-failed "$SERVICE" 2>/dev/null || true
  rm -rf -- /opt/qinlin
  if [[ $delete_data == y || $delete_data == Y ]]; then
    rm -rf -- /etc/qinlin /var/lib/qinlin
    echo '卸载完成，配置与默认业务数据已删除。手动配置在其他目录的数据请自行处理。'
  else echo '卸载完成，已保留 /etc/qinlin 和 /var/lib/qinlin；重新安装可继续使用。'; fi
  exit 0
}

while true; do
  printf '\n亲邻开门服务管理菜单\n\n1. 查看服务状态\n2. 启动服务\n3. 停止服务\n4. 重启服务\n5. 修改服务器端口\n6. 修改管理员账号和密码\n7. 随机重置管理员账号和密码\n8. 卸载\n0. 退出\n\n'
  read -r -p '请输入数字：' choice || exit 0
  case "$choice" in
    1) status ;;
    2) service_action start ;;
    3) service_action stop ;;
    4) service_action restart ;;
    5) change_port ;;
    6) change_admin set ;;
    7) change_admin reset ;;
    8) uninstall ;;
    0) exit 0 ;;
    *) echo '请输入菜单中的数字。' ;;
  esac
done
