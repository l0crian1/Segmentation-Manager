"""Opt-in VRF/matrix configuration round trips on disposable VyOS lab routers.

Run with --host ADDRESS (repeatable), --write, VYOS_TEST_API_KEY, and the ignored
.local-access.json application login. Uses temporary VRFs and one route-map;
never saves or configures transport, peers, interfaces, or bridges. Enrolls new
devices with certificate pinning and saving disabled. Verifies full restoration.
"""
import argparse
import copy
import hashlib
import http.client
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


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--host', action='append', required=True)
    parser.add_argument('--access', default='.local-access.json')
    parser.add_argument('--write', action='store_true')
    args = parser.parse_args()
    if not args.write:
        parser.error('--write is required for temporary lab configuration')
    key = os.environ['VYOS_TEST_API_KEY']
    access = json.loads(Path(args.access).read_text(encoding='utf-8'))
    ctx = ssl._create_unverified_context()  # Explicitly selected lab endpoints.
    jar = http.cookiejar.CookieJar()
    client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar),
                                       urllib.request.HTTPSHandler(context=ctx))
    csrf = ''

    def app(path, data=None, method=None):
        req = urllib.request.Request(access['url'] + '/api/' + path,
            data=json.dumps(data).encode() if data is not None else None,
            headers={'Content-Type': 'application/json', 'X-CSRF-Token': csrf}, method=method)
        for attempt in range(3):
            try:
                with client.open(req, timeout=90) as response:
                    return json.load(response)
            except urllib.error.HTTPError as e:
                message = e.read().decode().replace(key, '[redacted]')
                # Retry transient discovery reads only. Never replay writes.
                if data is None and e.code == 502 and 'device request failed' in message and attempt < 2:
                    time.sleep(2)
                    continue
                raise RuntimeError(message) from None

    def completed(path, data):
        result = app(path, data)
        deadline = time.monotonic() + 900
        while result['status'] == 'applying':
            if time.monotonic() > deadline:
                raise TimeoutError('Operation still active; inspect before cleanup')
            time.sleep(2)
            result = app('operation?id=' + result['operation_id'])
        if result['status'] not in ('applied-unsaved', 'unchanged'):
            raise RuntimeError('Configuration operation: ' + result['status'] + ': ' + result.get('message', ''))
        return result

    app('login', {'name': access['username'], 'password': access['password']})
    csrf = app('me')['csrf']
    failures = []
    for host in args.host:
        print('TEST:', host, flush=True)
        with ctx.wrap_socket(socket.create_connection((host, 443), timeout=10), server_hostname=host) as conn:
            pin = hashlib.sha256(conn.getpeercert(binary_form=True)).hexdigest()
        # Pin every direct router request, including fixture cleanup.
        class PinnedConnection(http.client.HTTPSConnection):
            def connect(self):
                super().connect()
                if hashlib.sha256(self.sock.getpeercert(binary_form=True)).hexdigest() != pin:
                    self.close()
                    raise RuntimeError('Router certificate changed')

        def router(endpoint, data):
            connection = PinnedConnection(host, timeout=660, context=ctx)
            try:
                connection.request('POST', '/' + endpoint,
                    body=urllib.parse.urlencode({'key': key, 'data': json.dumps(data)}),
                    headers={'Content-Type': 'application/x-www-form-urlencoded'})
                result = json.load(connection.getresponse())
                if not result['success']:
                    raise RuntimeError(str(result.get('error')).replace(key, '[redacted]'))
                return result.get('data')
            finally:
                connection.close()

        def config():
            return router('retrieve', {'op': 'showConfig', 'path': []})

        devices = app('devices')
        device = next((d for d in devices if d['host'] == host), None)
        if device is None:
            sites = app('sites')
            if not sites:
                app('sites', {'name': 'Release validation'})
                sites = app('sites')
            app('devices', {'site_id': sites[0]['id'], 'name': 'Validation ' + host,
                'host': host, 'port': 443, 'trust': 'pin', 'fingerprint': pin, 'save': False, 'key': key})
            device = next(d for d in app('devices') if d['host'] == host)
        assert device['trust'] == 'pin' and device['fingerprint'] == pin, 'Use pinned lab devices'
        def save_policy(save):
            fields = {k: device[k] for k in ('id', 'site_id', 'name', 'host', 'port', 'trust', 'fingerprint')}
            app('devices', dict(fields, save=save, key=''), method='PUT')
        device_id = device['id']
        baseline = config()
        names = ['SMReleaseA', 'SMReleaseB']
        policy = 'SMReleasePolicy'
        original_vrfs = baseline.get('vrf', {}).get('name', {})
        assert not any(n in original_vrfs for n in names), 'Fixture VRF exists'
        assert policy not in baseline.get('policy', {}).get('route-map', {}), 'Fixture policy exists'
        assert not any(v.get('table') in ('3901', '3902') or v.get('vni') in ('63901', '63902') for v in original_vrfs.values()), 'Fixture table/VNI exists'

        def inventory():
            return app('vrfs?id=' + str(device_id))

        def change(action, vrf):
            s = inventory()
            assert s['writable'], 'Version-based VRF restriction'
            payload = {'device_id': device_id, 'action': action, 'vrf': vrf,
                'fingerprint': s['fingerprint'], 'operation_id': str(uuid.uuid4()), 'confirm_name': vrf['name']}
            plan = app('vrfs/plan', payload)
            assert all(c['path'][:3] == ['vrf', 'name', vrf['name']] for c in plan['commands']), 'VRF edit escaped its scope'
            completed('vrfs/apply', payload)
            print('PASS:', host, action, vrf['name'], flush=True)

        def row(name):
            return next(v for v in inventory()['vrfs'] if v['name'] == name)

        def enabled(v, index):
            v.update(asn='65000')
            for family in ('ipv4', 'ipv6'):
                v[family] = {'enabled': True, 'rd': '65000:' + str(3901 + index),
                    'exports': ['65000:' + str(63901 + index)], 'label': 'auto', 'per_nexthop': False}
            v['evpn'] = {'enabled': True, 'vni': str(63901 + index),
                'rd': '65000:' + str(3901 + index), 'exports': ['65000:' + str(63901 + index)], 'ipv4': True, 'ipv6': True}
            return v

        try:
            if device['save']:
                save_policy(False)
            print('VERSION:', inventory()['version'], flush=True)
            change('create', {'name': names[0], 'table': '3901'})
            change('update', enabled(row(names[0]), 0))
            change('create', enabled({'name': names[1], 'table': '3902'}, 1))
            print('PASS: plain and dual-technology VRF creation; MPLS IPv4/IPv6 and EVPN enablement', flush=True)
            for mode, family in [('mpls', 'ipv4-unicast'), ('mpls', 'ipv6-unicast'), ('evpn', 'ipv4-unicast')]:
                before = config()
                for selected in (True, False):
                    s = app(f'snapshot?id={device_id}&mode={mode}&family={family}')
                    fixture_rows = [v for v in s['vrfs'] if v['name'] in names]
                    assert len(fixture_rows) == 2 and all(v['known'] and not v.get('reason') for v in fixture_rows)
                    edits = [{'a': names[0], 'b': names[0], 'enabled': selected},
                             {'a': names[1], 'b': names[1], 'enabled': selected},
                             {'a': names[0], 'b': names[1], 'enabled': selected}]
                    completed('apply', {'device_id': device_id, 'mode': mode, 'family': family,
                        'fingerprint': s['fingerprint'], 'operation_id': str(uuid.uuid4()), 'edits': edits})
                    fresh = app(f'snapshot?id={device_id}&mode={mode}&family={family}')
                    for v in fresh['vrfs']:
                        if v['name'] in names:
                            assert set(v['imports']) == ({'65000:63901', '65000:63902'} if selected else set())
                    if selected:
                        # An ordinary VRF edit must preserve all three families' imports.
                        v = row(names[0])
                        policies = [copy.deepcopy(v[f]['imports']) for f in ('ipv4', 'ipv6', 'evpn')]
                        v['description'] = 'Release validation ' + mode + family
                        change('update', v)
                        v = row(names[0])
                        assert [v[f]['imports'] for f in ('ipv4', 'ipv6', 'evpn')] == policies
                        v['description'] = ''
                        change('update', v)
                assert config() == before, 'Matrix round trip changed unrelated configuration'
                print('PASS:', mode, family, 'pair and diagonal imports; VRF edits preserve imports', flush=True)
            router('configure', [{'op': 'set', 'path': ['policy', 'route-map', policy, 'rule', '10', 'action', 'permit']}])
            assert policy in inventory()['route_maps'], 'Externally added policy not discovered'
            for assigned in (True, False):
                v = row(names[0])
                for family in ('ipv4', 'ipv6'):
                    v[family].update(import_route_map=policy if assigned else '', export_route_map=policy if assigned else '')
                change('update', v)
                v = row(names[0])
                for family in ('ipv4', 'ipv6'):
                    assert v[family]['import_route_map'] == (policy if assigned else '')
                    assert v[family]['export_route_map'] == (policy if assigned else '')
            print('PASS: all four VPN route-map assignments added and removed', flush=True)
            for selected in (True, False):
                v = row(names[0])
                for family in ('ipv4', 'ipv6'):
                    v[family]['per_nexthop'] = selected
                change('update', v)
                v = row(names[0])
                assert all(v[f]['per_nexthop'] == selected for f in ('ipv4', 'ipv6'))
            print('PASS: IPv4 and IPv6 per-nexthop label allocation enabled and disabled', flush=True)
            for name in names:
                change('delete', row(name))
            print('PASS: VRF deletion through application', flush=True)
        except Exception as e:
            failures.append((host, str(e)))
            print('FAIL:', host, str(e), flush=True)
        finally:
            # Only remove fixture roots absent from the original configuration.
            try:
                current = config()
                commands = [{'op': 'delete', 'path': ['vrf', 'name', n]} for n in names if n in current.get('vrf', {}).get('name', {})]
                if policy in current.get('policy', {}).get('route-map', {}):
                    commands.append({'op': 'delete', 'path': ['policy', 'route-map', policy]})
                if commands:
                    router('configure', commands)
                current = config()
                empty_roots = [{'op': 'delete', 'path': [root]} for root in ('vrf', 'policy')
                               if root not in baseline and current.get(root) == {}]
                if empty_roots:
                    router('configure', empty_roots)
                assert config() == baseline, 'Full router configuration differs after cleanup'
                print('PASS:', host, 'full original configuration restored; no save issued', flush=True)
            finally:
                if device['save']:
                    save_policy(True)
    if failures:
        raise SystemExit(str(failures))


if __name__ == '__main__':
    main()
