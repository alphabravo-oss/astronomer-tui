#!/usr/bin/env python3
"""Benchmark real terminal processes against an existing isolated fixture namespace.
Requires pyte==0.8.2. Does not create or mutate Kubernetes objects. Reports facts,
not thresholds; run both binaries against the same fixture and terminal size.
"""
import argparse, math, hashlib, re, codecs, fcntl, json, os, pty, select, signal, struct, subprocess, tempfile, termios, time
import pyte

p=argparse.ArgumentParser()
p.add_argument('--binary',required=True)
p.add_argument('--product',choices=['astrotui','k9s'],required=True)
p.add_argument('--context',required=True)
p.add_argument('--namespace',required=True)
p.add_argument('--count',type=int,required=True)
p.add_argument('--width',type=int,default=120)
p.add_argument('--height',type=int,default=40)
p.add_argument('--idle-seconds',type=int,default=0)
p.add_argument('--output',required=True)
p.add_argument('--no-color',action='store_true')
a=p.parse_args()
master,slave=pty.openpty()
fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',a.height,a.width,0,0))
screen=pyte.Screen(a.width,a.height);stream=pyte.Stream(screen)
decoder=codecs.getincrementaldecoder('utf-8')('replace')
config_directory=tempfile.TemporaryDirectory(prefix='astrotui-terminal-benchmark-')
args=[a.binary,'--context',a.context,'--namespace',a.namespace]
args+=['--no-restore','--config-dir',config_directory.name] if a.product=='astrotui' else ['--readonly','--command','pods','--logFile','/tmp/astrotui-k9s-benchmark.log']
env=dict(os.environ,TERM='xterm-256color',COLORTERM='truecolor')
env.pop('NO_COLOR',None)
if a.product=='k9s':env['K9S_CONFIG_DIR']=config_directory.name
if a.no_color:env['NO_COLOR']='1'
started=time.monotonic()
def terminal_session():
    os.setsid()
    fcntl.ioctl(0,termios.TIOCSCTTY,0)
proc=subprocess.Popen(args,stdin=slave,stdout=slave,stderr=slave,env=env,preexec_fn=terminal_session)
os.close(slave)

def pump(timeout=.05):
    if select.select([master],[],[],timeout)[0]:
        try: data=os.read(master,1024*1024)
        except OSError: return
        stream.feed(decoder.decode(data))
def text(): return '\n'.join(screen.display)
def wait(predicate,timeout=30):
    deadline=time.monotonic()+timeout
    while time.monotonic()<deadline:
        pump()
        if predicate(text()): return
        if proc.poll() is not None: raise RuntimeError('process exited: '+text())
    raise TimeoutError('terminal did not reach expected state:\n'+text())
def write(data): os.write(master,data.encode())
def stats():
    with open(f'/proc/{proc.pid}/stat') as f: fields=f.read().split()
    with open(f'/proc/{proc.pid}/status') as f:
        rss=next(int(line.split()[1])*1024 for line in f if line.startswith('VmRSS:'))
    return (int(fields[13])+int(fields[14]))/os.sysconf('SC_CLK_TCK'),rss

result=dict(no_color=a.no_color,binary_sha256=hashlib.sha256(open(a.binary,'rb').read()).hexdigest(),product=a.product,binary=os.path.realpath(a.binary),count=a.count,terminal=[a.width,a.height])
try:
    wait(lambda s: re.search(r'(?<!-)bench-\d{5}\b',s) is not None)
    result['first_useful_frame_ms']=round((time.monotonic()-started)*1000,2)
    result['initial_screen']=text()
    result['nondefault_color_cells']=sum(1 for row in screen.buffer.values() for cell in row.values() if cell.fg!='default' or cell.bg!='default')
    settle=time.monotonic()+2
    while time.monotonic()<settle:pump()
    latencies=[]
    for index in range(1,11):
        target=f'bench-{index:05}'
        start=time.monotonic();write('/'+target+'\r')
        wait(lambda s: set(re.findall(r'(?<!-)bench-\d{5}\b',s))=={target} and any(target in line and ('Pending' in line or 'SchedulingGated' in line) for line in s.splitlines()),10)
        latencies.append((time.monotonic()-start)*1000)
        write('\x1b');wait(lambda s:len(set(re.findall(r'(?<!-)bench-\d{5}\b',s)))>1,10)
    result['filter_latency_ms']=latencies
    result['filter_p95_ms']=round(sorted(latencies)[math.ceil(len(latencies)*.95)-1],2)
    before_cpu,before_rss=stats();peak=before_rss
    deadline=time.monotonic()+a.idle_seconds
    while time.monotonic()<deadline:
        pump(.25);peak=max(peak,stats()[1])
    after_cpu,after_rss=stats()
    result.update(idle_seconds=a.idle_seconds,idle_cpu_seconds=round(after_cpu-before_cpu,3),rss_before=before_rss,rss_after=after_rss,rss_peak=peak)
except Exception as error:
    result['error']=str(error)
finally:
    try: write(':q\r')
    except OSError: pass
    try: proc.wait(timeout=3)
    except subprocess.TimeoutExpired:
        os.killpg(proc.pid,signal.SIGTERM);proc.wait(timeout=3)
    os.close(master)
    config_directory.cleanup()
    with open(a.output,'w') as f:json.dump(result,f,indent=2)
print(json.dumps({k:v for k,v in result.items() if k!='initial_screen'},indent=2))
if 'error' in result:raise SystemExit(1)
