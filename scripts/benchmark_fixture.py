#!/usr/bin/env python3
"""Create inert, scheduling-gated Pod fixtures in an explicitly owned namespace.
Run only against a disposable cluster; delete the fixture namespace afterwards.
"""
import argparse,json,subprocess
p=argparse.ArgumentParser();p.add_argument('--namespace',required=True);p.add_argument('--count',type=int,choices=[1000,5000,10000],required=True);a=p.parse_args()
if not a.namespace.startswith('astrotui-bench'):raise SystemExit('namespace must start with astrotui-bench')
label='app.kubernetes.io/managed-by'
check=subprocess.run(['kubectl','get','namespace',a.namespace,'-o','json'],capture_output=True)
if check.returncode==0:
    ns=json.loads(check.stdout)
    if ns.get('metadata',{}).get('labels',{}).get(label)!='astrotui-benchmark':raise SystemExit('namespace is not owned by this benchmark')
else:
    ns={'apiVersion':'v1','kind':'Namespace','metadata':{'name':a.namespace,'labels':{label:'astrotui-benchmark'}}}
    subprocess.run(['kubectl','create','-f','-'],input=json.dumps(ns).encode(),check=True)
existing=json.loads(subprocess.check_output(['kubectl','get','pods','-n',a.namespace,'-o','json']))
names={item['metadata']['name'] for item in existing['items']}
items=[{'apiVersion':'v1','kind':'Pod','metadata':{'name':f'bench-{i:05}','namespace':a.namespace},'spec':{'schedulingGates':[{'name':'astrotui.test/benchmark'}],'containers':[{'name':'main','image':'registry.k8s.io/pause:3.9'}]}} for i in range(a.count) if f'bench-{i:05}' not in names]
if items:subprocess.run(['kubectl','create','--validate=false','-f','-'],input=json.dumps({'apiVersion':'v1','kind':'List','items':items}).encode(),stdout=subprocess.DEVNULL,check=True)
print(f'{a.namespace}: {a.count} inert pods')
