#!/usr/bin/env python3
"""Matched upstream W1-W5 protocol; raw trials are never discarded or replaced."""
import argparse,datetime,gzip,hashlib,json,os,pathlib,platform,shutil,statistics,subprocess,time
ROOT=pathlib.Path(__file__).resolve().parent
ENGINES=['kiban','golsm','lmdb']
STAGES=['remember','recall-by-id','recent-for-agent','update','mixed-90-10']
def sha(path):return hashlib.sha256(path.read_bytes()).hexdigest()
def dump(path,value):path.write_text(json.dumps(value,indent=2)+'\n')
def disk(root):
 apparent=allocated=0
 for path in root.rglob('*'):
  if path.is_file():s=path.stat();apparent+=s.st_size;allocated+=s.st_blocks*512
 return {'apparent_bytes':apparent,'allocated_bytes':allocated}
def command(cmd,log,samples):
 start=time.monotonic();rows=[]
 with log.open('w') as stream:
  child=subprocess.Popen(cmd,stdout=stream,stderr=subprocess.STDOUT)
  while True:
   now=time.time();rows.append({'epoch':now,'load':[float(x) for x in pathlib.Path('/proc/loadavg').read_text().split()[:3]]})
   status=child.poll()
   if status is not None:break
   if time.monotonic()-start>21600:child.kill();child.wait();raise RuntimeError('trial exceeded six-hour deadline')
   time.sleep(.2)
 with gzip.open(samples,'wt')as f:
  for r in rows:f.write(json.dumps(r)+'\n')
 if status:raise RuntimeError(f'command failed ({status}); see {log}')
 return rows

def run_trial(args,out,binary,engine,mode,n,ops,trial):
 prefix=f'n{n}-{mode}-r{trial}-{engine}';data=out/'databases'/prefix;data.parent.mkdir(exist_ok=True)
 config={'backend':engine,'db':str(data),'endpoint':'','n':n,'operations':ops,'k':10,'seed':20261016,'output':str(out/f'{prefix}.jsonl'),'verify_only':False,'mode':mode,'write_operations':min(ops,10000)if mode=='durable'else ops,'warmup':100}
 configpath=out/f'{prefix}.config.json';dump(configpath,config)
 cmd=['taskset','-c',args.engine_cpus,str(binary),str(configpath)]
 print(datetime.datetime.now().isoformat(),prefix,'START',flush=True)
 rows=command(cmd,out/f'{prefix}.log',out/f'{prefix}.load.jsonl.gz')
 stages=[json.loads(line)for line in pathlib.Path(config['output']).read_text().splitlines()]
 measured=[r for r in stages if r['type']=='stage'];complete=[r for r in stages if r['type']=='complete']
 assert [r['workload']for r in measured]==STAGES
 for i,r in enumerate(measured):
  expected=config['write_operations']if i in(0,3)else ops
  assert r['latency']['count']==expected and r['errors']==0,(prefix,r)
 assert len(complete)==1 and complete[0]['verified_records']==n+config['write_operations']
 resource=json.loads(pathlib.Path(config['output']).with_suffix('.resources.json').read_text())
 expected_cpu=','.join(str(i)for i in sorted(os.sched_getaffinity(0))) # driver is separately pinned
 assert resource['cpus_allowed_list']==args.engine_cpus,resource['cpus_allowed_list']
 settings=resource['engine_settings']
 if engine=='lmdb':assert settings['lmdb_flags_bits']==(0 if mode=='durable'else 0x50000)
 if engine=='golsm':assert settings['per_write_sync']==(mode=='durable') and resource['initial_engine_state']['gomaxprocs']==1 and resource['initial_engine_state']['metrics']['SSTables']==1
 window=[r for r in rows if resource['measurement_start_epoch']<=r['epoch']<=resource['measurement_end_epoch']]+resource['boundary_load_samples']
 window.sort(key=lambda r:r['epoch']);gaps=[b['epoch']-a['epoch']for a,b in zip(window,window[1:])]
 gate=statistics.median(r['load'][0]for r in window)<3 and max(gaps,default=0)<=2.5
 load={'samples':len(window),'max_gap_seconds':max(gaps,default=0),'median_load1':statistics.median(r['load'][0]for r in window),'max_load1':max(r['load'][0]for r in window),'gate_pass':gate}
 reopen={**config,'verify_only':True,'output':str(out/f'{prefix}.reopen.jsonl')};reopenpath=out/f'{prefix}.reopen.config.json';dump(reopenpath,reopen)
 reopen_cmd=['taskset','-c',args.engine_cpus,str(binary),str(reopenpath)]
 command(reopen_cmd,out/f'{prefix}.reopen.log',out/f'{prefix}.reopen.load.jsonl.gz')
 verification=[json.loads(line)for line in pathlib.Path(reopen['output']).read_text().splitlines()]
 assert verification==[{'type':'reopen_verified','verified_records':n+config['write_operations']}],verification
 summary={'prefix':prefix,'engine':engine,'mode':mode,'base_records':n,'operations':ops,'write_operations':config['write_operations'],'final_records':n+config['write_operations'],'trial':trial,'stages':measured,'resources':resource,'disk_after_close':disk(data),'load':load,'reopen_verified':True,'commands':[cmd,reopen_cmd]}
 dump(out/f'{prefix}.summary.json',summary)
 if not args.keep_databases:shutil.rmtree(data)
 print(datetime.datetime.now().isoformat(),prefix,'PASS',[(r['workload'],r['latency']['p50_ns'])for r in measured],flush=True)
 return summary

