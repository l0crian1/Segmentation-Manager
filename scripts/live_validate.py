"""Opt-in reversible MPLS smoke test. Credentials are read from local files/environment.

Run from the project root. Never saves the router configuration. The test refuses
to proceed if its selected pair already imports either peer's exports.
"""
import time
import argparse
import http.cookiejar
import json
import os
import ssl
import urllib.parse
import urllib.request
from pathlib import Path

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--access', default='.local-access.json')
    parser.add_argument('--device', type=int, default=1)
    parser.add_argument('--router', default='10.0.101.14')
    parser.add_argument('--a', default='MPLS01')
    parser.add_argument('--b', default='MPLS02')
    parser.add_argument('--write', action='store_true')
    args = parser.parse_args()
    access = json.loads(Path(args.access).read_text())
    context = ssl._create_unverified_context()  # Local lab app only; router calls below pin the enrolled cert.
    jar = http.cookiejar.CookieJar()
    client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar), urllib.request.HTTPSHandler(context=context))
    csrf = ''
    def app(path, data=None):
        req = urllib.request.Request(access['url']+'/api/'+path, data=json.dumps(data).encode() if data is not None else None, headers={'Content-Type':'application/json','X-CSRF-Token':csrf})
        with client.open(req,timeout=150) as response:
            result = json.load(response)
        while path == 'apply' and result.get('status') == 'applying':
            time.sleep(2)
            result = app('operation?id=' + result['operation_id'])
        return result
    app('login', {'name':access['username'],'password':access['password']})
    csrf = app('me')['csrf']
    device = next(d for d in app('devices') if d['id']==args.device)
    assert device['host']==args.router and device['save'] is False, 'Use the intended test host with saving disabled'
    def snapshot(): return app(f'snapshot?id={args.device}&mode=mpls&family=ipv4-unicast')
    s = snapshot()
    print('Discovered', len(s['vrfs']), 'MPLS VRFs on', s['version'])
    if not args.write:
        print('Read-only validation complete. Use --write for the reversible pair test.')
        return
    key = os.environ['VYOS_TEST_API_KEY']
    import socket, hashlib
    raw_socket = socket.create_connection((args.router,device['port']),timeout=10)
    with context.wrap_socket(raw_socket,server_hostname=args.router) as tls_socket:
        assert hashlib.sha256(tls_socket.getpeercert(binary_form=True)).hexdigest()==device['fingerprint'], 'Router certificate changed'
    def router(op,path=None):
        endpoint='retrieve' if op=='showConfig' else 'configure'
        data={'op':op,'path':path} if path is not None else op
        request=urllib.request.Request(f"https://{args.router}:{device['port']}/{endpoint}",data=urllib.parse.urlencode({'key':key,'data':json.dumps(data)}).encode())
        with urllib.request.urlopen(request,context=context,timeout=60) as response:result=json.load(response)
        assert result['success'], 'Router API failed'
        return result['data']
    before = router('showConfig',[])
    selected = {v['name']:v for v in s['vrfs']}
    a,b=selected[args.a],selected[args.b]
    assert not a.get('reason') and not b.get('reason'), 'Pair must be editable'
    assert not set(a['imports'])&set(b['exports']) and not set(b['imports'])&set(a['exports']), 'Pair already has imports'
    edits=[{'a':args.a,'b':args.b,'enabled':True}]
    def payload(s,edits):return {'device_id':args.device,'mode':'mpls','family':'ipv4-unicast','fingerprint':s['fingerprint'],'edits':edits}
    plan=app('plan',payload(s,edits))
    assert len(plan['commands'])==2
    applied=False
    try:
        applied=True  # A timeout may still commit, so always reconcile in finally.
        result=app('apply',payload(s,edits))
        assert result['status']=='applied-unsaved',result['status']
        print('PASS: bidirectional MPLS imports applied and independently retrieved')
        actual=snapshot();m={v['name']:v for v in actual['vrfs']}
        assert set(b['exports'])<=set(m[args.a]['imports']) and set(a['exports'])<=set(m[args.b]['imports'])
        result=app('apply',payload(actual,[{'a':args.a,'b':args.b,'enabled':False}]))
        assert result['status']=='applied-unsaved'
        print('PASS: bidirectional imports removed through the application')
    finally:
        if applied:
            current=router('showConfig',[])
            changes=[]
            for name,peer in [(args.a,b),(args.b,a)]:
                def rt(config):return config['vrf']['name'][name]['protocols']['bgp']['address-family']['ipv4-unicast']['route-target']['vpn']
                old=rt(before).get('import');now=rt(current).get('import')
                if old==now:continue
                allowed=set((old or '').split())|set(peer['exports'])
                if not set((now or '').split())<=allowed:raise RuntimeError('External import drift detected; automatic cleanup refused for '+name)
                path=['vrf','name',name,'protocols','bgp','address-family','ipv4-unicast','route-target','vpn','import']
                changes.append({'op':'set' if old else 'delete','path':path+([old] if old else [])})
            if changes:router(changes)
            assert router('showConfig',[])==before,'Full running configuration differs after cleanup'
            print('PASS: entire router running configuration exactly matches its pre-test snapshot; no save issued')

if __name__=='__main__':main()
