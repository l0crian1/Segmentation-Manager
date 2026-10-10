"""Reversible two-PE tests for Segmentation Manager. Requires --write for changes.

Uses the existing lab VRFs, changes only import RTs, and restores their exact
original leaves. Never saves. VYOS_TEST_API_KEY supplies the lab API credential.
The .local-access.json file supplies the local application login.
"""
import argparse
import hashlib
import http.cookiejar
import json
import os
import socket
import ssl
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from pathlib import Path

HOSTS = ['10.0.101.14', '10.0.101.11']


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--write', action='store_true')
    args = parser.parse_args()
    key = os.environ['VYOS_TEST_API_KEY']
    access = json.loads(Path('.local-access.json').read_text())
    ctx = ssl._create_unverified_context()  # Designated self-signed lab endpoints.
    jar = http.cookiejar.CookieJar()
    client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar), urllib.request.HTTPSHandler(context=ctx))
    csrf = ''

    def app(path, data=None):
        req = urllib.request.Request(access['url'] + '/api/' + path,
            data=json.dumps(data).encode() if data is not None else None,
            headers={'Content-Type': 'application/json', 'X-CSRF-Token': csrf})
        try:
            with client.open(req, timeout=90) as response:
                return json.load(response)
        except urllib.error.HTTPError as e:
            raise RuntimeError(e.read().decode()) from None

    def router(host, endpoint, data):
        req = urllib.request.Request('https://' + host + '/' + endpoint,
            data=urllib.parse.urlencode({'key': key, 'data': json.dumps(data)}).encode())
        try:
            with urllib.request.urlopen(req, context=ctx, timeout=660) as response:
                result = json.load(response)
        except urllib.error.HTTPError as e:
            raise RuntimeError(e.read().decode().replace(key, '[redacted]')) from None
        if not result['success']:
            raise RuntimeError(str(result['error']).replace(key, '[redacted]'))
        return result['data']

    def config(host):
        return router(host, 'retrieve', {'op': 'showConfig', 'path': []})

    app('login', {'name': access['username'], 'password': access['password']})
    csrf = app('me')['csrf']
    devices = app('devices')
    for host in HOSTS:
        with ctx.wrap_socket(socket.create_connection((host, 443), timeout=10), server_hostname=host) as conn:
            pin = hashlib.sha256(conn.getpeercert(binary_form=True)).hexdigest()
        found = next((d for d in devices if d['host'] == host), None)
        if found is None:
            if not args.write:
                raise RuntimeError('Enroll ' + host + ' in the app first, or use --write for lab enrollment.')
            app('devices', {'site_id': app('sites')[0]['id'], 'name': 'Validation PE ' + host,
                'host': host, 'port': 443, 'trust': 'pin', 'fingerprint': pin, 'save': False, 'key': key})
            devices = app('devices')
            found = next(d for d in devices if d['host'] == host)
        assert found['fingerprint'] == pin and found['trust'] == 'pin', 'Unexpected certificate trust'
    selected = {h: next(d for d in devices if d['host'] == h) for h in HOSTS}
    baselines = {h: config(h) for h in HOSTS}
    changed = []
    original_save = {h: selected[h]['save'] for h in HOSTS}
    def save_policy(host, enabled):
        d = selected[host]
        fields = {k: d[k] for k in ['id','site_id','name','host','port','trust','fingerprint']}
        fields.update(save=enabled, key='')
        req = urllib.request.Request(access['url']+'/api/devices', method='PUT', data=json.dumps(fields).encode(),
            headers={'Content-Type':'application/json','X-CSRF-Token':csrf})
        with client.open(req, timeout=60) as response:
            json.load(response)


    def snapshot(host, mode):
        return app(f"snapshot?id={selected[host]['id']}&mode={mode}&family=ipv4-unicast")

    def imports(raw, name, mode):
        afi = 'ipv4-unicast' if mode == 'mpls' else 'l2vpn-evpn'
        rt = raw['vrf']['name'][name]['protocols']['bgp']['address-family'][afi]['route-target']
        return rt.get('vpn', {}).get('import') if mode == 'mpls' else rt.get('import')

    try:
        if args.write:
            for host in HOSTS:
                if original_save[host]: save_policy(host, False)
        for mode, count in [('mpls', 15), ('evpn', 5)]:
            for host in HOSTS:
                s = snapshot(host, mode)
                vrfs = s['vrfs']
                assert len(vrfs) == count, (host, mode, len(vrfs))
                assert all(v['known'] and not v.get('reason') for v in vrfs), vrfs
                print(f"PASS: {host} discovered {count} editable {mode} VRFs", flush=True)
                if not args.write:
                    continue
                edits = [{'a': a['name'], 'b': b['name'], 'enabled': True}
                    for i, a in enumerate(vrfs) for b in vrfs[i:]]
                payload = {'device_id': selected[host]['id'], 'mode': mode, 'family': 'ipv4-unicast',
                    'fingerprint': s['fingerprint'], 'edits': edits, 'operation_id': str(uuid.uuid4())}
                for v in vrfs:
                    changed.append((host, mode, v['name']))
                started = time.monotonic()
                result = app('apply', payload)
                accepted = time.monotonic() - started
                while result['status'] == 'applying':
                    time.sleep(2)
                    result = app('operation?id=' + result['operation_id'])
                assert result['status'] in ['applied-unsaved', 'unchanged'], result
                fresh = snapshot(host, mode)
                exports = {t for v in fresh['vrfs'] for t in v['exports']}
                assert all(exports <= set(v['imports']) for v in fresh['vrfs']), fresh
                print(f"PASS: {host} {mode} full mesh including diagonals; accepted {accepted:.1f}s, verified {time.monotonic()-started:.1f}s", flush=True)
            if args.write:
                for host in HOSTS:
                    name = 'MPLS01' if mode == 'mpls' else 'EVPN01'
                    # A read-only operational observation, separate from configuration verification.
                    routes = router(host, 'show', {'op': 'show', 'path': ['bgp', 'vrf', name, 'ipv4', 'unicast']})
                    print(f"{host} {name} BGP RIB:\n{str(routes)[:3500]}", flush=True)
    finally:
        errors = []
        for host in HOSTS:
            commands = []
            current = config(host)
            for h, mode, name in changed:
                if h != host:
                    continue
                old = imports(baselines[host], name, mode)
                now = imports(current, name, mode)
                if old == now:
                    continue
                path = ['vrf', 'name', name, 'protocols', 'bgp', 'address-family']
                path += ['ipv4-unicast', 'route-target', 'vpn', 'import'] if mode == 'mpls' else ['l2vpn-evpn', 'route-target', 'import']
                if now is not None:
                    commands.append({'op': 'delete', 'path': path})
                if old is not None:
                    for value in old if isinstance(old, list) else [old]:
                        commands.append({'op': 'set', 'path': path + [value]})
            try:
                if commands:
                    router(host, 'configure', commands)
                assert config(host) == baselines[host], 'Full running configuration differs after cleanup'
                print('PASS: ' + host + ' full running configuration restored exactly; no save issued', flush=True)
            except Exception as e:
                errors.append((host, str(e)))
        if args.write:
            for host in HOSTS:
                if original_save[host]: save_policy(host, True)
        if errors:
            raise RuntimeError(errors)


if __name__ == '__main__':
    main()
