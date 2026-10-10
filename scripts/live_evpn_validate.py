"""Opt-in isolated EVPN lab fixture and application round trip.

Requires VYOS_TEST_API_KEY and the ignored local application login file. Adds two
temporary VRFs/VXLANs/bridges/dummies, tests explicit imports, removes its own
fixtures in finally, and never saves. Use only on the designated laboratory host.
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
    p=argparse.ArgumentParser();p.add_argument('--write',action='store_true');args=p.parse_args()
    if not args.write:raise SystemExit('Use --write only on the designated 10.0.101.14 lab host.')
    key=os.environ['VYOS_TEST_API_KEY'];ctx=ssl._create_unverified_context()
    def router(endpoint,data):
        req=urllib.request.Request('https://10.0.101.14/'+endpoint,data=urllib.parse.urlencode({'key':key,'data':json.dumps(data)}).encode())
        try:
            with urllib.request.urlopen(req,context=ctx,timeout=90) as response:out=json.load(response)
        except urllib.error.HTTPError as e:
            raise RuntimeError(e.read().decode().replace(key,'[redacted]'))
        if not out['success']:raise RuntimeError(str(out['error']).replace(key,'[redacted]'))
        return out['data']
    def read():return router('retrieve',{'op':'showConfig','path':[]})
    before=read()
    roots=[];commands=[]
    for index,name in [(3901,'MSVALIDA'),(3902,'MSVALIDB')]:
        for kind,ifname in [('bridge','br'+str(index)),('vxlan','vxlan'+str(index)),('dummy','dum'+str(index))]:
            assert ifname not in before.get('interfaces',{}).get(kind,{}),'Fixture interface already exists'
            roots.append(['interfaces',kind,ifname])
        assert name not in before.get('vrf',{}).get('name',{}),'Fixture VRF already exists'
        roots.append(['vrf','name',name])
        vni=str(60000+index);rt='65000:'+vni
        entries=[
          (['vrf','name',name,'table'],str(index)),(['vrf','name',name,'vni'],vni),
          (['vrf','name',name,'protocols','bgp','system-as'],'65000'),
          (['vrf','name',name,'protocols','bgp','address-family','ipv4-unicast','redistribute','connected'],None),
          (['vrf','name',name,'protocols','bgp','address-family','l2vpn-evpn','advertise','ipv4','unicast'],None),
          (['vrf','name',name,'protocols','bgp','address-family','l2vpn-evpn','route-target','import'],rt),
          (['vrf','name',name,'protocols','bgp','address-family','l2vpn-evpn','route-target','export'],rt),
          (['interfaces','vxlan','vxlan'+str(index),'vni'],vni),
          (['interfaces','vxlan','vxlan'+str(index),'source-address'],'10.255.255.1'),
          (['interfaces','vxlan','vxlan'+str(index),'parameters','nolearning'],None),
          (['interfaces','bridge','br'+str(index),'member','interface','vxlan'+str(index)],None),
          (['interfaces','bridge','br'+str(index),'vrf'],name),
          (['interfaces','dummy','dum'+str(index),'vrf'],name),
          (['interfaces','dummy','dum'+str(index),'address'],'198.18.'+str(index-3800)+'.1/32'),
        ]
        commands.extend({'op':'set','path':path+([value] if value else [])} for path,value in entries)
    access=json.loads(Path('.local-access.json').read_text());jar=http.cookiejar.CookieJar();client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar),urllib.request.HTTPSHandler(context=ctx));csrf=''
    def app(path,data=None):
        req=urllib.request.Request(access['url']+'/api/'+path,data=json.dumps(data).encode() if data is not None else None,headers={'Content-Type':'application/json','X-CSRF-Token':csrf})
        try:
            with client.open(req,timeout=150) as response:result=json.load(response)
            while path=='apply' and result.get('status')=='applying':
                time.sleep(2)
                result=app('operation?id='+result['operation_id'])
            return result
        except urllib.error.HTTPError as e:raise RuntimeError(e.read().decode())
    app('login',{'name':access['username'],'password':access['password']});csrf=app('me')['csrf']
    dev=next(d for d in app('devices') if d['id']==1)
    assert dev['host']=='10.0.101.14' and not dev['save']
    import socket,hashlib
    with ctx.wrap_socket(socket.create_connection(('10.0.101.14',443),timeout=10),server_hostname='10.0.101.14') as conn:
        assert hashlib.sha256(conn.getpeercert(binary_form=True)).hexdigest()==dev['fingerprint'],'Certificate changed'
    try:
        router('configure',commands)
        runtime=router('show',{'op':'show','path':['bgp','l2vpn','evpn','vni','json']})
        print('Fixture BGP runtime:',runtime,flush=True)
        def snap():return app('snapshot?id=1&mode=evpn&family=ipv4-unicast')
        s=snap();vrfs={v['name']:v for v in s['vrfs']}
        for name in ['MSVALIDA','MSVALIDB']:
            assert not vrfs[name].get('reason'),vrfs[name]
            assert vrfs[name].get('known'), 'Runtime target schema was not understood'
        for enable in [True,False]:
            result=app('apply',{'device_id':1,'mode':'evpn','family':'ipv4-unicast','fingerprint':s['fingerprint'],'edits':[{'a':'MSVALIDA','b':'MSVALIDB','enabled':enable}]})
            assert result['status']=='applied-unsaved',result
            s=snap();print('PASS: EVPN pair '+('connected' if enable else 'disconnected')+' and read-back verified',flush=True)
        routes=router('show',{'op':'show','path':['bgp','l2vpn','evpn','route','type','prefix']})
        print('EVPN Type-5 output after disconnect:',str(routes)[:1500],flush=True)
    finally:
        # These names were proved absent before the test. Never replace the full configuration.
        router('configure',[{'op':'delete','path':path} for path in roots])
        after=read()
        if after==before:print('PASS: full router running configuration restored exactly; no save issued',flush=True)
        else:raise RuntimeError('Configuration differs after fixture cleanup; review external concurrent edits. No whole-router rollback attempted.')

if __name__=='__main__':main()