def main():
 parser=argparse.ArgumentParser();parser.add_argument('--output',required=True);parser.add_argument('--sizes',nargs='+',type=int,default=[10000,1000000]);parser.add_argument('--small-runs',type=int,default=5);parser.add_argument('--large-runs',type=int,default=3);parser.add_argument('--engine-cpus',default='1');parser.add_argument('--driver-cpu',type=int,default=0);parser.add_argument('--keep-databases',action='store_true');parser.add_argument('--engines',nargs='+',choices=ENGINES,default=ENGINES);args=parser.parse_args()
 os.sched_setaffinity(0,{args.driver_cpu});out=pathlib.Path(args.output).resolve();out.mkdir(parents=True,exist_ok=False)
 binary=ROOT/'target/release/golsm-engine-h2h';binary_hash=sha(binary)
 metadata={'started_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'engine_sha256':binary_hash,'go_archive_sha256':sha(ROOT/'build/libgolsm.a'),'kernel':platform.uname()._asdict(),'engine_cpus':args.engine_cpus,'driver_cpu':args.driver_cpu,'engine_versions':{'kiban':'361305a8b63fef309e301e8106c254e60a70ac84','golsm':(ROOT/'build/golsm-revision.txt').read_text().strip(),'lmdb-master3-sys':'0.2.5','heed3':'0.22.0'},'upstream_harness':'082858f86fc38b79e582eb34b7f39aa2368b3af0','args':vars(args),'source_revision':subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip(),'source_hashes':{str(p.relative_to(ROOT)):sha(p)for p in ROOT.glob('src/**/*.rs')}}
 for cmd in [['go','version'],['rustc','--version'],['cargo','--version'],['lscpu'],['free','-h'],['df','-h',str(out)]]:
  metadata[' '.join(cmd)]=subprocess.check_output(cmd,text=True)
 dump(out/'metadata.json',metadata)
 with (out/'calibration.json').open('w')as f:subprocess.run(['taskset','-c',args.engine_cpus,str(binary),'--calibrate'],stdout=f,check=True)
 summaries=[]
 for n in args.sizes:
  reps=args.small_runs if n==10000 else args.large_runs
  ops=10000 if n==10000 else 100000
  for trial in range(1,reps+1):
   for mode in ['durable','fast']:
    for engine in args.engines:
     assert sha(binary)==binary_hash,'binary changed during campaign'
     summaries.append(run_trial(args,out,binary,engine,mode,n,ops,trial));dump(out/'progress.json',{'completed':len(summaries),'trials':summaries})
 dump(out/'complete.json',{'completed':len(summaries),'zero_value_errors':True,'all_reopen_verified':True,'all_load_gates_pass':all(s['load']['gate_pass']for s in summaries),'finished_utc':datetime.datetime.now(datetime.timezone.utc).isoformat()})
 print('CAMPAIGN COMPLETE',len(summaries),flush=True)
if __name__=='__main__':main()
