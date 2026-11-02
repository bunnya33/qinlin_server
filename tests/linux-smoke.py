#!/usr/bin/env python3
"""Destructive only to a fresh, dedicated qinlin test installation.

Run as root on a disposable systemd Linux environment. Refuses any existing
qinlin paths or OS account, and uses mock doors exclusively.
"""
import http.cookiejar
import json
import os
import pathlib
import pty
import pwd
import re
import select
import shutil
import socket
import subprocess
import time
import urllib.error
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parents[1]
CONFIG = pathlib.Path('/etc/qinlin/qinlin.env')
PATHS = [pathlib.Path(p) for p in ['/opt/qinlin', '/etc/qinlin', '/var/lib/qinlin', '/etc/systemd/system/qinlin.service', '/usr/local/bin/qinlin']]


def run(command, stdin=None, env=None):
    result = subprocess.run(command, input=stdin, text=True, capture_output=True, env=env, timeout=180)
    if result.returncode:
        raise RuntimeError('command failed: ' + ' '.join(map(str, command)) + '\n' + result.stderr[-1500:])
    return result.stdout


def config():
    return dict(line.split('=', 1) for line in CONFIG.read_text().splitlines() if line and not line.startswith('#'))


def free_port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]


def credentials(output):
    username = re.search(r'新的管理员账号：([^\r\n]+)', output)
    password = re.search(r'新的管理员密码：([^\r\n]+)', output)
    if not (username and password):
        sanitized = re.sub(r'新的管理员(?:账号|密码)：[^\r\n]+', '管理员凭据：[redacted]', output)
        raise AssertionError('reset did not display usable credentials:\n' + sanitized)
    return username.group(1), password.group(1)


class Client:
    def __init__(self, username, password):
        self.base = config()['QINLIN_PUBLIC_URL']
        self.csrf = ''
        self.open = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        self.request('/auth/login', 'POST', {'username': username, 'password': password})

    def request(self, path, method='GET', body=None):
        headers = {'Origin': self.base, 'X-CSRF-Token': self.csrf}
        if body is not None:
            headers['Content-Type'] = 'application/json'
        req = urllib.request.Request(self.base + '/api/v1' + path, data=None if body is None else json.dumps(body).encode(), headers=headers, method=method)
        with self.open.open(req, timeout=15) as response:
            value = json.load(response)
        self.csrf = value.get('csrf_token', self.csrf)
        return value


def manual_credentials(username, password):
    master, slave = pty.openpty()
    proc = subprocess.Popen(['/usr/local/bin/qinlin'], stdin=slave, stdout=slave, stderr=slave, start_new_session=True)
    os.close(slave)
    transcript = b''
    cursor = 0

    def expect(text):
        nonlocal transcript, cursor
        needle = text.encode()
        deadline = time.monotonic() + 45
        while needle not in transcript[cursor:]:
            if time.monotonic() > deadline:
                raise RuntimeError('menu prompt timed out: ' + text)
            readable, _, _ = select.select([master], [], [], 1)
            if readable:
                transcript += os.read(master, 65536)
        cursor = transcript.index(needle, cursor) + len(needle)

    try:
        for prompt, reply in [('请输入数字：', '6'), ('新管理员账号：', username), ('输入隐藏）：', password), ('再次输入密码：', password)]:
            expect(prompt)
            os.write(master, (reply + '\n').encode())
        expect('管理员凭据已生效')
        expect('请输入数字：')
        os.write(master, b'0\n')
        assert proc.wait(timeout=10) == 0
        assert password.encode() not in transcript, 'hidden password was echoed'
    finally:
        os.close(master)
        if proc.poll() is None:
            proc.kill()
            proc.wait()


def main():
    if os.geteuid() != 0:
        raise RuntimeError('run this test as root in a disposable systemd Linux environment')
    if any(path.exists() for path in PATHS):
        raise RuntimeError('existing installation detected; no test changes made')
    try:
        pwd.getpwnam('qinlin')
    except KeyError:
        pass
    else:
        raise RuntimeError('existing qinlin OS account detected; no test changes made')
    offline_dir = os.environ.get('QINLIN_TEST_OFFLINE_DIR')
    if offline_dir:
        installer = pathlib.Path(offline_dir).resolve() / 'install-offline.sh'
        assert installer.is_file(), 'extract the offline package before running the test'
        env = dict(os.environ)
        env.pop('QINLIN_PREBUILT_BINARY', None)
    else:
        binary = pathlib.Path(os.environ.get('QINLIN_PREBUILT_BINARY', str(ROOT / 'bin/qinlin-linux-amd64'))).resolve()
        assert binary.is_file(), 'build the Linux binary before running the test'
        env = dict(os.environ, QINLIN_PREBUILT_BINARY=str(binary))
        installer = ROOT / 'install.sh'
    port = free_port()
    install = ['bash', str(installer), '--port', str(port), '--public-url', 'http://127.0.0.1:' + str(port), '--mock']
    started = False
    try:
        started = True
        bootstrap = os.environ.get('QINLIN_TEST_BOOTSTRAP')
        first_install = ['bash', bootstrap, *install[2:]] if bootstrap else install
        output = run(first_install, env=env)
        username, password = credentials(output)
        original = config()
        original_config_bytes = CONFIG.read_bytes()
        assert run(['systemctl', 'is-active', 'qinlin.service']).strip() == 'active'
        assert run(['systemctl', 'is-enabled', 'qinlin.service']).strip() == 'enabled'
        client = Client(username, password)
        if offline_dir:
            update = client.request('/updates')['update']['preferences']
            assert update['auto_check'] is False and update['auto_download'] is False
            print('PASS offline installation defaults disable GitHub checks and downloads', flush=True)
        client.request('/account/login', 'POST', {'phone': '13800138000', 'code': '123456'})
        door = client.request('/doors')['doors'][0]['id']
        client.request('/doors/' + door, 'PATCH', {'enabled': True})
        door_token = client.request('/tokens', 'POST', {'name': 'Linux smoke', 'door_id': door})['secret']
        marker = pathlib.Path('/var/lib/qinlin/smoke-marker')
        marker.write_text('preserve business data')
        os.chown(marker, pwd.getpwnam('qinlin').pw_uid, pwd.getpwnam('qinlin').pw_gid)
        print('PASS install, systemd, password login, mock account and scoped token', flush=True)

        check = client.request('/account/check')['check']
        assert check['interval_minutes'] == 15
        client.request('/account/check', 'PUT', {'interval_minutes': 5})
        assert client.request('/account/check', 'POST', {})['check']['code'] == 'valid'
        client.request('/account/check', 'PUT', {'interval_minutes': 0})
        assert client.request('/account/check')['check']['next_check_at'] == 0
        assert client.request('/updates')['update']['managed']
        preferences = {'auto_check': False, 'auto_download': bool(offline_dir)}
        client.request('/updates/settings', 'PUT', preferences)
        print('PASS persisted account check interval, manual check and managed update settings', flush=True)

        if os.environ.get('QINLIN_TEST_PUBLISHED_UPDATE') == '1':
            client.request('/updates/check', 'POST', {})
            deadline = time.monotonic() + 60
            while True:
                update = client.request('/updates')['update']
                if update['state'] not in ('checking',):
                    break
                assert time.monotonic() < deadline, 'published update check timed out'
                time.sleep(1)
            assert update['available'], 'published version must be newer than the test baseline: ' + str(update)
            target = update['latest'].removeprefix('v')
            client.request('/updates/download', 'POST', {})
            deadline = time.monotonic() + 180
            while True:
                update = client.request('/updates')['update']
                if update['state'] != 'downloading':
                    break
                assert time.monotonic() < deadline, 'published download timed out'
                time.sleep(1)
            assert update['ready'], 'published binary download or checksum failed: ' + str(update)
            client.request('/updates/restart', 'POST', {})
            deadline = time.monotonic() + 60
            while True:
                try:
                    req = urllib.request.Request(config()['QINLIN_PUBLIC_URL'] + '/healthz')
                    with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(req, timeout=2) as response:
                        if json.load(response)['version'] == target:
                            break
                except (OSError, urllib.error.URLError):
                    pass
                assert time.monotonic() < deadline, 'new version failed to start'
                time.sleep(1)
            client = Client(username, password)
            assert client.request('/status')['account']['state'] == 'valid'
            assert marker.read_text() == 'preserve business data'
            assert CONFIG.read_bytes() == original_config_bytes
            print('PASS published GitHub release check, checksum download, explicit restart, new version and data preservation', flush=True)

        run(['/usr/local/bin/qinlin'], '1\n3\n0\n')
        assert subprocess.run(['systemctl', 'is-active', '--quiet', 'qinlin.service']).returncode != 0
        run(['/usr/local/bin/qinlin'], '2\n4\n0\n')
        assert run(['systemctl', 'is-active', 'qinlin.service']).strip() == 'active'
        with socket.socket() as occupied:
            occupied.bind(('127.0.0.1', 0)); occupied.listen()
            run(['/usr/local/bin/qinlin'], '5\n70000\n5\n' + str(occupied.getsockname()[1]) + '\n0\n')
        assert config()['QINLIN_ADDR'] == original['QINLIN_ADDR']
        new_port = free_port()
        run(['/usr/local/bin/qinlin'], '5\n' + str(new_port) + '\n0\n')
        assert config()['QINLIN_PUBLIC_URL'] == 'http://127.0.0.1:' + str(new_port)
        Client(username, password)
        print('PASS status, stop, start, restart, invalid/occupied port and new port', flush=True)

        manual_credentials('smoke_admin', 'Smoke-test-password-2026!')
        Client('smoke_admin', 'Smoke-test-password-2026!')
        try:
            Client(username, password)
        except urllib.error.HTTPError as error:
            assert error.code == 401
        else:
            raise AssertionError('previous administrator credentials still accepted')
        output = run(['/usr/local/bin/qinlin'], '7\ny\n0\n')
        username, password = credentials(output)
        Client(username, password)
        assert password not in CONFIG.read_text()
        assert config()['QINLIN_ENCRYPTION_KEY'] == original['QINLIN_ENCRYPTION_KEY']
        print('PASS hidden manual password, random reset, real login and key preservation', flush=True)

        before = CONFIG.read_bytes()
        run(install, env=env)
        assert CONFIG.read_bytes() == before
        assert marker.read_text() == 'preserve business data'
        assert Client(username, password).request('/status')['account']['state'] == 'valid'
        assert Client(username, password).request('/updates')['update']['preferences'] == preferences
        req = urllib.request.Request(config()['QINLIN_PUBLIC_URL'] + '/api/v1/doors', headers={'Authorization': 'Bearer ' + door_token})
        with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(req, timeout=10) as response:
            assert len(json.load(response)['doors']) == 1
        run(['/usr/local/bin/qinlin'], '8\nno\n0\n')
        assert PATHS[0].exists()
        run(['/usr/local/bin/qinlin'], '8\nUNINSTALL\n\n')
        assert CONFIG.is_file() and marker.is_file()
        assert not pathlib.Path('/opt/qinlin').exists() and not pathlib.Path('/usr/local/bin/qinlin').exists()
        run(install, env=env)
        assert CONFIG.read_bytes() == before
        Client(username, password)
        run(['/usr/local/bin/qinlin'], '8\nUNINSTALL\ny\nDELETE\n')
        assert not any(path.exists() for path in PATHS)
        print('PASS repeat install, account/token preservation, uninstall cancel/retain/delete and reinstall', flush=True)
    finally:
        if started:
            subprocess.run(['systemctl', 'stop', 'qinlin.service'], capture_output=True)
            subprocess.run(['systemctl', 'disable', 'qinlin.service'], capture_output=True)
            for path in PATHS:
                if path.is_dir():
                    shutil.rmtree(path)
                elif path.exists():
                    path.unlink()
            subprocess.run(['systemctl', 'daemon-reload'], capture_output=True)
            subprocess.run(['systemctl', 'reset-failed', 'qinlin.service'], capture_output=True)
            subprocess.run(['userdel', 'qinlin'], capture_output=True)
            subprocess.run(['groupdel', 'qinlin'], capture_output=True)


if __name__ == '__main__':
    main()
